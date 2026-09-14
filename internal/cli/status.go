package cli

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

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
			return o.emit(cmd, report, func(w io.Writer) { renderStatus(w, report) })
		},
	}

	cmd.Flags().BoolVar(&noCM, "no-cm", false, "Do not probe Steam Connection Managers")
	cmd.Flags().BoolVar(&noCoordinator, "no-coordinator", false, "Do not query Valve Game Coordinator and datacenters")
	cmd.Flags().IntVar(&cmLimit, "cm-limit", 5, "Number of connection managers to probe")
	cmd.Flags().StringArrayVar(&appIDs, "app", nil, "Report the player count for this AppID instead of the default titles; repeat for multiple")
	return cmd
}

func renderStatus(out io.Writer, report status.Report) {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Steam Status Report - %s\n\n", report.Timestamp.Format("2006-01-02 15:04:05 UTC"))

	sb.WriteString("Core Services:\n")
	for _, ep := range report.Endpoints {
		if ep.Error != "" {
			fmt.Fprintf(&sb, "  %-18s: [%s] %s\n", ep.Name, strings.ToUpper(ep.Status), ep.Error)
			continue
		}
		fmt.Fprintf(&sb, "  %-18s: [%s] %d ms (HTTP %d)\n",
			ep.Name, strings.ToUpper(ep.Status), ep.LatencyMS, ep.HTTPCode)
	}

	if len(report.PlayerCounts) > 0 {
		sb.WriteString("\nOnline Players:\n")
		for _, pc := range report.PlayerCounts {
			if pc.Error != "" {
				fmt.Fprintf(&sb, "  %-20s: unavailable (%s)\n", pc.Name, pc.Error)
				continue
			}
			fmt.Fprintf(&sb, "  %-20s: %s\n", pc.Name, thousands(pc.Count))
		}
	}

	for _, c := range report.Coordinators {
		fmt.Fprintf(&sb, "\nGame Coordinator - %s (AppID %d):\n", c.Name, c.AppID)
		if c.Error != "" {
			fmt.Fprintf(&sb, "  unavailable (%s)\n", c.Error)
			continue
		}
		for _, svc := range sortedKeys(c.Services) {
			fmt.Fprintf(&sb, "  %-18s: %s\n", svc, c.Services[svc])
		}
		if len(c.Matchmaking) > 0 {
			sb.WriteString("  Matchmaking:\n")
			for _, k := range sortedKeys(c.Matchmaking) {
				fmt.Fprintf(&sb, "    %-16s: %v\n", k, c.Matchmaking[k])
			}
		}
		if len(c.Datacenters) > 0 {
			sb.WriteString("  Datacenters:\n")
			for _, dc := range sortedKeys(c.Datacenters) {
				fmt.Fprintf(&sb, "    %-16s: %s\n", dc, formatDatacenter(c.Datacenters[dc]))
			}
		}
	}

	if len(report.ConnectionManagers) > 0 {
		sb.WriteString("\nConnection Managers:\n")
		for _, cm := range report.ConnectionManagers {
			if cm.Status == "online" {
				fmt.Fprintf(&sb, "  %-25s: [ONLINE] %d ms\n", cm.Server, cm.LatencyMS)
			} else {
				fmt.Fprintf(&sb, "  %-25s: [%s] %s\n", cm.Server, strings.ToUpper(cm.Status), cm.Error)
			}
		}
	}

	for _, w := range report.Warnings {
		fmt.Fprintf(&sb, "\nNote: %s\n", w)
	}

	fmt.Fprint(out, sb.String())
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
