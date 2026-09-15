// Package gameserver reproduces Steam's Game Servers browser: the master-list
// search, direct A2S queries against individual servers, and the local
// favorites and history lists the client keeps.
package gameserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"steamcli.local/steam/internal/webapi"
)

// Server is one entry from the master list.
type Server struct {
	Addr       string `json:"addr"`
	GamePort   int    `json:"gameport"`
	SteamID    string `json:"steamid"`
	Name       string `json:"name"`
	AppID      int    `json:"appid"`
	GameDir    string `json:"gamedir"`
	Version    string `json:"version"`
	Product    string `json:"product"`
	Region     int    `json:"region"`
	Players    int    `json:"players"`
	MaxPlayers int    `json:"max_players"`
	Bots       int    `json:"bots"`
	Map        string `json:"map"`
	Secure     bool   `json:"secure"`
	Dedicated  bool   `json:"dedicated"`
	OS         string `json:"os"`
	GameType   string `json:"gametype"`
}

// Filter builds the backslash-delimited filter string the master list expects.
// Valve documents the syntax on the Master Server Query Protocol page; the
// fields here are the ones the client's own browser exposes.
type Filter struct {
	AppID       int
	Map         string
	GameDir     string
	GameType    []string
	Name        string
	Address     string
	Version     string
	Region      int
	NotEmpty    bool
	NotFull     bool
	Secure      bool
	Dedicated   bool
	LinuxOnly   bool
	NoPassword  bool
	HasPlayers  bool
	WhiteListed bool
}

// String renders the filter. Region is deliberately absent from the string
// form: the master list takes it as its own parameter, not a filter term.
func (f Filter) String() string {
	var b strings.Builder
	add := func(k, v string) { fmt.Fprintf(&b, `\%s\%s`, k, v) }

	if f.AppID > 0 {
		add("appid", strconv.Itoa(f.AppID))
	}
	if f.Map != "" {
		add("map", f.Map)
	}
	if f.GameDir != "" {
		add("gamedir", f.GameDir)
	}
	if len(f.GameType) > 0 {
		add("gametype", strings.Join(f.GameType, ","))
	}
	if f.Name != "" {
		add("name_match", f.Name)
	}
	if f.Address != "" {
		add("gameaddr", f.Address)
	}
	if f.Version != "" {
		add("version_match", f.Version)
	}
	if f.NotEmpty {
		add("empty", "1")
	}
	if f.HasPlayers {
		add("noplayers", "0")
	}
	if f.NotFull {
		add("full", "1")
	}
	if f.Secure {
		add("secure", "1")
	}
	if f.Dedicated {
		add("dedicated", "1")
	}
	if f.LinuxOnly {
		add("linux", "1")
	}
	if f.NoPassword {
		add("password", "0")
	}
	if f.WhiteListed {
		add("white", "1")
	}
	return b.String()
}

// Browse queries the master list. It needs a Web API key.
func Browse(ctx context.Context, c *webapi.Client, f Filter, limit int) ([]Server, error) {
	if limit <= 0 {
		limit = 100
	}
	params := url.Values{"limit": {strconv.Itoa(limit)}}
	if s := f.String(); s != "" {
		params.Set("filter", s)
	}
	b, err := c.Call(ctx, "IGameServersService", "GetServerList", 1, "GET", params)
	if err != nil {
		return nil, err
	}
	var res struct {
		Response struct {
			Servers []Server `json:"servers"`
		} `json:"response"`
	}
	if err := json.Unmarshal(b, &res); err != nil {
		return nil, fmt.Errorf("decode server list: %w", err)
	}
	out := res.Response.Servers
	sort.SliceStable(out, func(i, j int) bool { return out[i].Players > out[j].Players })
	return out, nil
}
