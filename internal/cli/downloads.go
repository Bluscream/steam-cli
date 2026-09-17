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
	var doStop, doStart, doFix bool

	cmd := &cobra.Command{
		Use:     "downloads [APPID]",
		Aliases: []string{"download", "dl", "queue"},
		Short:   "View or inspect active, scheduled, paused, and corrupt Steam downloads",
		Long: "Inspect Steam downloads across local libraries, manifests, and cache directories.\n\n" +
			"Run without arguments to list all active, queued, scheduled, and corrupt downloads.\n" +
			"Provide an APPID (e.g. steamcli download 227300) to inspect or repair a specific download.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				inspect := downloadInspectCommand(o)
				if doStop {
					_ = inspect.Flags().Set("stop", "true")
				}
				if doStart {
					_ = inspect.Flags().Set("start", "true")
				}
				if doFix {
					_ = inspect.Flags().Set("fix", "true")
				}
				return inspect.RunE(cmd, args)
			}
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

			// Batch actions when invoked without specific APPID
			if doStop {
				var totalCleared int
				var totalFreed int64
				affectedAppIDs := make(map[string]bool)

				for _, it := range items {
					if affectedAppIDs[it.AppID] {
						continue
					}
					affectedAppIDs[it.AppID] = true
					cnt, freed, _ := library.CleanDownloadArtifacts(r, it.AppID)
					totalCleared += cnt
					totalFreed += freed
				}

				return o.emit(cmd, map[string]any{
					"action":        "stop_all",
					"cleared_items": totalCleared,
					"freed_bytes":   totalFreed,
					"freed_human":   humanBytes(totalFreed),
					"app_count":     len(affectedAppIDs),
				}, func(w io.Writer) {
					fmt.Fprintf(w, "%s Stopped/purged staging artifacts across %d download target(s): removed %d artifact(s) (freed %s). Base game files preserved.\n",
						green.Sprint("✓"), len(affectedAppIDs), totalCleared, humanBytes(totalFreed))
				})
			}

			if doFix {
				var totalCleared int
				var totalFreed int64
				affectedAppIDs := make(map[string]bool)
				var validated []string

				client := clientCommand(o)
				var validateCmd *cobra.Command
				for _, c := range client.Commands() {
					if c.Name() == "validate" {
						validateCmd = c
						break
					}
				}

				for _, it := range items {
					if affectedAppIDs[it.AppID] {
						continue
					}
					affectedAppIDs[it.AppID] = true
					cnt, freed, _ := library.CleanDownloadArtifacts(r, it.AppID)
					totalCleared += cnt
					totalFreed += freed

					// Only trigger client validation for items that actually have corruption or errors,
					// avoiding disturbing other running downloads/validations in Steam.
					if (it.Status == library.StatusCorrupt || it.Status == library.StatusError) && validateCmd != nil {
						_ = validateCmd.RunE(cmd, []string{it.AppID})
						validated = append(validated, it.AppID)
					}
				}

				// Also clean any unmapped/empty staging directories and lingering patches in libraries
				extraCnt, extraFreed := library.CleanLingeringArtifacts(r)
				totalCleared += extraCnt
				totalFreed += extraFreed

				return o.emit(cmd, map[string]any{
					"action":        "fix_all",
					"cleared_items": totalCleared,
					"freed_bytes":   totalFreed,
					"freed_human":   humanBytes(totalFreed),
					"app_count":     len(affectedAppIDs),
					"validated":     validated,
				}, func(w io.Writer) {
					if len(validated) > 0 {
						fmt.Fprintf(w, "%s Cleared %d corrupt/staging artifact(s) (%s freed) and triggered validation for %d app(s): %s.\n",
							green.Sprint("✓"), totalCleared, humanBytes(totalFreed), len(validated), strings.Join(validated, ", "))
					} else {
						fmt.Fprintf(w, "%s Cleared %d corrupt/staging artifact(s) (%s freed) across %d download target(s).\n",
							green.Sprint("✓"), totalCleared, humanBytes(totalFreed), len(affectedAppIDs))
					}
				})
			}

			if doStart {
				affectedAppIDs := make(map[string]bool)
				var started []string

				client := clientCommand(o)
				var installCmd *cobra.Command
				for _, c := range client.Commands() {
					if c.Name() == "install" {
						installCmd = c
						break
					}
				}

				for _, it := range items {
					if affectedAppIDs[it.AppID] {
						continue
					}
					affectedAppIDs[it.AppID] = true
					if installCmd != nil {
						_ = installCmd.RunE(cmd, []string{it.AppID})
						started = append(started, it.AppID)
					}
				}

				return o.emit(cmd, map[string]any{
					"action":    "start_all",
					"app_count": len(affectedAppIDs),
					"started":   started,
				}, func(w io.Writer) {
					fmt.Fprintf(w, "%s Triggered Steam client to start/resume %d download(s).\n",
						green.Sprint("✓"), len(affectedAppIDs))
				})
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
					validatingCount := len(rep.Validating)
					schedCount := len(rep.Scheduled)
					pausedCount := len(rep.Paused)
					corruptCount := len(rep.Corrupt)

					summary := fmt.Sprintf("%d download item(s) found", len(items))
					var details []string
					if activeCount > 0 {
						details = append(details, fmt.Sprintf("%d active", activeCount))
					}
					if validatingCount > 0 {
						details = append(details, cyan.Sprint(fmt.Sprintf("%d validating", validatingCount)))
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
	cmd.Flags().StringVarP(&filterStatus, "status", "s", "", "Filter by status: downloading, validating, staging, committing, paused, queued, scheduled, corrupt, error")
	cmd.Flags().IntVarP(&watchInterval, "interval", "i", 0, "Refresh interval in seconds (0 = run once)")
	cmd.Flags().BoolVar(&doStop, "stop", false, "Purge staging directories and delta chunks across downloads without touching base game")
	cmd.Flags().BoolVar(&doStart, "start", false, "Trigger Steam client to resume/start downloading all matching apps")
	cmd.Flags().BoolVar(&doFix, "fix", false, "Clear corrupt staging artifacts and trigger Steam to re-validate cleanly across matching apps")

	cmd.AddCommand(downloadInspectCommand(o))

	return cmd
}

func downloadInspectCommand(o *options) *cobra.Command {
	var roots []string
	var doStop bool
	var doStart bool
	var doFix bool

	cmd := &cobra.Command{
		Use:     "inspect APPID",
		Aliases: []string{"info", "view", "get"},
		Short:   "Detailed inspection, control, and repair for a specific download",
		Long: "Inspect detailed download/update state, staging artifacts, and logs for an AppID.\n\n" +
			"Flags:\n" +
			"  --stop   Purge staging directories and delta chunks without touching installed game\n" +
			"  --start  Trigger Steam client to resume or initiate download/update\n" +
			"  --fix    Clear corrupt download staging artifacts and trigger Steam to re-validate cleanly",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			appID := strings.TrimSpace(args[0])
			r := roots
			if len(r) == 0 {
				r = library.Defaults()
			}

			// Handle --stop
			if doStop {
				count, freed, err := library.CleanDownloadArtifacts(r, appID)
				if err != nil {
					return fmt.Errorf("cleaning artifacts: %w", err)
				}
				return o.emit(cmd, map[string]any{
					"appid":          appID,
					"action":         "stop",
					"cleared_items":  count,
					"freed_bytes":    freed,
					"freed_human":    humanBytes(freed),
				}, func(w io.Writer) {
					fmt.Fprintf(w, "%s Removed %d staging artifact(s) for AppID %s (freed %s). Base game files preserved.\n",
						green.Sprint("✓"), count, appID, humanBytes(freed))
				})
			}

			// Handle --fix (clear artifacts + restart/revalidate)
			if doFix {
				count, freed, _ := library.CleanDownloadArtifacts(r, appID)
				// Trigger validate via client launcher
				client := clientCommand(o)
				for _, c := range client.Commands() {
					if c.Name() == "validate" {
						_ = c.RunE(cmd, []string{appID})
						break
					}
				}
				return o.emit(cmd, map[string]any{
					"appid":         appID,
					"action":        "fix",
					"cleared_items": count,
					"freed_bytes":   freed,
					"triggered":     "validate",
				}, func(w io.Writer) {
					fmt.Fprintf(w, "%s Cleared %d corrupt/staging artifact(s) (%s freed) and triggered validation for AppID %s.\n",
						green.Sprint("✓"), count, humanBytes(freed), appID)
				})
			}

			// Handle --start
			if doStart {
				client := clientCommand(o)
				for _, c := range client.Commands() {
					if c.Name() == "install" {
						_ = c.RunE(cmd, []string{appID})
						break
					}
				}
				return o.emit(cmd, map[string]any{
					"appid":     appID,
					"action":    "start",
					"triggered": "install",
				}, func(w io.Writer) {
					fmt.Fprintf(w, "%s Triggered Steam client to start/resume download for AppID %s.\n",
						green.Sprint("✓"), appID)
				})
			}

			// Default: view detailed information
			detail, err := library.GetDownloadDetail(r, appID)
			if err != nil {
				return err
			}

			return o.emit(cmd, detail, func(w io.Writer) {
				t := o.newDetail(w)
				dlStr := "-"
				if detail.Item.BytesDownloaded > 0 {
					dlStr = humanBytes(detail.Item.BytesDownloaded)
				}
				totStr := "-"
				if detail.Item.BytesTotal > 0 {
					totStr = humanBytes(detail.Item.BytesTotal)
				}
				progStr := "-"
				if detail.Item.BytesTotal > 0 {
					progStr = fmt.Sprintf("%.1f%%", detail.Item.Progress)
				}

				flagsStr := "(none)"
				if len(detail.StateFlagNames) > 0 {
					flagsStr = strings.Join(detail.StateFlagNames, ", ")
				}

				schedStr := "(none)"
				if detail.Item.ScheduledHuman != "" {
					schedStr = detail.Item.ScheduledHuman
				}

				errStr := "(none)"
				if detail.Item.ErrorDetail != "" {
					errStr = red.Sprint(detail.Item.ErrorDetail)
				}

				artifactStr := fmt.Sprintf("%d dir(s), %d file(s) (%s)",
					len(detail.StagingDirs), len(detail.StagingFiles), humanBytes(detail.TotalArtifactSz))

				detailRows(t,
					kv("AppID", detail.Item.AppID),
					kv("Name", detail.Item.Name),
					kv("Type", detail.Item.Type),
					kv("Status", formatDownloadStatus(detail.Item.Status)),
					kv("Progress", progStr),
					kv("Downloaded", dlStr),
					kv("Total", totStr),
					kv("State Flags", fmt.Sprintf("%d (%s)", detail.StateFlagsRaw, flagsStr)),
					kv("Update Error", errStr),
					kv("Scheduled", schedStr),
					kv("Library", detail.Item.Library),
					kv("Manifest", detail.ManifestPath),
					kv("Build ID", detail.BuildID),
					kv("Target Build ID", detail.TargetBuildID),
					kv("Staging Artifacts", artifactStr),
				)
				o.renderTable(t)

				if len(detail.RecentLogs) > 0 {
					o.heading(w, "Recent Log Entries (logs/content_log.txt)")
					for _, l := range detail.RecentLogs {
						fmt.Fprintf(w, "  %s\n", faint(l))
					}
				}
			})
		},
	}

	cmd.Flags().StringArrayVar(&roots, "root", nil, "Steam root directory; repeat for multiple installations")
	cmd.Flags().BoolVar(&doStop, "stop", false, "Purge staging directories and delta chunks without touching base game")
	cmd.Flags().BoolVar(&doStart, "start", false, "Trigger Steam client to resume/start downloading")
	cmd.Flags().BoolVar(&doFix, "fix", false, "Clear corrupt staging artifacts and trigger Steam to re-validate cleanly")

	return cmd
}

func formatDownloadStatus(s library.DownloadStatus) string {
	switch s {
	case library.StatusActive, library.StatusStaging, library.StatusCommitting:
		return green.Sprint(strings.ToUpper(string(s)))
	case library.StatusValidating:
		return cyan.Sprint("VALIDATING")
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

