// Package status probes Steam's public infrastructure: web endpoints,
// connection managers, game coordinators, and live player counts.
package status

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"sync"
	"time"

	"steamcli.local/steam/internal/httpx"
)

// SlowThreshold is the latency above which an endpoint is reported as slow
// rather than normal.
const SlowThreshold = 1500 * time.Millisecond

type EndpointStatus struct {
	Name      string `json:"name"`
	URL       string `json:"url"`
	Status    string `json:"status"` // normal, slow, down, error
	Error     string `json:"error,omitempty"`
	HTTPCode  int    `json:"http_code,omitempty"`
	LatencyMS int64  `json:"latency_ms"`
}

type CMStatus struct {
	Server    string `json:"server"`
	Status    string `json:"status"` // online, unreachable
	Error     string `json:"error,omitempty"`
	LatencyMS int64  `json:"latency_ms"`
}

type CoordinatorStatus struct {
	Services    map[string]string `json:"services,omitempty"`
	Matchmaking map[string]any    `json:"matchmaking,omitempty"`
	Datacenters map[string]any    `json:"datacenters,omitempty"`
	Name        string            `json:"name"`
	Error       string            `json:"error,omitempty"`
	AppID       int               `json:"appid"`
}

type PlayerCount struct {
	Name  string `json:"name"`
	Error string `json:"error,omitempty"`
	AppID int    `json:"appid"`
	Count int    `json:"count"`
}

type Report struct {
	Timestamp          time.Time           `json:"timestamp"`
	Endpoints          []EndpointStatus    `json:"endpoints"`
	PlayerCounts       []PlayerCount       `json:"player_counts,omitempty"`
	Coordinators       []CoordinatorStatus `json:"coordinators,omitempty"`
	ConnectionManagers []CMStatus          `json:"connection_managers,omitempty"`
	Warnings           []string            `json:"warnings,omitempty"`
}

// TrackedApp is a title whose live player count is reported.
type TrackedApp struct {
	Name  string
	AppID int
}

// DefaultApps are the titles reported when none are requested explicitly.
var DefaultApps = []TrackedApp{
	{"Counter-Strike 2", 730},
	{"Dota 2", 570},
	{"PUBG", 578080},
	{"Apex Legends", 1172470},
	{"Rust", 252490},
	{"Team Fortress 2", 440},
	{"Grand Theft Auto V", 271590},
	{"Baldur's Gate 3", 1086940},
}

// CoordinatorApps are the titles exposing an ICSGOServers-style status
// interface. Valve only ever shipped this shape for CS2.
var CoordinatorApps = []TrackedApp{{"Counter-Strike 2", 730}}

type Monitor struct {
	HTTP *httpx.Client
	// WebAPIURL, StoreURL, CommunityURL and HelpURL are overridable so the
	// probes can be pointed at a test server; all default to Valve's hosts.
	WebAPIURL    string
	StoreURL     string
	CommunityURL string
	HelpURL      string
	WebKey       string
	Apps         []TrackedApp
	CMLimit      int
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func (m *Monitor) webAPI() string {
	return firstNonEmpty(m.WebAPIURL, "https://api.steampowered.com")
}

func (m *Monitor) targets() []struct{ name, url string } {
	return []struct{ name, url string }{
		{"Steam Store", firstNonEmpty(m.StoreURL, "https://store.steampowered.com") + "/"},
		{"Steam Community", firstNonEmpty(m.CommunityURL, "https://steamcommunity.com") + "/"},
		{"Steam Web API", m.webAPI() + "/ISteamWebAPIUtil/GetServerInfo/v1/"},
		{"Steam Help", firstNonEmpty(m.HelpURL, "https://help.steampowered.com") + "/"},
	}
}

func (m *Monitor) apps() []TrackedApp {
	if len(m.Apps) > 0 {
		return m.Apps
	}
	return DefaultApps
}

// Check runs every probe concurrently and returns a single report. Individual
// probe failures are recorded in the report rather than failing the whole run.
func (m *Monitor) Check(ctx context.Context, checkCM, checkCoordinator bool) (Report, error) {
	if m.HTTP == nil {
		return Report{}, errors.New("status monitor requires an HTTP client")
	}
	if m.HTTP.Offline {
		return Report{}, errors.New("cannot check Steam status in --offline mode")
	}

	report := Report{Timestamp: time.Now().UTC()}
	var wg sync.WaitGroup

	targets := m.targets()
	endpoints := make([]EndpointStatus, len(targets))
	for i, t := range targets {
		wg.Add(1)
		go func(idx int, name, target string) {
			defer wg.Done()
			endpoints[idx] = m.probeHTTP(ctx, name, target)
		}(i, t.name, t.url)
	}

	apps := m.apps()
	counts := make([]PlayerCount, len(apps))
	for i, a := range apps {
		wg.Add(1)
		go func(idx int, app TrackedApp) {
			defer wg.Done()
			pc := PlayerCount{Name: app.Name, AppID: app.AppID}
			n, err := m.fetchPlayerCount(ctx, app.AppID)
			if err != nil {
				pc.Error = err.Error()
			} else {
				pc.Count = n
			}
			counts[idx] = pc
		}(i, a)
	}

	var coords []CoordinatorStatus
	if checkCoordinator {
		if m.WebKey == "" {
			report.Warnings = append(report.Warnings,
				"game coordinator status skipped: no Steam Web API key configured (set STEAM_WEB_API_KEY)")
		} else {
			coords = make([]CoordinatorStatus, len(CoordinatorApps))
			for i, a := range CoordinatorApps {
				wg.Add(1)
				go func(idx int, app TrackedApp) {
					defer wg.Done()
					c, err := m.fetchGameServersStatus(ctx, app.AppID)
					c.Name, c.AppID = app.Name, app.AppID
					if err != nil {
						c.Error = err.Error()
					}
					coords[idx] = c
				}(i, a)
			}
		}
	}

	var cmList []CMStatus
	if checkCM {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var err error
			cmList, err = m.probeCMs(ctx)
			if err != nil {
				cmList = nil
			}
		}()
	}

	wg.Wait()

	report.Endpoints = endpoints
	report.PlayerCounts = counts
	report.Coordinators = coords
	report.ConnectionManagers = cmList
	return report, nil
}

func (m *Monitor) probeHTTP(ctx context.Context, name, target string) EndpointStatus {
	start := time.Now()
	r, err := m.HTTP.DoFull(ctx, http.MethodGet, target, nil, nil, nil)
	ms := time.Since(start).Milliseconds()

	st := EndpointStatus{Name: name, URL: target, LatencyMS: ms}
	if err != nil {
		var se *httpx.StatusError
		if errors.As(err, &se) {
			// A non-2xx answer still proves the host is serving traffic.
			st.HTTPCode = se.Code
			st.Status = "normal"
			if se.Code >= 500 {
				st.Status = "down"
			} else if ms > SlowThreshold.Milliseconds() {
				st.Status = "slow"
			}
			return st
		}
		st.Status = "down"
		st.Error = err.Error()
		return st
	}

	st.HTTPCode = r.Status
	st.Status = "normal"
	if ms > SlowThreshold.Milliseconds() {
		st.Status = "slow"
	}
	return st
}

func (m *Monitor) fetchPlayerCount(ctx context.Context, appID int) (int, error) {
	endpoint, err := httpx.Endpoint(m.webAPI(), "ISteamUserStats/GetNumberOfCurrentPlayers/v1/", m.HTTP.AllowHTTP)
	if err != nil {
		return 0, err
	}
	body, err := m.HTTP.Do(ctx, http.MethodGet, endpoint,
		url.Values{"appid": {strconv.Itoa(appID)}}, nil, nil)
	if err != nil {
		return 0, err
	}
	var res struct {
		Response struct {
			PlayerCount int `json:"player_count"`
			Result      int `json:"result"`
		} `json:"response"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return 0, fmt.Errorf("decode player count: %w", err)
	}
	if res.Response.Result != 1 {
		return 0, fmt.Errorf("player count unavailable (result %d)", res.Response.Result)
	}
	return res.Response.PlayerCount, nil
}

func (m *Monitor) fetchGameServersStatus(ctx context.Context, appID int) (CoordinatorStatus, error) {
	endpoint, err := httpx.Endpoint(m.webAPI(),
		fmt.Sprintf("ICSGOServers_%d/GetGameServersStatus/v1/", appID), m.HTTP.AllowHTTP)
	if err != nil {
		return CoordinatorStatus{}, err
	}
	body, err := m.HTTP.Do(ctx, http.MethodGet, endpoint, url.Values{"key": {m.WebKey}}, nil, nil)
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
	if err := json.Unmarshal(body, &res); err != nil {
		return CoordinatorStatus{}, fmt.Errorf("decode coordinator status: %w", err)
	}
	return CoordinatorStatus{
		Services:    res.Result.Services,
		Matchmaking: res.Result.Matchmaking,
		Datacenters: res.Result.Datacenters,
	}, nil
}

func (m *Monitor) probeCMs(ctx context.Context) ([]CMStatus, error) {
	endpoint, err := httpx.Endpoint(m.webAPI(), "ISteamDirectory/GetCMList/v1/", m.HTTP.AllowHTTP)
	if err != nil {
		return nil, err
	}
	body, err := m.HTTP.Do(ctx, http.MethodGet, endpoint, url.Values{"cellid": {"0"}}, nil, nil)
	if err != nil {
		return nil, err
	}
	var res struct {
		Response struct {
			ServerList []string `json:"serverlist"`
		} `json:"response"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, fmt.Errorf("decode CM list: %w", err)
	}
	servers := res.Response.ServerList
	if len(servers) == 0 {
		return nil, errors.New("Steam returned an empty connection manager list")
	}
	sort.Strings(servers)
	limit := m.CMLimit
	if limit <= 0 {
		limit = 5
	}
	if len(servers) > limit {
		servers = servers[:limit]
	}

	results := make([]CMStatus, len(servers))
	var wg sync.WaitGroup
	for i, s := range servers {
		wg.Add(1)
		go func(idx int, srv string) {
			defer wg.Done()
			results[idx] = probeSocket(ctx, srv)
		}(i, s)
	}
	wg.Wait()
	return results, nil
}

func probeSocket(ctx context.Context, addr string) CMStatus {
	var d net.Dialer
	ctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()

	start := time.Now()
	conn, err := d.DialContext(ctx, "tcp", addr)
	ms := time.Since(start).Milliseconds()

	if err != nil {
		return CMStatus{Server: addr, Status: "unreachable", LatencyMS: ms, Error: err.Error()}
	}
	conn.Close()
	return CMStatus{Server: addr, Status: "online", LatencyMS: ms}
}
