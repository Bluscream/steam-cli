package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"steamcli.local/steam/internal/status"
)

func statusCommand(o *options) *cobra.Command {
	var noCM, noCoordinator bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Steam services status and live telemetry (modeled after steamstat.us)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := o.settings()
			if err != nil {
				return err
			}
			key, _ := s.WebKey()
			mon := &status.Monitor{
				HTTP:    o.http(),
				WebKey:  key,
				BaseURL: s.WebURL,
			}

			report, err := mon.Check(cmd.Context(), !noCM, !noCoordinator)
			if err != nil {
				return err
			}

			if o.format == "raw" {
				var sb strings.Builder
				sb.WriteString(fmt.Sprintf("Steam Status Report - %s\n\n", report.Timestamp.Format("2006-01-02 15:04:05 UTC")))
				sb.WriteString("Core Services:\n")
				for _, ep := range report.Endpoints {
					sb.WriteString(fmt.Sprintf("  %-18s: [%s] %d ms (HTTP %d)\n", ep.Name, strings.ToUpper(ep.Status), ep.LatencyMS, ep.HTTPCode))
				}

				if len(report.PlayerCounts) > 0 {
					sb.WriteString("\nOnline Players:\n")
					for k, v := range report.PlayerCounts {
						sb.WriteString(fmt.Sprintf("  %-18s: %d\n", k, v))
					}
				}

				if len(report.Coordinators) > 0 {
					for _, c := range report.Coordinators {
						sb.WriteString(fmt.Sprintf("\nGame Coordinator (AppID %d):\n", c.AppID))
						for svc, st := range c.Services {
							sb.WriteString(fmt.Sprintf("  %-18s: %s\n", svc, st))
						}
					}
				}

				if len(report.ConnectionManagers) > 0 {
					sb.WriteString("\nConnection Managers:\n")
					for _, cm := range report.ConnectionManagers {
						if cm.Status == "online" {
							sb.WriteString(fmt.Sprintf("  %-25s: [%s] %d ms\n", cm.Server, strings.ToUpper(cm.Status), cm.LatencyMS))
						} else {
							sb.WriteString(fmt.Sprintf("  %-25s: [%s] %s\n", cm.Server, strings.ToUpper(cm.Status), cm.Error))
						}
					}
				}

				_, err = fmt.Fprint(cmd.OutOrStdout(), sb.String())
				return err
			}

			return o.print(cmd, report)
		},
	}

	cmd.Flags().BoolVar(&noCM, "no-cm", false, "Do not probe Steam Connection Managers")
	cmd.Flags().BoolVar(&noCoordinator, "no-coordinator", false, "Do not query Valve Game Coordinator and datacenters")
	return cmd
}
