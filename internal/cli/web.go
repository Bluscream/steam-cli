package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jedib0t/go-pretty/v6/table"
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
		aliases                    []string
		version, n                 int
		build                      func([]string) url.Values
		// render prints a human view of the response. It reports false when
		// the payload is not the shape it expects, so the raw JSON is printed
		// instead of a misleading table.
		render func(*options, io.Writer, []byte) bool
	}
	helpers := []helper{
		{"server-info", "Steam server time and version", "ISteamWebAPIUtil", "GetServerInfo", nil, 1, 0, func([]string) url.Values { return nil }, nil},
		{"player STEAMID[,STEAMID...]", "Player summaries (SteamID64)", "ISteamUser", "GetPlayerSummaries",
			[]string{"profile", "profiles"}, 2, 1,
			func(a []string) url.Values { return url.Values{"steamids": {a[0]}} }, renderPlayers},
		{"resolve VANITY", "Resolve a vanity profile name to SteamID64", "ISteamUser", "ResolveVanityURL", nil, 1, 1, func(a []string) url.Values { return url.Values{"vanityurl": {a[0]}} }, nil},
		{"owned STEAMID", "Owned games visible to your API key", "IPlayerService", "GetOwnedGames", nil, 1, 1, func(a []string) url.Values {
			return url.Values{"steamid": {a[0]}, "include_appinfo": {"1"}, "include_played_free_games": {"1"}}
		}, nil},
		{"recent STEAMID", "Recently played games", "IPlayerService", "GetRecentlyPlayedGames", nil, 1, 1, func(a []string) url.Values { return url.Values{"steamid": {a[0]}} }, nil},
		{"friends STEAMID", "Visible friend list", "ISteamUser", "GetFriendList", nil, 1, 1, func(a []string) url.Values { return url.Values{"steamid": {a[0]}, "relationship": {"friend"}} }, nil},
		{"bans STEAMID[,STEAMID...]", "Public player ban information", "ISteamUser", "GetPlayerBans", nil, 1, 1, func(a []string) url.Values { return url.Values{"steamids": {a[0]}} }, nil},
		{"achievements STEAMID APPID", "Player achievements for a game", "ISteamUserStats", "GetPlayerAchievements", nil, 1, 2, func(a []string) url.Values { return url.Values{"steamid": {a[0]}, "appid": {a[1]}} }, nil},
		{"news APPID", "Recent game news", "ISteamNews", "GetNewsForApp", nil, 2, 1, func(a []string) url.Values { return url.Values{"appid": {a[0]}, "count": {strconv.Itoa(10)}} }, nil},
		{"players APPID", "Current player count", "ISteamUserStats", "GetNumberOfCurrentPlayers", nil, 1, 1, func(a []string) url.Values { return url.Values{"appid": {a[0]}} }, nil},
	}
	for _, h := range helpers {
		root.AddCommand(&cobra.Command{Use: h.name, Aliases: h.aliases, Short: h.short, Args: cobra.ExactArgs(h.n), RunE: func(cmd *cobra.Command, args []string) error {
			c, e := client()
			if e != nil {
				return e
			}
			b, e := c.Call(cmd.Context(), h.iface, h.method, h.version, "GET", h.build(args))
			if e != nil {
				return e
			}
			if h.render != nil && o.human() {
				if h.render(o, cmd.OutOrStdout(), b) {
					return nil
				}
			}
			return o.printBytes(cmd, b)
		}})
	}
	root.AddCommand(workshopCommand(o))
	return root
}

// --- player summaries -------------------------------------------------------

type playerSummary struct {
	SteamID    string `json:"steamid"`
	Persona    string `json:"personaname"`
	RealName   string `json:"realname"`
	ProfileURL string `json:"profileurl"`
	Avatar     string `json:"avatarfull"`
	// Steam omits these for profiles the key cannot see, so they stay pointers
	// and an absent field is rendered as unknown rather than as zero.
	State      *int   `json:"personastate"`
	Visibility *int   `json:"communityvisibilitystate"`
	Configured *int   `json:"profilestate"`
	LastLogoff *int64 `json:"lastlogoff"`
	Created    *int64 `json:"timecreated"`
	Country    string `json:"loccountrycode"`
	StateCode  string `json:"locstatecode"`
	ClanID     string `json:"primaryclanid"`
	GameID     string `json:"gameid"`
	GameName   string `json:"gameextrainfo"`
	GameServer string `json:"gameserverip"`
}

// personaStates are Valve's EPersonaState values.
var personaStates = map[int]string{
	0: "Offline", 1: "Online", 2: "Busy", 3: "Away",
	4: "Snooze", 5: "Looking to trade", 6: "Looking to play",
}

func (p playerSummary) status() string {
	if p.GameName != "" {
		return "In game: " + p.GameName
	}
	if p.State == nil {
		return "unknown"
	}
	if s, ok := personaStates[*p.State]; ok {
		return s
	}
	return fmt.Sprintf("state %d", *p.State)
}

func (p playerSummary) visibility() string {
	if p.Visibility == nil {
		return "unknown"
	}
	// ECommunityVisibilityState. Valve's own documentation mentions only 1 and
	// 3, but live profiles do return 2.
	switch *p.Visibility {
	case 1:
		return "private"
	case 2:
		return "friends only"
	case 3:
		return "public"
	}
	return fmt.Sprintf("visibility %d", *p.Visibility)
}

func stamp(t *int64) string {
	if t == nil || *t <= 0 {
		return ""
	}
	return time.Unix(*t, 0).UTC().Format("2006-01-02 15:04 UTC")
}

// renderPlayers prints GetPlayerSummaries: one profile as a detail view, several
// as a table. It reports false if the payload is not a player summary.
func renderPlayers(o *options, w io.Writer, b []byte) bool {
	var res struct {
		Response struct {
			Players []playerSummary `json:"players"`
		} `json:"response"`
	}
	if json.Unmarshal(b, &res) != nil {
		return false
	}
	players := res.Response.Players
	if len(players) == 0 {
		// A valid request for a profile nobody can see returns an empty list.
		if strings.Contains(string(b), `"players"`) {
			fmt.Fprintln(w, "No profile returned. The SteamID may not exist, or the profile is not visible to this key.")
			return true
		}
		return false
	}

	sort.Slice(players, func(i, j int) bool { return players[i].Persona < players[j].Persona })

	if len(players) > 1 {
		t := o.newTable(w)
		t.AppendHeader(table.Row{"Persona", "SteamID64", "Status", "Visibility", "Country"})
		for _, p := range players {
			t.AppendRow(table.Row{truncate(p.Persona, 28), p.SteamID,
				colorPersona(p), colorVisibility(p.visibility()), p.Country})
		}
		t.Render()
		fmt.Fprintln(w, faint(fmt.Sprintf("%d profile(s).", len(players))))
		return true
	}

	p := players[0]
	t := o.newDetail(w)
	rows := []([2]string){
		kv("Persona", p.Persona),
		kv("Real name", p.RealName),
		kv("SteamID64", p.SteamID),
	}
	if ids, err := convertID(p.SteamID); err == nil {
		rows = append(rows, kv("SteamID3", ids["steamid3"]), kv("SteamID2", ids["steamid2"]))
	}
	rows = append(rows, kv("Status", colorPersona(p)))
	if p.GameName != "" {
		rows = append(rows, kv("Game", p.GameName+gameSuffix(p)), kv("Game server", p.GameServer))
	}
	rows = append(rows, kv("Visibility", colorVisibility(p.visibility())))
	if p.Configured != nil && *p.Configured == 0 {
		rows = append(rows, kv("Profile", yellow.Sprint("not set up")))
	}
	rows = append(rows,
		kv("Country", locality(p)),
		kv("Created", stamp(p.Created)),
	)
	if p.GameName == "" {
		rows = append(rows, kv("Last seen", stamp(p.LastLogoff)))
	}
	rows = append(rows,
		kv("Primary group", p.ClanID),
		kv("Profile URL", p.ProfileURL),
		kv("Avatar", p.Avatar),
	)
	detailRows(t, rows...)
	t.Render()
	return true
}

// colorPersona greens an online player, greens-with-emphasis one in a game,
// and dims an offline one.
func colorPersona(p playerSummary) string {
	s := p.status()
	if p.GameName != "" {
		return green.Sprint(s)
	}
	if p.State != nil && *p.State == 0 {
		return faint(s)
	}
	if p.State != nil && *p.State != 0 {
		return green.Sprint(s)
	}
	return s
}

func colorVisibility(v string) string {
	switch v {
	case "public":
		return green.Sprint(v)
	case "private":
		return red.Sprint(v)
	case "friends only":
		return yellow.Sprint(v)
	}
	return v
}

func gameSuffix(p playerSummary) string {
	if p.GameID == "" {
		return ""
	}
	return " (AppID " + p.GameID + ")"
}

func locality(p playerSummary) string {
	if p.Country == "" {
		return ""
	}
	if p.StateCode != "" {
		return p.Country + "-" + p.StateCode
	}
	return p.Country
}
