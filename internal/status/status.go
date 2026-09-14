package status

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"steamcli.local/steam/internal/httpx"
	"sync"
	"time"
)

type EndpointStatus struct {
	Name      string        `json:"name"`
	URL       string        `json:"url"`
	Status    string        `json:"status"` // "normal", "slow", "down", "error"
	HTTPCode  int           `json:"http_code,omitempty"`
	Latency   time.Duration `json:"latency"`
	LatencyMS int64         `json:"latency_ms"`
	Error     string        `json:"error,omitempty"`
}

type CMStatus struct {
	Server    string        `json:"server"`
	Status    string        `json:"status"` // "online", "unreachable"
	Latency   time.Duration `json:"latency"`
	LatencyMS int64         `json:"latency_ms"`
	Error     string        `json:"error,omitempty"`
}

type CoordinatorStatus struct {
	AppID       int               `json:"appid"`
	Services    map[string]string `json:"services,omitempty"`
	Matchmaking map[string]any    `json:"matchmaking,omitempty"`
	Datacenters map[string]any    `json:"datacenters,omitempty"`
}

type Report struct {
	Timestamp          time.Time           `json:"timestamp"`
	Endpoints          []EndpointStatus    `json:"endpoints"`
	ConnectionManagers []CMStatus          `json:"connection_managers,omitempty"`
	Coordinators       []CoordinatorStatus `json:"coordinators,omitempty"`
	PlayerCounts       map[string]int      `json:"player_counts,omitempty"`
}

type Monitor struct {
	HTTP    *httpx.Client
	WebKey  string
	BaseURL string
}

func (m *Monitor) Check(ctx context.Context, checkCM, checkCoordinator bool) (Report, error) {
	if m.HTTP.Offline {
		return Report{}, errors.New("cannot check Steam status in --offline mode")
	}

	report := Report{
		Timestamp:    time.Now().UTC(),
		PlayerCounts: make(map[string]int),
	}

	var wg sync.WaitGroup

	// 1. Probing primary web endpoints
	endpointTargets := []struct {
		name string
		url  string
	}{
		{"Steam Store", "https://store.steampowered.com/"},
		{"Steam Community", "https://steamcommunity.com/"},
		{"Steam Web API", "https://api.steampowered.com/ISteamWebAPIUtil/GetServerInfo/v1/"},
		{"Steam Help", "https://help.steampowered.com/"},
	}

	endpoints := make([]EndpointStatus, len(endpointTargets))
	for i, target := range endpointTargets {
		wg.Add(1)
		go func(idx int, name, targetURL string) {
			defer wg.Done()
			endpoints[idx] = probeHTTP(ctx, name, targetURL)
		}(i, target.name, target.url)
	}

	// 2. Player count for CS2 (AppID 730)
	var playerCountLock sync.Mutex
	wg.Add(1)
	go func() {
		defer wg.Done()
		count, err := m.fetchPlayerCount(ctx, 730)
		if err == nil {
			playerCountLock.Lock()
			report.PlayerCounts["CS2"] = count
			playerCountLock.Unlock()
		}
	}()

	// 3. Coordinator status (CS2 game coordinator / datacenters)
	var coord CoordinatorStatus
	var hasCoord bool
	if checkCoordinator && m.WebKey != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := m.fetchGameServersStatus(ctx, 730)
			if err == nil {
				coord = c
				hasCoord = true
			}
		}()
	}

	// 4. Connection Managers probe
	var cmList []CMStatus
	if checkCM {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cmList = m.probeCMs(ctx)
		}()
	}

	wg.Wait()

	report.Endpoints = endpoints
	if hasCoord {
		report.Coordinators = append(report.Coordinators, coord)
	}
	report.ConnectionManagers = cmList

	return report, nil
}

func probeHTTP(ctx context.Context, name, targetURL string) EndpointStatus {
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return EndpointStatus{
			Name:   name,
			URL:    targetURL,
			Status: "error",
			Error:  err.Error(),
		}
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) steam-cli/0.1")

	client := &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}

	resp, err := client.Do(req)
	duration := time.Since(start)
	ms := duration.Milliseconds()

	if err != nil {
		return EndpointStatus{
			Name:      name,
			URL:       targetURL,
			Status:    "down",
			Latency:   duration,
			LatencyMS: ms,
			Error:     err.Error(),
		}
	}
	defer resp.Body.Close()

	status := "normal"
	if ms > 1500 {
		status = "slow"
	}
	if resp.StatusCode >= 500 {
		status = "down"
	}

	return EndpointStatus{
		Name:      name,
		URL:       targetURL,
		Status:    status,
		HTTPCode:  resp.StatusCode,
		Latency:   duration,
		LatencyMS: ms,
	}
}

func (m *Monitor) fetchPlayerCount(ctx context.Context, appID int) (int, error) {
	targetURL := fmt.Sprintf("https://api.steampowered.com/ISteamUserStats/GetNumberOfCurrentPlayers/v1/?appid=%d", appID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) steam-cli/0.1")
	client := &http.Client{Timeout: 4 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	var res struct {
		Response struct {
			PlayerCount int `json:"player_count"`
			Result      int `json:"result"`
		} `json:"response"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return 0, err
	}
	if res.Response.Result != 1 {
		return 0, errors.New("player count not available")
	}
	return res.Response.PlayerCount, nil
}

func (m *Monitor) fetchGameServersStatus(ctx context.Context, appID int) (CoordinatorStatus, error) {
	targetURL := fmt.Sprintf("https://api.steampowered.com/ICSGOServers_%d/GetGameServersStatus/v1/", appID)
	q := url.Values{"key": {m.WebKey}}
	data, err := m.HTTP.Do(ctx, http.MethodGet, targetURL, q, nil, nil)
	if err != nil {
		return CoordinatorStatus{}, err
	}
	var res struct {
		Result struct {
			Services    map[string]string `json:"services"`
			Matchmaking map[string]any    `json:"matchmaking"`
			Datacenters map[string]any    `json:"datacenters"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &res); err != nil {
		return CoordinatorStatus{}, err
	}
	return CoordinatorStatus{
		AppID:       appID,
		Services:    res.Result.Services,
		Matchmaking: res.Result.Matchmaking,
		Datacenters: res.Result.Datacenters,
	}, nil
}

func (m *Monitor) probeCMs(ctx context.Context) []CMStatus {
	targetURL := "https://api.steampowered.com/ISteamDirectory/GetCMList/v1/?cellid=0"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) steam-cli/0.1")
	client := &http.Client{Timeout: 4 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	var res struct {
		Response struct {
			ServerList []string `json:"serverlist"`
		} `json:"response"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil || len(res.Response.ServerList) == 0 {
		return nil
	}

	servers := res.Response.ServerList
	if len(servers) > 5 {
		servers = servers[:5]
	}

	results := make([]CMStatus, len(servers))
	var wg sync.WaitGroup
	for i, s := range servers {
		wg.Add(1)
		go func(idx int, srv string) {
			defer wg.Done()
			results[idx] = probeSocket(srv)
		}(i, s)
	}
	wg.Wait()
	return results
}

func probeSocket(addr string) CMStatus {
	start := time.Now()
	conn, err := net.DialTimeout("tcp", addr, 1500*time.Millisecond)
	duration := time.Since(start)
	ms := duration.Milliseconds()

	if err != nil {
		return CMStatus{
			Server:    addr,
			Status:    "unreachable",
			Latency:   duration,
			LatencyMS: ms,
			Error:     err.Error(),
		}
	}
	conn.Close()
	return CMStatus{
		Server:    addr,
		Status:    "online",
		Latency:   duration,
		LatencyMS: ms,
	}
}
