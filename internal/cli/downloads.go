package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
	"steamcli.local/steam/internal/library"
)

func downloadsCommand(o *options) *cobra.Command {
	var roots []string
	var filterStatus string
	var watchInterval int

	cmd := &cobra.Command{
		Use:     "downloads",
		Aliases: []string{"download", "dl", "queue"},
		Short:   "View active, scheduled, paused, and corrupt Steam downloads",
		Long: "Inspect Steam downloads across local libraries, manifests, and cache directories.\n\n" +
			"Discovers active downloading/staging files, queued game updates, scheduled\n" +
			"auto-updates, paused transfers, workshop downloads, and disk/checksum error states.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			r := roots
			if len(r) == 0 {
				r = library.Defaults()
			}

			rep, err := library.ScanDownloads(r)
			if err != nil && len(rep.All) == 0 {
				return err
			}

			// Apply status filter if provided
			items := rep.All
			if filterStatus != "" {
				var filtered []library.DownloadItem
				fLower := strings.ToLower(filterStatus)
				for _, it := range items {
					if strings.ToLower(string(it.Status)) == fLower {
						filtered = append(filtered, it)
					}
				}
				items = filtered
			}

			return o.emit(cmd, rep, func(w io.Writer) {
				if len(items) == 0 {
					if filterStatus != "" {
						fmt.Fprintf(w, "No Steam downloads found matching status %q.\n", filterStatus)
					} else {
						fmt.Fprintln(w, "No active, queued, or scheduled downloads found across Steam libraries.")
					}
					return
				}

				t := o.newTable(w)
				t.AppendHeader(table.Row{"AppID", "Name", "Type", "Status", "Progress", "Downloaded", "Total", "Schedule / Note"})
				t.SetColumnConfigs([]table.ColumnConfig{
					{Number: 1, Align: text.AlignRight},
					{Number: 5, Align: text.AlignRight},
					{Number: 6, Align: text.AlignRight},
					{Number: 7, Align: text.AlignRight},
				})

				for _, it := range items {
					dlStr := "-"
					if it.BytesDownloaded > 0 {
						dlStr = humanBytes(it.BytesDownloaded)
					}
					totStr := "-"
					if it.BytesTotal > 0 {
						totStr = humanBytes(it.BytesTotal)
					}

					progStr := "-"
					if it.BytesTotal > 0 {
						progStr = fmt.Sprintf("%.1f%%", it.Progress)
					}

					extra := ""
					if it.ScheduledHuman != "" {
						extra = "Sched: " + it.ScheduledHuman
					}
					if it.ErrorDetail != "" {
						if extra != "" {
							extra += " | " + it.ErrorDetail
						} else {
							extra = it.ErrorDetail
						}
					}
					if extra == "" {
						extra = "-"
					}

					statusCell := formatDownloadStatus(it.Status)

					t.AppendRow(table.Row{
						it.AppID,
						truncate(it.Name, 32),
						it.Type,
						statusCell,
						progStr,
						dlStr,
						totStr,
						truncate(extra, 35),
					})
				}

				o.renderTable(t)

				if o.format != "csv" {
					activeCount := len(rep.Active)
					schedCount := len(rep.Scheduled)
					pausedCount := len(rep.Paused)
					corruptCount := len(rep.Corrupt)

					summary := fmt.Sprintf("%d download item(s) found", len(items))
					var details []string
					if activeCount > 0 {
						details = append(details, fmt.Sprintf("%d active", activeCount))
					}
					if corruptCount > 0 {
						details = append(details, red.Sprint(fmt.Sprintf("%d error/corrupt", corruptCount)))
					}
					if pausedCount > 0 {
						details = append(details, yellow.Sprint(fmt.Sprintf("%d paused", pausedCount)))
					}
					if schedCount > 0 {
						details = append(details, fmt.Sprintf("%d scheduled", schedCount))
					}

					if len(details) > 0 {
						summary += " (" + strings.Join(details, ", ") + ")"
					}
					fmt.Fprintf(w, "%s\n", faint(summary))

					for _, warn := range rep.Warnings {
						fmt.Fprintf(w, "%s %s\n", yellow.Sprint("Warning:"), warn)
					}
				}
			})
		},
	}

	cmd.Flags().StringArrayVar(&roots, "root", nil, "Steam root directory; repeat for multiple installations")
	cmd.Flags().StringVarP(&filterStatus, "status", "s", "", "Filter by status: downloading, staging, committing, paused, queued, scheduled, corrupt, error")
	cmd.Flags().IntVarP(&watchInterval, "interval", "i", 0, "Refresh interval in seconds (0 = run once)")

	return cmd
}

func formatDownloadStatus(s library.DownloadStatus) string {
	switch s {
	case library.StatusActive, library.StatusStaging, library.StatusCommitting:
		return green.Sprint(strings.ToUpper(string(s)))
	case library.StatusPaused:
		return yellow.Sprint("PAUSED")
	case library.StatusQueued:
		return cyan.Sprint("QUEUED")
	case library.StatusScheduled:
		return dim.Sprint("SCHEDULED")
	case library.StatusCorrupt:
		return red.Sprint("CORRUPT")
	case library.StatusError:
		return red.Sprint("ERROR")
	case library.StatusCompleted:
		return green.Sprint("DONE")
	default:
		return strings.ToUpper(string(s))
	}
}
