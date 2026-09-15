package gameserver

import (
	"fmt"
	"sort"
	"time"

	a2s "github.com/rumblefrog/go-a2s"
)

// Info is one server's A2S_INFO reply, flattened to what a listing needs.
type Info struct {
	Address    string        `json:"address"`
	Name       string        `json:"name"`
	Map        string        `json:"map"`
	Folder     string        `json:"folder"`
	Game       string        `json:"game"`
	AppID      int           `json:"appid"`
	Players    int           `json:"players"`
	MaxPlayers int           `json:"max_players"`
	Bots       int           `json:"bots"`
	ServerType string        `json:"server_type"`
	OS         string        `json:"os"`
	Visibility string        `json:"visibility"`
	VAC        bool          `json:"vac"`
	Version    string        `json:"version"`
	Keywords   string        `json:"keywords,omitempty"`
	GamePort   int           `json:"game_port,omitempty"`
	SteamID    uint64        `json:"steamid,omitempty"`
	Ping       time.Duration `json:"-"`
	PingMS     int64         `json:"ping_ms"`
}

// Player is one entry from A2S_PLAYER.
type Player struct {
	Name     string  `json:"name"`
	Score    int     `json:"score"`
	Duration float32 `json:"duration_seconds"`
}

// Query performs A2S_INFO against a server. The address is host:port of the
// *query* port, which for many games is not the game port.
func Query(addr string, timeout time.Duration) (Info, error) {
	c, err := a2s.NewClient(addr, a2s.TimeoutOption(timeout))
	if err != nil {
		return Info{}, fmt.Errorf("connect %s: %w", addr, err)
	}
	defer c.Close()

	start := time.Now()
	raw, err := c.QueryInfo()
	ping := time.Since(start)
	if err != nil {
		return Info{}, fmt.Errorf("query %s: %w", addr, err)
	}

	i := Info{
		Address:    addr,
		Name:       raw.Name,
		Map:        raw.Map,
		Folder:     raw.Folder,
		Game:       raw.Game,
		AppID:      int(raw.ID),
		Players:    int(raw.Players),
		MaxPlayers: int(raw.MaxPlayers),
		Bots:       int(raw.Bots),
		ServerType: raw.ServerType.String(),
		OS:         raw.ServerOS.String(),
		VAC:        raw.VAC,
		Version:    raw.Version,
		Ping:       ping,
		PingMS:     ping.Milliseconds(),
	}
	i.Visibility = "public"
	if raw.Visibility {
		i.Visibility = "password protected"
	}
	if raw.ExtendedServerInfo != nil {
		i.Keywords = raw.ExtendedServerInfo.Keywords
		i.GamePort = int(raw.ExtendedServerInfo.Port)
		i.SteamID = raw.ExtendedServerInfo.SteamID
	}
	return i, nil
}

// QueryPlayers performs A2S_PLAYER, returning the scoreboard.
func QueryPlayers(addr string, timeout time.Duration) ([]Player, error) {
	c, err := a2s.NewClient(addr, a2s.TimeoutOption(timeout))
	if err != nil {
		return nil, fmt.Errorf("connect %s: %w", addr, err)
	}
	defer c.Close()

	raw, err := c.QueryPlayer()
	if err != nil {
		return nil, fmt.Errorf("query players on %s: %w", addr, err)
	}
	out := make([]Player, 0, len(raw.Players))
	for _, p := range raw.Players {
		out = append(out, Player{Name: p.Name, Score: int(p.Score), Duration: p.Duration})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out, nil
}

// QueryRules performs A2S_RULES, the server's convar list.
func QueryRules(addr string, timeout time.Duration) (map[string]string, error) {
	c, err := a2s.NewClient(addr, a2s.TimeoutOption(timeout))
	if err != nil {
		return nil, fmt.Errorf("connect %s: %w", addr, err)
	}
	defer c.Close()

	raw, err := c.QueryRules()
	if err != nil {
		return nil, fmt.Errorf("query rules on %s: %w", addr, err)
	}
	return raw.Rules, nil
}

// QueryMany queries servers concurrently, preserving the input order. Failures
// are returned per address rather than aborting the batch, because a listing is
// still useful when some servers are down or filtered.
type Result struct {
	Address string `json:"address"`
	Info    *Info  `json:"info,omitempty"`
	Error   string `json:"error,omitempty"`
}

func QueryMany(addrs []string, timeout time.Duration, concurrency int) []Result {
	if concurrency <= 0 {
		concurrency = 24
	}
	out := make([]Result, len(addrs))
	sem := make(chan struct{}, concurrency)
	done := make(chan int, len(addrs))

	for i, a := range addrs {
		go func(idx int, addr string) {
			sem <- struct{}{}
			defer func() { <-sem; done <- idx }()
			r := Result{Address: addr}
			if info, err := Query(addr, timeout); err != nil {
				r.Error = err.Error()
			} else {
				r.Info = &info
			}
			out[idx] = r
		}(i, a)
	}
	for range addrs {
		<-done
	}
	return out
}
