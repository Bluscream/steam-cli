package cli

import (
	"encoding/json"
	"errors"
	"net/url"
	"strconv"

	"github.com/spf13/cobra"
	"steamcli.local/steam/internal/webapi"
)

func webCommand(o *options) *cobra.Command {
	var base, catalog string
	var noKey bool
	root := &cobra.Command{Use: "web", Short: "Discover and call Steam Web API methods"}
	root.PersistentFlags().StringVar(&base, "url", "", "Override API base URL (also supports the partner API)")
	root.PersistentFlags().StringVar(&catalog, "catalog", "all", "Discovery source: all (live + bundled xPaw), live, xpaw")
	root.PersistentFlags().BoolVar(&noKey, "no-key", false, "Do not load or send a configured API key")
	client := func() (*webapi.Client, error) {
		s, e := o.settings()
		if e != nil {
			return nil, e
		}
		k := ""
		if !noKey {
			k, e = s.WebKey()
			if e != nil {
				return nil, e
			}
		}
		u := s.WebURL
		if base != "" {
			u = base
		}
		token := ""
		if !noKey {
			token, e = s.AccessToken()
			if e != nil {
				return nil, e
			}
		}
		return &webapi.Client{HTTP: o.http(), BaseURL: u, Key: k, CacheDir: s.CacheDir, AccessToken: token}, nil
	}
	var refresh bool
	methods := &cobra.Command{Use: "methods [FILTER]", Short: "List method signatures; cache them for offline browsing", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		c, e := client()
		if e != nil {
			return e
		}
		v, e := c.Discover(cmd.Context(), catalog, refresh)
		if e != nil {
			return e
		}
		filter := ""
		if len(args) > 0 {
			filter = args[0]
		}
		return o.print(cmd, v.Filter(filter))
	}}
	methods.Flags().BoolVar(&refresh, "refresh", false, "Refresh the cached API catalog")
	var verb, input string
	var version int
	var values []string
	call := &cobra.Command{Use: "call INTERFACE METHOD [NAME=VALUE...]", Short: "Call any method; discover HTTP verb/version unless explicitly supplied", Args: cobra.MinimumNArgs(2), Example: "  steam web call ISteamUser GetPlayerSummaries steamids=76561197960287930\n  steam web call IPlayerService GetOwnedGames --input-json '{\"steamid\":\"76561197960287930\"}'", RunE: func(cmd *cobra.Command, args []string) error {
		c, e := client()
		if e != nil {
			return e
		}
		p, e := params(append(values, args[2:]...))
		if e != nil {
			return e
		}
		if input != "" {
			b, e := bodyInput(cmd, input)
			if e != nil {
				return e
			}
			if !json.Valid(b) {
				return errors.New("input_json must be valid JSON")
			}
			p.Set("input_json", string(b))
		}
		v, method := version, verb
		if method == "" {
			schema, e := c.Discover(cmd.Context(), catalog, false)
			if e != nil {
				return e
			}
			m, e := schema.Resolve(args[0], args[1], v)
			if e != nil {
				return e
			}
			v = m.Version
			method = m.HTTPMethod
			if method == "" {
				return errors.New("the reference does not know this method’s HTTP verb; specify --method GET or POST explicitly")
			}
		} else if v == 0 {
			v = 1
		}
		b, e := c.Call(cmd.Context(), args[0], args[1], v, method, p)
		if e != nil {
			return e
		}
		return o.printBytes(cmd, b)
	}}
	call.Flags().StringVarP(&verb, "method", "X", "", "Explicit GET/POST bypasses discovery")
	call.Flags().IntVar(&version, "api-version", 0, "API method version (discovered latest, or 1 for explicit --method)")
	call.Flags().StringArrayVarP(&values, "param", "p", nil, "Parameter NAME=VALUE; repeat for arrays, e.g. appids[0]=730")
	call.Flags().StringVar(&input, "input-json", "", "Service input_json: JSON, @file, or - for stdin")
	root.AddCommand(methods, call)
	type helper struct {
		name, short, iface, method string
		version, n                 int
		build                      func([]string) url.Values
	}
	helpers := []helper{
		{"server-info", "Steam server time and version", "ISteamWebAPIUtil", "GetServerInfo", 1, 0, func([]string) url.Values { return nil }},
		{"player STEAMID[,STEAMID...]", "Player summaries (SteamID64)", "ISteamUser", "GetPlayerSummaries", 2, 1, func(a []string) url.Values { return url.Values{"steamids": {a[0]}} }},
		{"resolve VANITY", "Resolve a vanity profile name to SteamID64", "ISteamUser", "ResolveVanityURL", 1, 1, func(a []string) url.Values { return url.Values{"vanityurl": {a[0]}} }},
		{"owned STEAMID", "Owned games visible to your API key", "IPlayerService", "GetOwnedGames", 1, 1, func(a []string) url.Values {
			return url.Values{"steamid": {a[0]}, "include_appinfo": {"1"}, "include_played_free_games": {"1"}}
		}},
		{"recent STEAMID", "Recently played games", "IPlayerService", "GetRecentlyPlayedGames", 1, 1, func(a []string) url.Values { return url.Values{"steamid": {a[0]}} }},
		{"friends STEAMID", "Visible friend list", "ISteamUser", "GetFriendList", 1, 1, func(a []string) url.Values { return url.Values{"steamid": {a[0]}, "relationship": {"friend"}} }},
		{"bans STEAMID[,STEAMID...]", "Public player ban information", "ISteamUser", "GetPlayerBans", 1, 1, func(a []string) url.Values { return url.Values{"steamids": {a[0]}} }},
		{"achievements STEAMID APPID", "Player achievements for a game", "ISteamUserStats", "GetPlayerAchievements", 1, 2, func(a []string) url.Values { return url.Values{"steamid": {a[0]}, "appid": {a[1]}} }},
		{"news APPID", "Recent game news", "ISteamNews", "GetNewsForApp", 2, 1, func(a []string) url.Values { return url.Values{"appid": {a[0]}, "count": {strconv.Itoa(10)}} }},
		{"players APPID", "Current player count", "ISteamUserStats", "GetNumberOfCurrentPlayers", 1, 1, func(a []string) url.Values { return url.Values{"appid": {a[0]}} }},
	}
	for _, h := range helpers {
		root.AddCommand(&cobra.Command{Use: h.name, Short: h.short, Args: cobra.ExactArgs(h.n), RunE: func(cmd *cobra.Command, args []string) error {
			c, e := client()
			if e != nil {
				return e
			}
			b, e := c.Call(cmd.Context(), h.iface, h.method, h.version, "GET", h.build(args))
			if e != nil {
				return e
			}
			return o.printBytes(cmd, b)
		}})
	}
	root.AddCommand(workshopCommand(o))
	return root
}
