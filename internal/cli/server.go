package cli

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
	"steamcli.local/steam/internal/gameserver"
	"steamcli.local/steam/internal/library"
	"steamcli.local/steam/internal/steamclient"
)

func serverCommand(o *options) *cobra.Command {
	root := &cobra.Command{
		Use:     "server",
		Aliases: []string{"servers"},
		Short:   "Browse, query and favourite game servers (the client's Game Servers dialog)",
		Long: "Browse, query and favourite game servers.\n\n" +
			"Mirrors the desktop client's Game Servers dialog: the internet master list,\n" +
			"direct queries against a server, LAN discovery, and the favourites and history\n" +
			"lists the client itself keeps. Favourites are read from and written back to\n" +
			"Steam's own file, so changes show up in the client.",
	}

	var roots []string
	var account string
	historyFile := func() (string, error) {
		if account != "" {
			r := roots
			if len(r) == 0 {
				r = library.Defaults()
			}
			for _, root := range r {
				p := gameserver.HistoryPath(root, account)
				if _, err := os.Stat(p); err == nil {
					return p, nil
				}
			}
			return "", fmt.Errorf("no server browser file for account %s", account)
		}
		r := roots
		if len(r) == 0 {
			r = library.Defaults()
		}
		files := gameserver.FindHistoryFiles(r)
		if len(files) == 0 {
			return "", errors.New("no Steam server browser file found; sign in to the desktop client once, or pass --root")
		}
		return files[0], nil
	}

	root.PersistentFlags().StringArrayVar(&roots, "root", nil, "Steam root directory; repeat for multiple installations")
	root.PersistentFlags().StringVar(&account, "account", "", "Steam account ID whose lists to use (default: most recently written)")

	root.AddCommand(
		serverBrowseCommand(o),
		serverInfoCommand(o),
		serverListCommand(o, gameserver.ListFavorites, historyFile),
		serverListCommand(o, gameserver.ListHistory, historyFile),
		serverAddCommand(o, historyFile),
		serverRemoveCommand(o, historyFile),
		serverLANCommand(o),
		serverConnectCommand(o),
	)
	return root
}

// --- internet master list ---------------------------------------------------

func serverBrowseCommand(o *options) *cobra.Command {
	var f gameserver.Filter
	var limit int
	var gameType []string
	var appArg string

	c := &cobra.Command{
		Use:     "browse [APPID|NAME]",
		Aliases: []string{"internet", "search"},
		Short:   "Search the internet master server list",
		Long: "Search the internet master server list.\n\n" +
			"Needs a Steam Web API key. Results come from Valve's master list, which\n" +
			"reports what servers advertise; 'server info' queries a server directly.",
		Example: "  steamcli server browse 730 --not-empty --secure\n" +
			"  steamcli server browse \"team fortress 2\" --map ctf_2fort\n" +
			"  steamcli server browse --app 440 --name-match \"2fort\" --limit 20",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			wc, _, err := o.webClient()
			if err != nil {
				return err
			}
			if len(args) > 0 {
				appArg = args[0]
			}
			if appArg != "" {
				id, err := o.resolveAppID(cmd.Context(), appArg, cmd.ErrOrStderr())
				if err != nil {
					return err
				}
				f.AppID = id
			}
			if f.AppID == 0 && f.Address == "" && f.Name == "" {
				return errors.New("give an APPID or app name, or narrow with --address or --name-match; " +
					"the master list is too large to return unfiltered")
			}
			f.GameType = gameType

			servers, err := gameserver.Browse(cmd.Context(), wc, f, limit)
			if err != nil {
				return err
			}
			return o.emit(cmd, servers, func(w io.Writer) { o.renderServers(w, servers) })
		},
	}
	fl := c.Flags()
	fl.StringVar(&appArg, "app", "", "AppID or game name to search")
	fl.StringVar(&f.Map, "map", "", "Only servers running this map")
	fl.StringVar(&f.GameDir, "gamedir", "", "Only servers for this game directory, e.g. csgo")
	fl.StringArrayVar(&gameType, "gametype", nil, "Require this server tag; repeat for multiple")
	fl.StringVar(&f.Name, "name-match", "", "Match the server name (supports * wildcards)")
	fl.StringVar(&f.Address, "address", "", "Only this IP or IP:port")
	fl.StringVar(&f.Version, "version", "", "Match the server version")
	fl.BoolVar(&f.NotEmpty, "not-empty", false, "Hide empty servers")
	fl.BoolVar(&f.NotFull, "not-full", false, "Hide full servers")
	fl.BoolVar(&f.Secure, "secure", false, "Only VAC-secured servers")
	fl.BoolVar(&f.Dedicated, "dedicated", false, "Only dedicated servers")
	fl.BoolVar(&f.LinuxOnly, "linux", false, "Only servers running Linux")
	fl.BoolVar(&f.NoPassword, "no-password", false, "Hide password-protected servers")
	fl.BoolVar(&f.WhiteListed, "whitelisted", false, "Only servers not on the Valve blocklist")
	fl.IntVar(&limit, "limit", 50, "Maximum servers to return")
	return c
}

func (o *options) renderServers(w io.Writer, servers []gameserver.Server) {
	t := o.newTable(w)
	t.AppendHeader(table.Row{"Address", "Name", "Map", "Players", "Bots", "Secure"})
	t.SetColumnConfigs([]table.ColumnConfig{{Number: 4, Align: text.AlignRight}})
	for _, s := range servers {
		t.AppendRow(table.Row{
			s.Addr, truncate(s.Name, 42), truncate(s.Map, 20),
			fmt.Sprintf("%d/%d", s.Players, s.MaxPlayers), s.Bots, colorBool(s.Secure),
		})
	}
	o.renderTable(t)
	fmt.Fprintln(w, faint(fmt.Sprintf("%d server(s).", len(servers))))
}

// --- direct query -----------------------------------------------------------

func serverInfoCommand(o *options) *cobra.Command {
	var withPlayers, withRules bool
	var timeout time.Duration

	c := &cobra.Command{
		Use:     "info ADDRESS",
		Aliases: []string{"query", "ping"},
		Short:   "Query a server directly for its status, players and rules",
		Long: "Query a server directly over A2S.\n\n" +
			"ADDRESS is host:port of the server's *query* port, which for many games is\n" +
			"not the port you connect on. No API key is needed; this talks to the server\n" +
			"rather than to Valve.",
		Example: "  steamcli server info 192.168.1.10:27015 --players\n" +
			"  steamcli server info play.example.com:27015 --rules",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			addr := withDefaultPort(args[0], 27015)
			info, err := gameserver.Query(addr, timeout)
			if err != nil {
				return err
			}

			out := map[string]any{"info": info}
			var players []gameserver.Player
			var rules map[string]string
			if withPlayers {
				if players, err = gameserver.QueryPlayers(addr, timeout); err != nil {
					return err
				}
				out["players"] = players
			}
			if withRules {
				if rules, err = gameserver.QueryRules(addr, timeout); err != nil {
					return err
				}
				out["rules"] = rules
			}

			return o.emit(cmd, out, func(w io.Writer) {
				o.renderServerInfo(w, info)
				if withPlayers {
					o.renderPlayers(w, players)
				}
				if withRules {
					o.renderRules(w, rules)
				}
			})
		},
	}
	c.Flags().BoolVarP(&withPlayers, "players", "p", false, "Also fetch the player list")
	c.Flags().BoolVarP(&withRules, "rules", "r", false, "Also fetch the server's rules (convars)")
	c.Flags().DurationVar(&timeout, "query-timeout", 3*time.Second, "Per-query timeout")
	return c
}

func (o *options) renderServerInfo(w io.Writer, i gameserver.Info) {
	t := o.newDetail(w)
	rows := []([2]string){
		kv("Name", i.Name),
		kv("Address", i.Address),
		kv("Game", i.Game),
		kv("Map", i.Map),
		kv("Players", fmt.Sprintf("%d/%d", i.Players, i.MaxPlayers)),
	}
	if i.Bots > 0 {
		rows = append(rows, kv("Bots", strconv.Itoa(i.Bots)))
	}
	rows = append(rows,
		kv("AppID", strconv.Itoa(i.AppID)),
		kv("Version", i.Version),
		kv("Type", i.ServerType),
		kv("OS", i.OS),
		kv("VAC", boolWord(i.VAC)),
		kv("Visibility", i.Visibility),
		kv("Keywords", i.Keywords),
		kv("Ping", fmt.Sprintf("%d ms", i.PingMS)),
	)
	if i.GamePort > 0 {
		rows = append(rows, kv("Game port", strconv.Itoa(i.GamePort)))
	}
	detailRows(t, rows...)
	o.renderTable(t)
}

func (o *options) renderPlayers(w io.Writer, players []gameserver.Player) {
	o.heading(w, "Players")
	t := o.newTable(w)
	t.AppendHeader(table.Row{"Name", "Score", "Time"})
	t.SetColumnConfigs([]table.ColumnConfig{{Number: 2, Align: text.AlignRight}})
	for _, p := range players {
		name := p.Name
		if strings.TrimSpace(name) == "" {
			name = faint("(connecting)")
		}
		t.AppendRow(table.Row{name, p.Score, (time.Duration(p.Duration) * time.Second).Round(time.Second)})
	}
	o.renderTable(t)
}

func (o *options) renderRules(w io.Writer, rules map[string]string) {
	o.heading(w, "Rules")
	keys := make([]string, 0, len(rules))
	for k := range rules {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	t := o.newTable(w)
	t.AppendHeader(table.Row{"Rule", "Value"})
	for _, k := range keys {
		t.AppendRow(table.Row{k, truncate(rules[k], 60)})
	}
	o.renderTable(t)
}

// --- favourites and history -------------------------------------------------

func serverListCommand(o *options, list string, file func() (string, error)) *cobra.Command {
	use, short, aliases := "favorites", "List your favourite servers", []string{"favourites", "fav", "favs"}
	if list == gameserver.ListHistory {
		use, short, aliases = "history", "List servers you have recently played on", []string{"recent"}
	}

	var refresh bool
	var timeout time.Duration
	c := &cobra.Command{
		Use:     use,
		Aliases: aliases,
		Short:   short,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := file()
			if err != nil {
				return err
			}
			entries, err := gameserver.Load(path, list)
			if err != nil {
				return err
			}
			if !refresh {
				return o.emit(cmd, entries, func(w io.Writer) { o.renderEntries(w, entries, nil) })
			}

			addrs := make([]string, len(entries))
			for i, e := range entries {
				addrs[i] = e.Address
			}
			results := gameserver.QueryMany(addrs, timeout, 24)
			return o.emit(cmd, map[string]any{"entries": entries, "status": results},
				func(w io.Writer) { o.renderEntries(w, entries, results) })
		},
	}
	c.Flags().BoolVar(&refresh, "refresh", false, "Query each server for its live status")
	c.Flags().DurationVar(&timeout, "query-timeout", 2*time.Second, "Per-query timeout when refreshing")
	return c
}

func (o *options) renderEntries(w io.Writer, entries []gameserver.Entry, status []gameserver.Result) {
	t := o.newTable(w)
	if status == nil {
		t.AppendHeader(table.Row{"#", "Address", "Name", "Last played"})
		for i, e := range entries {
			t.AppendRow(table.Row{i + 1, e.Address, truncate(e.Name, 40), e.LastPlayedTime()})
		}
	} else {
		t.AppendHeader(table.Row{"#", "Address", "Name", "Map", "Players", "Ping"})
		t.SetColumnConfigs([]table.ColumnConfig{{Number: 6, Align: text.AlignRight}})
		for i, e := range entries {
			name, mapName, players, ping := truncate(e.Name, 36), "", "", faint("—")
			if i < len(status) && status[i].Info != nil {
				in := status[i].Info
				name = truncate(in.Name, 36)
				mapName = truncate(in.Map, 18)
				players = fmt.Sprintf("%d/%d", in.Players, in.MaxPlayers)
				ping = fmt.Sprintf("%d ms", in.PingMS)
			} else if i < len(status) {
				name = faint(name)
				mapName = red.Sprint("offline")
			}
			t.AppendRow(table.Row{i + 1, e.Address, name, mapName, players, ping})
		}
	}
	o.renderTable(t)
	fmt.Fprintln(w, faint(fmt.Sprintf("%d entr%s.", len(entries),
		map[bool]string{true: "y", false: "ies"}[len(entries) == 1])))
}

func serverAddCommand(o *options, file func() (string, error)) *cobra.Command {
	var name string
	var appID int
	c := &cobra.Command{
		Use:     "add ADDRESS",
		Aliases: []string{"favorite", "star"},
		Short:   "Add a server to your favourites",
		Long: "Add a server to your favourites.\n\n" +
			"Writes Steam's own file, so the server appears in the client's Favourites\n" +
			"tab. Close Steam first: a running client holds this list in memory and will\n" +
			"overwrite the file when it exits.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			addr := withDefaultPort(args[0], 27015)
			if _, _, err := net.SplitHostPort(addr); err != nil {
				return fmt.Errorf("ADDRESS must be host:port, got %q", args[0])
			}
			path, err := file()
			if err != nil {
				return err
			}
			entries, err := gameserver.Load(path, gameserver.ListFavorites)
			if err != nil {
				return err
			}
			for _, e := range entries {
				if strings.EqualFold(e.Address, addr) {
					return fmt.Errorf("%s is already a favourite", addr)
				}
			}
			if name == "" {
				// Prefer the server's own name; fall back to the address.
				if info, err := gameserver.Query(addr, 2*time.Second); err == nil && info.Name != "" {
					name = info.Name
					if appID == 0 {
						appID = info.AppID
					}
				} else {
					name = addr
				}
			}
			entries = append(entries, gameserver.Entry{Name: name, Address: addr, AppID: appID})
			if err := gameserver.Save(path, gameserver.ListFavorites, entries); err != nil {
				return err
			}
			warnIfSteamRunning(cmd.ErrOrStderr())
			return o.print(cmd, map[string]any{"added": addr, "name": name, "favorites": len(entries)})
		},
	}
	c.Flags().StringVar(&name, "name", "", "Label to store (default: the server's own name)")
	c.Flags().IntVar(&appID, "appid", 0, "AppID to record alongside the entry")
	return c
}

func serverRemoveCommand(o *options, file func() (string, error)) *cobra.Command {
	var fromHistory bool
	c := &cobra.Command{
		Use:     "remove ADDRESS|INDEX",
		Aliases: []string{"rm", "unfavorite"},
		Short:   "Remove a server from your favourites",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			list := gameserver.ListFavorites
			if fromHistory {
				list = gameserver.ListHistory
			}
			path, err := file()
			if err != nil {
				return err
			}
			entries, err := gameserver.Load(path, list)
			if err != nil {
				return err
			}

			target := strings.TrimSpace(args[0])
			idx := -1
			if n, err := strconv.Atoi(target); err == nil {
				if n < 1 || n > len(entries) {
					return fmt.Errorf("index %d is out of range; the list has %d entries", n, len(entries))
				}
				idx = n - 1
			} else {
				addr := withDefaultPort(target, 27015)
				for i, e := range entries {
					if strings.EqualFold(e.Address, addr) {
						idx = i
						break
					}
				}
				if idx < 0 {
					return fmt.Errorf("%s is not in your %s", addr, list)
				}
			}

			removed := entries[idx]
			entries = append(entries[:idx], entries[idx+1:]...)
			if err := gameserver.Save(path, list, entries); err != nil {
				return err
			}
			warnIfSteamRunning(cmd.ErrOrStderr())
			return o.print(cmd, map[string]any{"removed": removed.Address, "name": removed.Name, "remaining": len(entries)})
		},
	}
	c.Flags().BoolVar(&fromHistory, "history", false, "Remove from the history list instead of favourites")
	return c
}

// --- LAN --------------------------------------------------------------------

func serverLANCommand(o *options) *cobra.Command {
	var ports []int
	var wait, timeout time.Duration
	c := &cobra.Command{
		Use:   "lan",
		Short: "Discover servers on the local network",
		Long: "Discover servers on the local network by broadcasting a server query.\n\n" +
			"Steam's own LAN tab uses an internal protocol this cannot reach, so a server\n" +
			"that does not answer A2S will not appear here even though the client lists it.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			addrs, err := gameserver.DiscoverLAN(ports, wait)
			if err != nil {
				return err
			}
			if len(addrs) == 0 {
				return o.emit(cmd, []gameserver.Result{}, func(w io.Writer) {
					fmt.Fprintln(w, faint("No servers answered on the local network."))
				})
			}
			results := gameserver.QueryMany(addrs, timeout, 24)
			return o.emit(cmd, results, func(w io.Writer) {
				t := o.newTable(w)
				t.AppendHeader(table.Row{"Address", "Name", "Map", "Players", "Ping"})
				t.SetColumnConfigs([]table.ColumnConfig{{Number: 5, Align: text.AlignRight}})
				for _, r := range results {
					if r.Info == nil {
						t.AppendRow(table.Row{r.Address, faint("(did not answer a full query)"), "", "", ""})
						continue
					}
					t.AppendRow(table.Row{r.Address, truncate(r.Info.Name, 40), truncate(r.Info.Map, 18),
						fmt.Sprintf("%d/%d", r.Info.Players, r.Info.MaxPlayers),
						fmt.Sprintf("%d ms", r.Info.PingMS)})
				}
				o.renderTable(t)
				fmt.Fprintln(w, faint(fmt.Sprintf("%d server(s) found.", len(results))))
			})
		},
	}
	c.Flags().IntSliceVar(&ports, "port", nil, "Query port to broadcast to; repeat for multiple (default: common Source ports)")
	c.Flags().DurationVar(&wait, "wait", 2*time.Second, "How long to listen for replies")
	c.Flags().DurationVar(&timeout, "query-timeout", 2*time.Second, "Per-server timeout when fetching details")
	return c
}

// --- connect ----------------------------------------------------------------

func serverConnectCommand(o *options) *cobra.Command {
	var password string
	c := &cobra.Command{
		Use:   "connect ADDRESS",
		Short: "Join a server through the desktop Steam client",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			addr := withDefaultPort(args[0], 27015)
			if _, _, err := net.SplitHostPort(addr); err != nil {
				return fmt.Errorf("ADDRESS must be host:port, got %q", args[0])
			}
			url := "steam://connect/" + addr
			if password != "" {
				url += "/" + password
			}
			return runSteamClient(o, cmd, []string{url})
		},
	}
	c.Flags().StringVar(&password, "password", "", "Server password (appended to the steam:// URL)")
	return c
}

// --- helpers ----------------------------------------------------------------

// withDefaultPort appends the default query port when the address has none.
func withDefaultPort(addr string, port int) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return addr
	}
	if _, _, err := net.SplitHostPort(addr); err == nil {
		return addr
	}
	return net.JoinHostPort(addr, strconv.Itoa(port))
}

func boolWord(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// warnIfSteamRunning says so when the client is up, because it keeps the
// favourites list in memory and rewrites the file when it exits.
func warnIfSteamRunning(w io.Writer) {
	if steamIsRunning() {
		fmt.Fprintf(w, "%s Steam is running. It holds this list in memory and will overwrite the file on exit; restart Steam to see the change.\n",
			yellow.Sprint("Note:"))
	}
}

// steamIsRunning reports whether a desktop Steam client is up.
func steamIsRunning() bool {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false // not Linux, or /proc unavailable: do not guess
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		b, err := os.ReadFile("/proc/" + e.Name() + "/comm")
		if err != nil {
			continue
		}
		if strings.TrimSpace(string(b)) == "steam" {
			return true
		}
	}
	return false
}

// runSteamClient hands arguments to the desktop client, reusing the locator and
// self-reference guard behind "steamcli client".
func runSteamClient(o *options, cmd *cobra.Command, args []string) error {
	self, _ := os.Executable()
	s, err := o.settings()
	if err != nil {
		return err
	}
	l := steamclient.Locator{Path: s.SteamClientPath, Self: self, DefaultArgs: s.SteamClientArgs}
	return l.Run(cmd.Context(), args, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
}
