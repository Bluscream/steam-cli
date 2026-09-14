package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
	"steamcli.local/steam/internal/asf"
)

func asfCommand(o *options) *cobra.Command {
	var base, botsFlag string
	root := &cobra.Command{Use: "asf", Short: "Control an ArchiSteamFarm instance through its IPC API"}
	root.PersistentFlags().StringVar(&base, "url", "", "ASF base URL, including optional reverse-proxy prefix")
	root.PersistentFlags().StringVarP(&botsFlag, "bots", "b", "", "Comma-separated bot names for commands that take a selector (default: ASF, meaning all bots)")

	// selector resolves a bot selector from the positional argument, then
	// --bots, then ASF, which ArchiSteamFarm reads as every bot.
	selector := func(args []string) string {
		if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
			return args[0]
		}
		if strings.TrimSpace(botsFlag) != "" {
			return botsFlag
		}
		return "ASF"
	}
	client := func() (*asf.Client, error) {
		s, e := o.settings()
		if e != nil {
			return nil, e
		}
		p, e := s.ASFPassword()
		if e != nil {
			return nil, e
		}
		u := s.ASFURL
		if base != "" {
			u = base
		}
		return &asf.Client{HTTP: o.http(), BaseURL: u, Password: p}, nil
	}
	emit := func(cmd *cobra.Command, b []byte, e error) error {
		if len(b) > 0 {
			if pe := o.printBytes(cmd, b); pe != nil {
				return pe
			}
		}
		return e
	}
	schema := &cobra.Command{Use: "schema", Short: "OpenAPI schema from this ASF version", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		c, e := client()
		if e != nil {
			return e
		}
		b, e := c.Call(cmd.Context(), "GET", "swagger/ASF/swagger.json", nil, nil)
		return emit(cmd, b, e)
	}}
	asfStatus := &cobra.Command{Use: "status", Short: "ASF process information", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		c, e := client()
		if e != nil {
			return e
		}
		b, e := c.Call(cmd.Context(), "GET", "Api/ASF", nil, nil)
		if e != nil || !o.human() {
			return emit(cmd, b, e)
		}
		if renderASFStatus(o, cmd.OutOrStdout(), b) {
			return nil
		}
		return emit(cmd, b, e)
	}}
	root.AddCommand(schema, asfStatus)
	bots := &cobra.Command{Use: "bots [SELECTOR]", Short: "Read bot information (default: ASF = all bots)", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		p, e := asf.BotPath(selector(args), "")
		if e != nil {
			return e
		}
		c, e := client()
		if e != nil {
			return e
		}
		b, e := c.Call(cmd.Context(), "GET", p, nil, nil)
		if e != nil || !o.human() {
			return emit(cmd, b, e)
		}
		summaries, ok := asf.Bots(b)
		if !ok {
			return emit(cmd, b, e)
		}
		w := cmd.OutOrStdout()
		t := o.newTable(w)
		t.AppendHeader(table.Row{"Bot", "Connected", "Farming", "Games", "Cards", "SteamID64"})
		t.SetColumnConfigs([]table.ColumnConfig{
			{Number: 4, Align: text.AlignRight}, {Number: 5, Align: text.AlignRight},
		})
		for _, s := range summaries {
			id := s.SteamID
			if id == "0" || id == "" {
				id = faint("—")
			}
			t.AppendRow(table.Row{s.Name, colorBool(s.Connected), colorBool(s.Farming),
				s.GamesRemaining, s.CardsRemaining, id})
		}
		o.renderTable(t)
		return nil
	}}
	command := &cobra.Command{Use: "command COMMAND...", Short: "Execute an ASF command as IPC owner (can change account state)", Args: cobra.MinimumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		c, e := client()
		if e != nil {
			return e
		}
		b, e := c.Command(cmd.Context(), strings.Join(args, " "))
		return emit(cmd, b, e)
	}}
	var data string
	var values []string
	call := &cobra.Command{Use: "call METHOD PATH", Short: "Call any ASF API/plugin endpoint; supports JSON and query parameters", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		q, e := params(values)
		if e != nil {
			return e
		}
		body, e := bodyInput(cmd, data)
		if e != nil {
			return e
		}
		c, e := client()
		if e != nil {
			return e
		}
		b, e := c.Call(cmd.Context(), args[0], args[1], q, body)
		return emit(cmd, b, e)
	}}
	call.Flags().StringVarP(&data, "data", "d", "", "JSON, @file, or - for stdin")
	call.Flags().StringArrayVarP(&values, "param", "p", nil, "Query parameter NAME=VALUE (use env/file for IPC password)")
	root.AddCommand(bots, command, call)
	for _, action := range []string{"start", "stop", "pause", "resume"} {
		var permanent bool
		var resume uint16
		c := &cobra.Command{Use: action + " [SELECTOR]", Short: strings.Title(action) + " selected bots (default: --bots, else ASF)", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			p, e := asf.BotPath(selector(args), strings.Title(action))
			if e != nil {
				return e
			}
			c, e := client()
			if e != nil {
				return e
			}
			var body []byte
			if action == "pause" {
				body, _ = json.Marshal(map[string]any{"Permanent": permanent, "ResumeInSeconds": resume})
			}
			b, e := c.Call(cmd.Context(), "POST", p, nil, body)
			return emit(cmd, b, e)
		}}
		if action == "pause" {
			c.Flags().BoolVar(&permanent, "permanent", false, "Pause permanently")
			c.Flags().Uint16Var(&resume, "resume-in", 0, "Automatically resume after this many seconds")
		}
		root.AddCommand(c)
	}
	token := &cobra.Command{Use: "token [SELECTOR]", Aliases: []string{"2fa", "auth"}, Short: "Retrieve two-factor tokens (sensitive stdout)", Args: cobra.MaximumNArgs(1),
		Example: "  steamcli asf token gabeN --output short\n  steamcli asf 2fa --bots gabeN,robinwalker"}
	token.RunE = func(cmd *cobra.Command, args []string) error {
		p, e := asf.BotPath(selector(args), "TwoFactorAuthentication/Token")
		if e != nil {
			return e
		}
		c, e := client()
		if e != nil {
			return e
		}
		b, e := c.Call(cmd.Context(), "GET", p, nil, nil)
		if e != nil || !o.human() {
			return emit(cmd, b, e)
		}
		if render2FA(o, cmd.OutOrStdout(), b) {
			return nil
		}
		return emit(cmd, b, e)
	}
	root.AddCommand(token)
	return root
}

func render2FA(o *options, w io.Writer, b []byte) bool {
	lines, ok := asf.Parse(b)
	if !ok || len(lines) == 0 {
		return false
	}
	if len(lines) == 1 {
		l := lines[0]
		t := o.newDetail(w)
		if l.Bot != "" {
			detailRows(t, kv("Bot", l.Bot))
		}
		detailRows(t, kv("2FA Token", green.Sprint(l.Value)))
		o.renderTable(t)
		return true
	}

	t := o.newTable(w)
	t.AppendHeader(table.Row{"Bot", "2FA Token"})
	for _, l := range lines {
		bot := l.Bot
		if bot == "" {
			bot = faint("(default)")
		}
		t.AppendRow(table.Row{bot, green.Sprint(l.Value)})
	}
	o.renderTable(t)
	return true
}

func renderASFStatus(o *options, w io.Writer, b []byte) bool {
	var env struct {
		Result struct {
			Version      string `json:"Version"`
			ProcessID    int64  `json:"ProcessID"`
			MemoryUsage  int64  `json:"MemoryUsage"` // in KB according to ASF OpenAPI schema
			StartedAt    string `json:"ProcessStartTime"`
			BotsCount    int    `json:"BotsCount"`
			BuildVariant string `json:"BuildVariant"`
		} `json:"Result"`
	}
	if json.Unmarshal(b, &env) != nil || env.Result.Version == "" {
		return false
	}
	res := env.Result
	memMB := float64(res.MemoryUsage) / 1024.0
	t := o.newDetail(w)
	detailRows(t,
		kv("ASF version", res.Version),
		kv("Build variant", res.BuildVariant),
	)
	if res.ProcessID > 0 {
		detailRows(t, kv("Process ID", fmt.Sprint(res.ProcessID)))
	}
	detailRows(t,
		kv("Memory usage", fmt.Sprintf("%.1f MiB", memMB)),
		kv("Started at", res.StartedAt),
	)
	if res.BotsCount > 0 {
		detailRows(t, kv("Bots configured", fmt.Sprint(res.BotsCount)))
	}
	o.renderTable(t)
	return true
}
