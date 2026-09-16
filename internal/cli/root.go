package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"steamcli.local/steam/internal/asf"
	"steamcli.local/steam/internal/community"
	"steamcli.local/steam/internal/config"
	"steamcli.local/steam/internal/httpx"
	"steamcli.local/steam/internal/library"
	"steamcli.local/steam/internal/webapi"
)

var Version = "0.1.0-dev"

type options struct {
	configPath, profile, format, color string
	timeout                            time.Duration
	offline, allowHTTP, withHeader     bool

	// Settings are resolved once; see settings().
	settingsOnce   sync.Once
	cachedSettings config.Settings
	settingsErr    error
}

func New(in io.Reader, out, errOut io.Writer) *cobra.Command {
	o := &options{withHeader: true}
	r := &cobra.Command{Use: "steamcli", Short: "Private Steam toolkit: Web API, SteamCMD, ASF, and local libraries", Version: Version, SilenceUsage: true, SilenceErrors: true}
	r.SetIn(in)
	r.SetOut(out)
	r.SetErr(errOut)
	f := r.PersistentFlags()
	f.StringVar(&o.configPath, "config", "", "Configuration JSON path")
	f.StringVar(&o.profile, "profile", "", "Named configuration profile")
	f.StringVarP(&o.format, "output", "o", "auto", "Output: auto, table, json, compact, raw, short, csv")
	f.BoolVar(&o.withHeader, "with-header", true, "Include header row in tabular/CSV output")
	f.DurationVar(&o.timeout, "timeout", 30*time.Second, "Timeout per HTTP attempt (not game downloads)")
	f.BoolVar(&o.offline, "offline", false, "Disable network and external SteamCMD execution; use cached metadata")
	f.StringVar(&o.color, "color", "auto", "Colour output: auto, always, never")
	f.BoolVar(&o.allowHTTP, "allow-http", false, "Allow plaintext HTTP outside loopback on a trusted network")
	r.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if o.timeout <= 0 {
			return errors.New("--timeout must be positive")
		}
		switch o.color {
		case "auto", "always", "never":
		default:
			return errors.New("--color must be auto, always, or never")
		}
		switch o.format {
		case "parsed":
			// Renamed to "short" in 0.8.0. Kept working so documented examples
			// and anything already scripted do not break.
			o.format = "short"
			return nil
		case "auto", "table", "json", "compact", "raw", "short", "csv":
			return nil
		}
		return errors.New("--output must be auto, table, json, compact, raw, short, or csv")
	}
	accCmd := accountCommand(o)
	idlingCmd := idleCommand(o)
	r.AddCommand(
		accCmd,
		idlingCmd,
		sdkCommand(o),
		statusCommand(o),
		workshopCommand(o),
		webCommand(o),
		asfCommand(o),
		clientCommand(o),
		cmdCommand(o),
		configCommand(o),
		doctorCommand(o),
		libraryCommand(o),
		idCommand(o),
		appsCommand(o),
		searchCommand(o),
		infoCommand(o),
		serverCommand(o),
	)

	// Top-level direct shortcuts and ergonomic aliases
	whoamiCmd := &cobra.Command{
		Use:   "whoami",
		Short: "Display currently active Steam account (alias for account active)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			for _, c := range accCmd.Commands() {
				if c.Name() == "active" {
					return c.RunE(cmd, args)
				}
			}
			return nil
		},
	}

	var nickBotFlag string
	nickCmd := &cobra.Command{
		Use:     "nick NEW_NAME",
		Aliases: []string{"nickname"},
		Short:   "Quickly change Steam nickname / persona name (alias for account nick)",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			for _, c := range accCmd.Commands() {
				if c.Name() == "name" {
					if nickBotFlag != "" {
						_ = c.Flags().Set("bot", nickBotFlag)
					}
					return c.RunE(cmd, args)
				}
			}
			return nil
		},
	}
	nickCmd.Flags().StringVarP(&nickBotFlag, "bot", "b", "", "ASF bot name to target if using ASF (default: matches logged-in user, else ASF)")

	runClientCmd := &cobra.Command{
		Use:   "run APPID",
		Short: "Launch a game in the desktop Steam client",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client := clientCommand(o)
			for _, c := range client.Commands() {
				if c.Name() == "run" {
					return c.RunE(cmd, args)
				}
			}
			return nil
		},
	}

	launchClientCmd := &cobra.Command{
		Use:     "launch -- [ARGS...]",
		Aliases: []string{"exec"},
		Short:   "Pass arguments directly to the Steam client launcher",
		Args:    cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client := clientCommand(o)
			for _, c := range client.Commands() {
				if c.Name() == "launch" {
					return c.RunE(cmd, args)
				}
			}
			return nil
		},
	}

	shutdownClientCmd := &cobra.Command{
		Use:   "shutdown",
		Short: "Ask the running desktop Steam client to exit",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client := clientCommand(o)
			for _, c := range client.Commands() {
				if c.Name() == "shutdown" {
					return c.RunE(cmd, args)
				}
			}
			return nil
		},
	}

	// Promote top web queries directly to root: friends, bans, owned, recent
	friendsCmd := &cobra.Command{
		Use:   "friends [STEAMID]",
		Short: "Show visible friend list (defaults to logged-in user)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			web := webCommand(o)
			for _, c := range web.Commands() {
				if strings.HasPrefix(c.Use, "friends") {
					return c.RunE(cmd, args)
				}
			}
			return nil
		},
	}

	bansCmd := &cobra.Command{
		Use:   "bans [STEAMID[,STEAMID...]]",
		Short: "Show public player ban info (defaults to logged-in user)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			web := webCommand(o)
			for _, c := range web.Commands() {
				if strings.HasPrefix(c.Use, "bans") {
					return c.RunE(cmd, args)
				}
			}
			return nil
		},
	}

	ownedCmd := &cobra.Command{
		Use:   "owned [STEAMID]",
		Short: "Show owned games visible to API key (defaults to logged-in user)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			web := webCommand(o)
			for _, c := range web.Commands() {
				if strings.HasPrefix(c.Use, "owned") {
					return c.RunE(cmd, args)
				}
			}
			return nil
		},
	}

	recentCmd := &cobra.Command{
		Use:   "recent [STEAMID]",
		Short: "Show recently played games (defaults to logged-in user)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			web := webCommand(o)
			for _, c := range web.Commands() {
				if strings.HasPrefix(c.Use, "recent") {
					return c.RunE(cmd, args)
				}
			}
			return nil
		},
	}

	collectionCmd := &cobra.Command{
		Use:   "collection COLLECTION_ID",
		Short: "Inspect a Workshop collection and its items",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws := workshopCommand(o)
			for _, c := range ws.Commands() {
				if strings.HasPrefix(c.Use, "collection") {
					return c.RunE(cmd, args)
				}
			}
			return nil
		},
	}

	r.AddCommand(
		whoamiCmd,
		nickCmd,
		runClientCmd,
		launchClientCmd,
		shutdownClientCmd,
		friendsCmd,
		bansCmd,
		ownedCmd,
		recentCmd,
		collectionCmd,
	)
	return r
}

// settings resolves the configuration once per process.
//
// Commands such as search and info fan out across goroutines, and every one of
// them needs the settings. Loading on each call re-read the file and, because
// a profile with allow_http mutates the shared options struct, raced with the
// concurrent readers of that field. sync.Once both removes the repeated reads
// and establishes the happens-before edge the concurrent readers rely on.
func (o *options) settings() (config.Settings, error) {
	o.settingsOnce.Do(func() {
		s, e := config.Load(o.configPath, o.profile)
		if e == nil && s.AllowHTTP {
			// A profile may opt its own hosts into plaintext; the flag is
			// still able to turn it on, never off.
			o.allowHTTP = true
		}
		o.cachedSettings, o.settingsErr = s, e
	})
	return o.cachedSettings, o.settingsErr
}
func (o *options) http() *httpx.Client { return httpx.New(o.timeout, o.offline, o.allowHTTP) }

// human reports whether the caller wants a rendered view rather than data.
// "auto" is the default and means "the nicest representation available".
func (o *options) human() bool { return o.format == "auto" || o.format == "table" || o.format == "csv" }

// emit renders v through a table writer when the caller wants a human view and
// one exists, and falls back to JSON otherwise. This is what makes "auto" the
// default without every command having to know about output modes.
//
// "raw" also renders here. emit is only ever given a value this CLI assembled,
// never bytes from a server, so there is no unmodified form for raw to mean —
// and rendering keeps "--output raw status" doing what it always did.
func (o *options) emit(cmd *cobra.Command, v any, render func(io.Writer)) error {
	if render != nil && (o.human() || o.format == "raw") {
		render(cmd.OutOrStdout())
		return nil
	}
	return o.print(cmd, v)
}

func (o *options) print(cmd *cobra.Command, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	return o.printBytes(cmd, b)
}

func tw(w io.Writer) *tabwriter.Writer { return tabwriter.NewWriter(w, 0, 0, 2, ' ', 0) }
func (o *options) printBytes(cmd *cobra.Command, b []byte) error {
	// "short", table/csv or auto ("human") reduces an ASF envelope to the value behind it.
	// "short" strictly outputs only the bare value (for one bot) or values.
	if (o.format == "short" || o.human()) && len(bytes.TrimSpace(b)) > 0 {
		if lines, ok := asf.Parse(b); ok {
			w := cmd.OutOrStdout()
			for _, l := range lines {
				var e error
				if o.format == "short" || l.Bot == "" || len(lines) == 1 {
					_, e = fmt.Fprintln(w, l.Value)
				} else {
					_, e = fmt.Fprintf(w, "%s: %s\n", l.Bot, l.Value)
				}
				if e != nil {
					return e
				}
			}
			return nil
		}
	}
	if o.format != "raw" && o.format != "short" && len(bytes.TrimSpace(b)) > 0 {
		var dst bytes.Buffer
		var e error
		if o.format == "compact" {
			e = json.Compact(&dst, b)
		} else {
			e = json.Indent(&dst, b, "", "  ")
		}
		if e != nil {
			return errors.New("response is not JSON; use --output raw for XML, VDF, or text")
		}
		b = dst.Bytes()
	}
	if len(b) == 0 {
		return nil
	}
	if _, e := cmd.OutOrStdout().Write(b); e != nil {
		return e
	}
	if b[len(b)-1] != '\n' {
		_, e := fmt.Fprintln(cmd.OutOrStdout())
		return e
	}
	return nil
}
func params(values []string) (url.Values, error) {
	p := make(url.Values)
	for _, s := range values {
		k, v, ok := bytes.Cut([]byte(s), []byte("="))
		if !ok || len(k) == 0 {
			return nil, errors.New("parameters must be NAME=VALUE")
		}
		p.Add(string(k), string(v))
	}
	return p, nil
}
func bodyInput(cmd *cobra.Command, s string) ([]byte, error) {
	if s == "" {
		return nil, nil
	}
	if s == "-" {
		return readBounded(cmd.InOrStdin())
	}
	if s[0] == '@' {
		f, e := os.Open(s[1:])
		if e != nil {
			return nil, e
		}
		defer f.Close()
		return readBounded(f)
	}
	return []byte(s), nil
}
func readBounded(r io.Reader) ([]byte, error) {
	b, e := io.ReadAll(io.LimitReader(r, 8<<20+1))
	if e != nil {
		return nil, e
	}
	if len(b) > 8<<20 {
		return nil, errors.New("input exceeds 8 MiB")
	}
	return b, nil
}

func humanBytes(n int64) string {
	const u = 1024
	if n < u {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(u), 0
	for v := n / u; v >= u; v /= u {
		div *= u
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// currentUserID resolves whose profile a command acts on when no SteamID is
// given: an explicit STEAM_USER_ID, then the SteamID embedded in the Community
// session cookie, then the account the desktop client last logged in as.
//
// Three commands had grown their own copy of this, and one of them had lost the
// desktop-client fallback, so "search" resolved a different user from "web" and
// "info" on the same machine.
func (o *options) currentUserID() (string, error) {
	if s, err := o.settings(); err == nil {
		if id, err := s.SteamUserID(); err == nil && id != "" {
			return id, nil
		}
		if cookie, err := s.CommunityLoginSecure(); err == nil && cookie != "" {
			if id, err := (&community.Client{LoginSecure: cookie}).SteamID(); err == nil && id != "" {
				return id, nil
			}
		}
	}
	if id, err := library.LoggedInUser(nil); err == nil && id != "" {
		return id, nil
	}
	return "", errors.New("no SteamID given and no logged-in user could be detected; " +
		"set STEAM_USER_ID or STEAM_LOGIN_SECURE, or sign in to the desktop Steam client")
}

// webClient builds a Steam Web API client from the resolved settings. Several
// commands were assembling this by hand and had drifted apart.
func (o *options) webClient() (*webapi.Client, config.Settings, error) {
	s, err := o.settings()
	if err != nil {
		return nil, s, err
	}
	key, err := s.WebKey()
	if err != nil {
		return nil, s, err
	}
	token, err := s.AccessToken()
	if err != nil {
		return nil, s, err
	}
	return &webapi.Client{
		HTTP:        o.http(),
		BaseURL:     s.WebURL,
		Key:         key,
		CacheDir:    s.CacheDir,
		AccessToken: token,
	}, s, nil
}
