package cli

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
	"steamcli.local/steam/internal/status"
)

func statusCommand(o *options) *cobra.Command {
	var noCM, noCoordinator bool
	var cmLimit int
	var appIDs []string

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Steam services status and live telemetry (modeled after steamstat.us)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := o.settings()
			if err != nil {
				return err
			}
			key, err := s.WebKey()
			if err != nil {
				return err
			}

			mon := &status.Monitor{
				HTTP:         o.http(),
				WebKey:       key,
				WebAPIURL:    s.WebURL,
				CommunityURL: s.CommunityURL,
				CMLimit:      cmLimit,
			}
			for _, raw := range appIDs {
				id, err := strconv.Atoi(strings.TrimSpace(raw))
				if err != nil || id <= 0 {
					return fmt.Errorf("--app expects a positive AppID, got %q", raw)
				}
				mon.Apps = append(mon.Apps, status.TrackedApp{Name: "AppID " + raw, AppID: id})
			}

			report, err := mon.Check(cmd.Context(), !noCM, !noCoordinator)
			if err != nil {
				return err
			}
			return o.emit(cmd, report, func(w io.Writer) { o.renderStatus(w, report) })
		},
	}

	cmd.Flags().BoolVar(&noCM, "no-cm", false, "Do not probe Steam Connection Managers")
	cmd.Flags().BoolVar(&noCoordinator, "no-coordinator", false, "Do not query Valve Game Coordinator and datacenters")
	cmd.Flags().IntVar(&cmLimit, "cm-limit", 5, "Number of connection managers to probe")
	cmd.Flags().StringArrayVar(&appIDs, "app", nil, "Report the player count for this AppID instead of the default titles; repeat for multiple")
	return cmd
}

func (o *options) renderStatus(out io.Writer, report status.Report) {
	if o.format != "csv" {
		fmt.Fprintf(out, "%s\n", faint("Steam status — "+report.Timestamp.Format("2006-01-02 15:04:05 UTC")))
	}

	o.heading(out, "Core Services")
	t := o.newTable(out)
	t.AppendHeader(table.Row{"Service", "State", "Latency", "HTTP"})
	t.SetColumnConfigs([]table.ColumnConfig{{Number: 3, Align: text.AlignRight}})
	for _, ep := range report.Endpoints {
		if ep.Error != "" {
			t.AppendRow(table.Row{ep.Name, colorStatus(ep.Status), "", ep.Error})
			continue
		}
		t.AppendRow(table.Row{ep.Name, colorStatus(ep.Status),
			fmt.Sprintf("%d ms", ep.LatencyMS), ep.HTTPCode})
	}
	o.renderTable(t)

	if len(report.PlayerCounts) > 0 {
		o.heading(out, "Online Players")
		pt := o.newTable(out)
		pt.AppendHeader(table.Row{"Title", "AppID", "Players"})
		pt.SetColumnConfigs([]table.ColumnConfig{
			{Number: 3, Align: text.AlignRight, Transformer: o.numberT()},
		})
		for _, pc := range report.PlayerCounts {
			if pc.Error != "" {
				pt.AppendRow(table.Row{pc.Name, pc.AppID, red.Sprint("unavailable")})
				continue
			}
			pt.AppendRow(table.Row{pc.Name, pc.AppID, pc.Count})
		}
		o.renderTable(pt)
	}

	for _, c := range report.Coordinators {
		o.heading(out, "Game Coordinator — %s (AppID %d)", c.Name, c.AppID)
		ct := o.newTable(out)
		ct.AppendHeader(table.Row{"Service", "State"})
		if c.Error != "" {
			ct.AppendRow(table.Row{"coordinator", red.Sprint(c.Error)})
			o.renderTable(ct)
			continue
		}
		for _, svc := range sortedKeys(c.Services) {
			ct.AppendRow(table.Row{svc, colorStatus(c.Services[svc])})
		}
		for _, k := range sortedKeys(c.Matchmaking) {
			ct.AppendRow(table.Row{faint("mm: " + k), fmt.Sprint(c.Matchmaking[k])})
		}
		o.renderTable(ct)

		if len(c.Datacenters) > 0 {
			o.heading(out, "Datacenters")
			dt := o.newTable(out)
			dt.AppendHeader(table.Row{"Region", "Capacity", "Load"})
			for _, name := range sortedKeys(c.Datacenters) {
				cap, load := datacenterFields(c.Datacenters[name])
				dt.AppendRow(table.Row{name, colorCapacity(cap), colorStatus(load)})
			}
			o.renderTable(dt)
		}
	}

	if len(report.ConnectionManagers) > 0 {
		o.heading(out, "Connection Managers")
		mt := o.newTable(out)
		mt.AppendHeader(table.Row{"Server", "State", "Latency"})
		mt.SetColumnConfigs([]table.ColumnConfig{{Number: 3, Align: text.AlignRight}})
		for _, cm := range report.ConnectionManagers {
			if cm.Status == "online" {
				mt.AppendRow(table.Row{cm.Server, colorStatus(cm.Status), fmt.Sprintf("%d ms", cm.LatencyMS)})
			} else {
				mt.AppendRow(table.Row{cm.Server, colorStatus(cm.Status), cm.Error})
			}
		}
		o.renderTable(mt)
	}

	if o.format != "csv" {
		for _, w := range report.Warnings {
			fmt.Fprintf(out, "%s %s\n", yellow.Sprint("Note:"), w)
		}
	}
}

// datacenterFields pulls capacity and load out of Valve's per-region object.
func datacenterFields(v any) (capacity, load string) {
	obj, ok := v.(map[string]any)
	if !ok {
		return fmt.Sprint(v), ""
	}
	if c, ok := obj["capacity"]; ok {
		capacity = fmt.Sprint(c)
	}
	if l, ok := obj["load"]; ok {
		load = fmt.Sprint(l)
	}
	if capacity == "" && load == "" {
		return formatDatacenter(v), ""
	}
	return capacity, load
}

// colorCapacity reads inversely to load: "full" capacity is healthy.
func colorCapacity(c string) string {
	switch strings.ToLower(c) {
	case "full":
		return green.Sprint("FULL")
	case "medium":
		return yellow.Sprint("MEDIUM")
	case "empty", "offline":
		return red.Sprint(strings.ToUpper(c))
	}
	return strings.ToUpper(c)
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// formatDatacenter renders Valve's per-region capacity/load object compactly.
func formatDatacenter(v any) string {
	obj, ok := v.(map[string]any)
	if !ok {
		return fmt.Sprint(v)
	}
	var parts []string
	for _, k := range sortedKeys(obj) {
		parts = append(parts, fmt.Sprintf("%s=%v", k, obj[k]))
	}
	return strings.Join(parts, " ")
}

func thousands(n int) string {
	s := strconv.Itoa(n)
	if n < 0 {
		return s
	}
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	return string(out)
}
