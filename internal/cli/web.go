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
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
	"steamcli.local/steam/internal/community"
	"steamcli.local/steam/internal/library"
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
	defaultSteamID := func() (string, error) {
		s, err := o.settings()
		if err == nil {
			if id, err := s.SteamUserID(); err == nil && id != "" {
				return id, nil
			}
			if cLogin, err := s.CommunityLoginSecure(); err == nil && cLogin != "" {
				client := &community.Client{LoginSecure: cLogin}
				if id, err := client.SteamID(); err == nil && id != "" {
					return id, nil
				}
			}
		}
		if id, err := library.LoggedInUser(nil); err == nil && id != "" {
			return id, nil
		}
		return "", errors.New("no SteamID specified, and unable to detect logged-in user (set STEAM_USER_ID, STEAM_LOGIN_SECURE, or log in to Steam desktop)")
	}

	type helper struct {
		name, short, iface, method string
		aliases                    []string
		version                    int
		args                       cobra.PositionalArgs
		build                      func(*cobra.Command, []string) (url.Values, error)
		// render prints a human view of the response. It reports false when
		// the payload is not the shape it expects, so the raw JSON is printed
		// instead of a misleading table.
		render func(*options, io.Writer, []byte) bool
	}
	helpers := []helper{
		{"server-info", "Steam server time and version", "ISteamWebAPIUtil", "GetServerInfo", nil, 1, cobra.NoArgs, func(*cobra.Command, []string) (url.Values, error) { return nil, nil }, renderServerInfo},
		{"player [STEAMID[,STEAMID...]]", "Player summaries (defaults to logged-in user)", "ISteamUser", "GetPlayerSummaries",
			[]string{"profile", "profiles"}, 2, cobra.MaximumNArgs(1),
			func(cmd *cobra.Command, a []string) (url.Values, error) {
				id := ""
				if len(a) > 0 && a[0] != "" {
					id = a[0]
				} else {
					var err error
					id, err = defaultSteamID()
					if err != nil {
						return nil, err
					}
				}
				return url.Values{"steamids": {id}}, nil
			}, renderPlayers},
		{"resolve VANITY", "Resolve a vanity profile name to SteamID64", "ISteamUser", "ResolveVanityURL", nil, 1, cobra.ExactArgs(1), func(cmd *cobra.Command, a []string) (url.Values, error) { return url.Values{"vanityurl": {a[0]}}, nil }, renderResolve},
		{"owned [STEAMID]", "Owned games visible to your API key (defaults to logged-in user)", "IPlayerService", "GetOwnedGames", nil, 1, cobra.MaximumNArgs(1), func(cmd *cobra.Command, a []string) (url.Values, error) {
			id := ""
			if len(a) > 0 && a[0] != "" {
				id = a[0]
			} else {
				var err error
				id, err = defaultSteamID()
				if err != nil {
					return nil, err
				}
			}
			return url.Values{"steamid": {id}, "include_appinfo": {"1"}, "include_played_free_games": {"1"}}, nil
		}, renderOwned},
		{"recent [STEAMID]", "Recently played games (defaults to logged-in user)", "IPlayerService", "GetRecentlyPlayedGames", nil, 1, cobra.MaximumNArgs(1), func(cmd *cobra.Command, a []string) (url.Values, error) {
			id := ""
			if len(a) > 0 && a[0] != "" {
				id = a[0]
			} else {
				var err error
				id, err = defaultSteamID()
				if err != nil {
					return nil, err
				}
			}
			return url.Values{"steamid": {id}}, nil
		}, renderRecent},
		{"friends [STEAMID]", "Visible friend list (defaults to logged-in user)", "ISteamUser", "GetFriendList", nil, 1, cobra.MaximumNArgs(1), func(cmd *cobra.Command, a []string) (url.Values, error) {
			id := ""
			if len(a) > 0 && a[0] != "" {
				id = a[0]
			} else {
				var err error
				id, err = defaultSteamID()
				if err != nil {
					return nil, err
				}
			}
			return url.Values{"steamid": {id}, "relationship": {"friend"}}, nil
		}, renderFriends},
		{"bans [STEAMID[,STEAMID...]]", "Public player ban information (defaults to logged-in user)", "ISteamUser", "GetPlayerBans", nil, 1, cobra.MaximumNArgs(1), func(cmd *cobra.Command, a []string) (url.Values, error) {
			id := ""
			if len(a) > 0 && a[0] != "" {
				id = a[0]
			} else {
				var err error
				id, err = defaultSteamID()
				if err != nil {
					return nil, err
				}
			}
			return url.Values{"steamids": {id}}, nil
		}, renderBans},
		{"achievements [STEAMID] APPID", "Player achievements for a game (defaults to logged-in user if 1 arg; accepts app name)", "ISteamUserStats", "GetPlayerAchievements", nil, 1, cobra.RangeArgs(1, 2), func(cmd *cobra.Command, a []string) (url.Values, error) {
			rawApp := a[0]
			steamID := ""
			if len(a) == 1 {
				var err error
				steamID, err = defaultSteamID()
				if err != nil {
					return nil, err
				}
			} else {
				steamID = a[0]
				rawApp = a[1]
			}
			appID, err := o.resolveAppID(cmd.Context(), rawApp, cmd.ErrOrStderr())
			if err != nil {
				return nil, err
			}
			return url.Values{"steamid": {steamID}, "appid": {strconv.Itoa(appID)}}, nil
		}, nil},
		{"news APPID", "Recent game news (accepts app name, e.g. vrchat)", "ISteamNews", "GetNewsForApp", nil, 2, cobra.ExactArgs(1), func(cmd *cobra.Command, a []string) (url.Values, error) {
			appID, err := o.resolveAppID(cmd.Context(), a[0], cmd.ErrOrStderr())
			if err != nil {
				return nil, err
			}
			return url.Values{"appid": {strconv.Itoa(appID)}, "count": {strconv.Itoa(10)}}, nil
		}, renderNews},
		{"players APPID", "Current player count (accepts app name, e.g. cs2)", "ISteamUserStats", "GetNumberOfCurrentPlayers", nil, 1, cobra.ExactArgs(1), func(cmd *cobra.Command, a []string) (url.Values, error) {
			appID, err := o.resolveAppID(cmd.Context(), a[0], cmd.ErrOrStderr())
			if err != nil {
				return nil, err
			}
			return url.Values{"appid": {strconv.Itoa(appID)}}, nil
		}, renderPlayerCount},
	}
	for _, h := range helpers {
		root.AddCommand(&cobra.Command{Use: h.name, Aliases: h.aliases, Short: h.short, Args: h.args, RunE: func(cmd *cobra.Command, args []string) error {
			c, e := client()
			if e != nil {
				return e
			}
			params, e := h.build(cmd, args)
			if e != nil {
				return e
			}
			b, e := c.Call(cmd.Context(), h.iface, h.method, h.version, "GET", params)
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
	root.AddCommand(workshopCommand(o), appsCommand(o))
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
		o.renderTable(t)
		if o.format != "csv" {
			fmt.Fprintln(w, faint(fmt.Sprintf("%d profile(s).", len(players))))
		}
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
	o.renderTable(t)
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

// --- server info ------------------------------------------------------------

func renderServerInfo(o *options, w io.Writer, b []byte) bool {
	var res struct {
		ServerTime       int64  `json:"servertime"`
		ServerTimeString string `json:"servertimestring"`
	}
	if json.Unmarshal(b, &res) != nil || res.ServerTime == 0 {
		return false
	}
	t := o.newDetail(w)
	detailRows(t,
		kv("Server time", res.ServerTimeString),
		kv("Unix timestamp", fmt.Sprint(res.ServerTime)),
		kv("UTC time", time.Unix(res.ServerTime, 0).UTC().Format("2006-01-02 15:04:05 UTC")),
	)
	o.renderTable(t)
	return true
}

// --- resolve vanity ---------------------------------------------------------

func renderResolve(o *options, w io.Writer, b []byte) bool {
	var res struct {
		Response struct {
			SteamID string `json:"steamid"`
			Success int    `json:"success"`
			Message string `json:"message"`
		} `json:"response"`
	}
	if json.Unmarshal(b, &res) != nil {
		return false
	}
	if res.Response.Success != 1 || res.Response.SteamID == "" {
		msg := res.Response.Message
		if msg == "" {
			msg = "No match was found for that vanity URL name"
		}
		fmt.Fprintf(w, "%s %s\n", red.Sprint("Error:"), msg)
		return true
	}
	id := res.Response.SteamID
	t := o.newDetail(w)
	detailRows(t,
		kv("SteamID64", id),
		kv("Profile URL", "https://steamcommunity.com/profiles/"+id),
	)
	if conv, err := convertID(id); err == nil {
		detailRows(t,
			kv("SteamID3", conv["steamid3"]),
			kv("SteamID2", conv["steamid2"]),
		)
	}
	o.renderTable(t)
	return true
}

// --- player bans ------------------------------------------------------------

type playerBan struct {
	SteamID          string `json:"SteamId"`
	CommunityBanned  bool   `json:"CommunityBanned"`
	VACBanned        bool   `json:"VACBanned"`
	NumberOfVACBans  int    `json:"NumberOfVACBans"`
	DaysSinceLastBan int    `json:"DaysSinceLastBan"`
	NumberOfGameBans int    `json:"NumberOfGameBans"`
	EconomyBan       string `json:"EconomyBan"`
}

func renderBans(o *options, w io.Writer, b []byte) bool {
	var res struct {
		Players []playerBan `json:"players"`
	}
	if json.Unmarshal(b, &res) != nil || res.Players == nil {
		return false
	}
	if len(res.Players) == 0 {
		fmt.Fprintln(w, "No ban information returned.")
		return true
	}
	if len(res.Players) == 1 {
		p := res.Players[0]
		t := o.newDetail(w)
		detailRows(t,
			kv("SteamID64", p.SteamID),
			kv("Community ban", colorBan(p.CommunityBanned)),
			kv("VAC ban", colorBan(p.VACBanned)),
			kv("VAC bans count", fmt.Sprint(p.NumberOfVACBans)),
			kv("Game bans count", fmt.Sprint(p.NumberOfGameBans)),
			kv("Days since last ban", fmt.Sprint(p.DaysSinceLastBan)),
			kv("Economy ban", colorEconomyBan(p.EconomyBan)),
		)
		o.renderTable(t)
		return true
	}

	t := o.newTable(w)
	t.AppendHeader(table.Row{"SteamID64", "Community", "VAC", "VAC Bans", "Game Bans", "Last Ban (Days)", "Economy"})
	t.SetColumnConfigs([]table.ColumnConfig{
		{Number: 4, Align: text.AlignRight},
		{Number: 5, Align: text.AlignRight},
		{Number: 6, Align: text.AlignRight},
	})
	for _, p := range res.Players {
		t.AppendRow(table.Row{
			p.SteamID,
			colorBan(p.CommunityBanned),
			colorBan(p.VACBanned),
			p.NumberOfVACBans,
			p.NumberOfGameBans,
			p.DaysSinceLastBan,
			colorEconomyBan(p.EconomyBan),
		})
	}
	o.renderTable(t)
	if o.format != "csv" {
		fmt.Fprintln(w, faint(fmt.Sprintf("%d player(s).", len(res.Players))))
	}
	return true
}

func colorBan(banned bool) string {
	if banned {
		return red.Sprint("BANNED")
	}
	return green.Sprint("none")
}

func colorEconomyBan(status string) string {
	if strings.ToLower(status) == "none" || status == "" {
		return green.Sprint("none")
	}
	return red.Sprint(strings.ToUpper(status))
}

// --- friends list -----------------------------------------------------------

type friendItem struct {
	SteamID      string `json:"steamid"`
	Relationship string `json:"relationship"`
	FriendSince  int64  `json:"friend_since"`
}

func renderFriends(o *options, w io.Writer, b []byte) bool {
	var res struct {
		FriendsList struct {
			Friends []friendItem `json:"friends"`
		} `json:"friendslist"`
	}
	if json.Unmarshal(b, &res) != nil || res.FriendsList.Friends == nil {
		return false
	}
	friends := res.FriendsList.Friends
	if len(friends) == 0 {
		fmt.Fprintln(w, "Friend list is empty or private.")
		return true
	}
	sort.Slice(friends, func(i, j int) bool { return friends[i].FriendSince > friends[j].FriendSince })

	t := o.newTable(w)
	t.AppendHeader(table.Row{"SteamID64", "Relationship", "Friend Since"})
	for _, f := range friends {
		since := ""
		if f.FriendSince > 0 {
			since = time.Unix(f.FriendSince, 0).UTC().Format("2006-01-02 15:04 UTC")
		}
		t.AppendRow(table.Row{f.SteamID, f.Relationship, since})
	}
	o.renderTable(t)
	if o.format != "csv" {
		fmt.Fprintln(w, faint(fmt.Sprintf("%d friend(s).", len(friends))))
	}
	return true
}

// --- owned games ------------------------------------------------------------

type ownedGame struct {
	AppID                  int    `json:"appid"`
	Name                   string `json:"name"`
	Playtime2Weeks         int    `json:"playtime_2weeks"`
	PlaytimeForever        int    `json:"playtime_forever"`
	PlaytimeWindowsForever int    `json:"playtime_windows_forever"`
	PlaytimeMacForever     int    `json:"playtime_mac_forever"`
	PlaytimeLinuxForever   int    `json:"playtime_linux_forever"`
}

func renderOwned(o *options, w io.Writer, b []byte) bool {
	var res struct {
		Response struct {
			GameCount int         `json:"game_count"`
			Games     []ownedGame `json:"games"`
		} `json:"response"`
	}
	if json.Unmarshal(b, &res) != nil {
		return false
	}
	if len(res.Response.Games) == 0 {
		if strings.Contains(string(b), `"game_count"`) || strings.Contains(string(b), `"games"`) {
			fmt.Fprintln(w, "No owned games visible (profile games library may be private).")
			return true
		}
		return false
	}
	games := res.Response.Games
	sort.Slice(games, func(i, j int) bool { return games[i].PlaytimeForever > games[j].PlaytimeForever })

	t := o.newTable(w)
	t.AppendHeader(table.Row{"AppID", "Name", "Total Playtime", "Recent (2 wks)"})
	t.SetColumnConfigs([]table.ColumnConfig{
		{Number: 3, Align: text.AlignRight},
		{Number: 4, Align: text.AlignRight},
	})
	for _, g := range games {
		name := g.Name
		if name == "" {
			name = faint("(AppID " + strconv.Itoa(g.AppID) + ")")
		} else {
			name = truncate(name, 45)
		}
		recent := ""
		if g.Playtime2Weeks > 0 {
			recent = formatPlaytime(g.Playtime2Weeks)
		}
		t.AppendRow(table.Row{g.AppID, name, formatPlaytime(g.PlaytimeForever), recent})
	}
	o.renderTable(t)
	if o.format != "csv" {
		fmt.Fprintln(w, faint(fmt.Sprintf("%d owned game(s).", len(games))))
	}
	return true
}

func formatPlaytime(minutes int) string {
	if minutes <= 0 {
		return "0 hrs"
	}
	hrs := float64(minutes) / 60.0
	if hrs < 0.1 {
		return fmt.Sprintf("%d mins", minutes)
	}
	return fmt.Sprintf("%.1f hrs", hrs)
}

// --- recent games -----------------------------------------------------------

func renderRecent(o *options, w io.Writer, b []byte) bool {
	var res struct {
		Response struct {
			TotalCount int         `json:"total_count"`
			Games      []ownedGame `json:"games"`
		} `json:"response"`
	}
	if json.Unmarshal(b, &res) != nil {
		return false
	}
	if len(res.Response.Games) == 0 {
		if strings.Contains(string(b), `"total_count"`) || strings.Contains(string(b), `"games"`) {
			fmt.Fprintln(w, "No recently played games recorded in the last 2 weeks.")
			return true
		}
		return false
	}
	games := res.Response.Games
	sort.Slice(games, func(i, j int) bool { return games[i].Playtime2Weeks > games[j].Playtime2Weeks })

	t := o.newTable(w)
	t.AppendHeader(table.Row{"AppID", "Name", "Past 2 Weeks", "Total Playtime"})
	t.SetColumnConfigs([]table.ColumnConfig{
		{Number: 3, Align: text.AlignRight},
		{Number: 4, Align: text.AlignRight},
	})
	for _, g := range games {
		name := g.Name
		if name == "" {
			name = faint("(AppID " + strconv.Itoa(g.AppID) + ")")
		} else {
			name = truncate(name, 45)
		}
		t.AppendRow(table.Row{g.AppID, name, formatPlaytime(g.Playtime2Weeks), formatPlaytime(g.PlaytimeForever)})
	}
	o.renderTable(t)
	if o.format != "csv" {
		fmt.Fprintln(w, faint(fmt.Sprintf("%d recently played game(s).", len(games))))
	}
	return true
}

// --- app news ---------------------------------------------------------------

type newsItem struct {
	GID       string `json:"gid"`
	Title     string `json:"title"`
	URL       string `json:"url"`
	Author    string `json:"author"`
	FeedLabel string `json:"feedlabel"`
	Date      int64  `json:"date"`
}

func renderNews(o *options, w io.Writer, b []byte) bool {
	var res struct {
		AppNews struct {
			AppID     int        `json:"appid"`
			NewsItems []newsItem `json:"newsitems"`
			Count     int        `json:"count"`
		} `json:"appnews"`
	}
	if json.Unmarshal(b, &res) != nil || res.AppNews.NewsItems == nil {
		return false
	}
	items := res.AppNews.NewsItems
	if len(items) == 0 {
		fmt.Fprintln(w, "No news items returned.")
		return true
	}
	t := o.newTable(w)
	t.AppendHeader(table.Row{"Date", "Title", "Author", "Feed"})
	for _, item := range items {
		d := ""
		if item.Date > 0 {
			d = time.Unix(item.Date, 0).UTC().Format("2006-01-02")
		}
		t.AppendRow(table.Row{
			d,
			truncate(item.Title, 52),
			truncate(item.Author, 16),
			item.FeedLabel,
		})
	}
	o.renderTable(t)
	if o.format != "csv" {
		fmt.Fprintln(w, faint(fmt.Sprintf("%d news article(s).", len(items))))
	}
	return true
}

// --- player count -----------------------------------------------------------

func renderPlayerCount(o *options, w io.Writer, b []byte) bool {
	var res struct {
		Response struct {
			PlayerCount int `json:"player_count"`
			Result      int `json:"result"`
		} `json:"response"`
	}
	if json.Unmarshal(b, &res) != nil || res.Response.Result != 1 {
		return false
	}
	t := o.newDetail(w)
	detailRows(t,
		kv("Online players", thousands(res.Response.PlayerCount)),
	)
	o.renderTable(t)
	return true
}
