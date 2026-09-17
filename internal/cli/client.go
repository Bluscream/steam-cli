package cli

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
	"steamcli.local/steam/internal/library"
	"steamcli.local/steam/internal/steamclient"
)

func clientCommand(o *options) *cobra.Command {
	var path string
	var extraArgs []string
	var noDefaults bool

	root := &cobra.Command{
		Use:   "client",
		Short: "Drive the desktop Steam client (forwards to Valve's launcher)",
		Long: "Drive the desktop Steam client.\n\n" +
			"These subcommands forward to Valve's own Steam launcher, which owns login,\n" +
			"the overlay, and steam:// protocol handling. The client is located on PATH,\n" +
			"then in the usual install locations; --steam-path, STEAM_CLIENT_PATH, or\n" +
			"steam_client_path in your profile override that. A candidate that resolves\n" +
			"to this CLI is refused, so installing it as \"steam\" cannot cause a loop.\n\n" +
			"steam_client_args in your profile (or STEAM_CLIENT_ARGS) is prepended to\n" +
			"every launch, for options you always want such as -console. --steam-arg\n" +
			"adds to that for one run and --no-default-args skips it.",
	}
	root.PersistentFlags().StringVar(&path, "steam-path", "", "Steam client executable or launcher script")
	root.PersistentFlags().StringArrayVar(&extraArgs, "steam-arg", nil, "Extra argument for the Steam client; repeat for multiple")
	root.PersistentFlags().BoolVar(&noDefaults, "no-default-args", false, "Ignore steam_client_args from the configuration for this run")

	locator := func() (steamclient.Locator, error) {
		s, err := o.settings()
		if err != nil {
			return steamclient.Locator{}, err
		}
		p := s.SteamClientPath
		if path != "" {
			p = path
		}
		var defaults []string
		if !noDefaults {
			defaults = append(defaults, s.SteamClientArgs...)
		}
		defaults = append(defaults, extraArgs...)

		self, _ := os.Executable()
		return steamclient.Locator{Path: p, Self: self, DefaultArgs: defaults}, nil
	}

	run := func(cmd *cobra.Command, args []string) error {
		l, err := locator()
		if err != nil {
			return err
		}
		return l.Run(cmd.Context(), args, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
	}

	where := &cobra.Command{
		Use:   "path",
		Short: "Show which Steam client executable and default arguments would be used",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			l, err := locator()
			if err != nil {
				return err
			}
			p, err := l.Find()
			if err != nil {
				return err
			}
			out := map[string]any{"path": p, "default_args": l.DefaultArgs}
			if l.DefaultArgs == nil {
				out["default_args"] = []string{}
			}
			return o.emit(cmd, out, func(w io.Writer) {
				t := o.newDetail(w)
				argsStr := "(none)"
				if len(l.DefaultArgs) > 0 {
					argsStr = strings.Join(l.DefaultArgs, " ")
				}
				detailRows(t,
					kv("Steam executable", p),
					kv("Default arguments", argsStr),
				)
				o.renderTable(t)
			})
		},
	}

	launch := &cobra.Command{
		Use:     "launch -- [ARGS...]",
		Aliases: []string{"exec"},
		Short:   "Pass arguments straight to the Steam client",
		Example: "  steamcli client launch -- -silent\n  steamcli client launch -- steam://open/console",
		Args:    cobra.ArbitraryArgs,
		RunE:    run,
	}

	open := &cobra.Command{
		Use:   "open URL",
		Short: "Open a steam:// URL in the client",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := steamclient.ValidateURL(args[0]); err != nil {
				return err
			}
			return run(cmd, []string{args[0]})
		},
	}

	root.AddCommand(where, launch, open)

	// steam:// verbs that take an AppID (run, install, validate, store).
	for _, v := range []struct{ use, verb, short string }{
		{"run APPID", "run", "Launch a game"},
		{"install APPID", "install", "Start installing a game"},
		{"validate APPID", "validate", "Verify a game's local files"},
		{"store APPID", "store", "Open a game's store page"},
	} {
		root.AddCommand(&cobra.Command{
			Use:   v.use,
			Short: v.short,
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				appID, err := o.resolveAppID(cmd.Context(), args[0], cmd.ErrOrStderr())
				if err != nil {
					return err
				}
				u, err := steamclient.URL(v.verb, strconv.Itoa(appID))
				if err != nil {
					return err
				}
				return run(cmd, []string{u})
			},
		})
	}

	uninstall := newUninstallCommand(o, run)
	root.AddCommand(uninstall)

	shutdown := &cobra.Command{
		Use:   "shutdown",
		Short: "Ask the running Steam client to exit",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, []string{"-shutdown"})
		},
	}
	root.AddCommand(shutdown)

	return root
}

func newUninstallCommand(o *options, clientRun func(cmd *cobra.Command, args []string) error) *cobra.Command {
	var force, purge bool
	var roots []string
	var installDirHint string

	cmd := &cobra.Command{
		Use:     "uninstall APPID",
		Aliases: []string{"remove", "purge"},
		Short:   "Uninstall a game via Steam client or thoroughly purge all files (--force / --purge)",
		Long: "Uninstall a game.\n\n" +
			"By default, forwards to the running Steam client via steam://uninstall/<APPID>.\n" +
			"With --force / -f, it instructs the Steam client to uninstall AND physically purges all\n" +
			"related files across all libraries: installation files, appmanifest, compatdata prefixes,\n" +
			"shader cache, download staging, workshop items, and user cloud saves.\n\n" +
			"With --purge / -p, it also finds and deletes external non-Steam save files, configs,\n" +
			"and application data in OS directories (~/.config, ~/.local/share, ~/Documents, Saved Games).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			appIDInt, err := o.resolveAppID(cmd.Context(), args[0], cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			appID := strconv.Itoa(appIDInt)

			if !force && !purge {
				u, err := steamclient.URL("uninstall", appID)
				if err != nil {
					return err
				}
				if clientRun != nil {
					return clientRun(cmd, []string{u})
				}
				return nil
			}

			// Force or purge mode: notify client if available, then purge all files across libraries
			clientNotified := false
			if clientRun != nil {
				u, err := steamclient.URL("uninstall", appID)
				if err == nil {
					_ = clientRun(cmd, []string{u})
					clientNotified = true
				}
			}

			r := roots
			if len(r) == 0 {
				r = library.Defaults()
			}

			purgeRes, err := library.PurgeAppFiles(r, appID, installDirHint, purge)
			if err != nil {
				return err
			}
			purgeRes.ClientNotified = clientNotified

			return o.emit(cmd, purgeRes, func(w io.Writer) {
				if len(purgeRes.Artifacts) == 0 {
					gameLabel := appID
					if purgeRes.Name != "" {
						gameLabel = fmt.Sprintf("%s (%s)", purgeRes.Name, appID)
					}
					fmt.Fprintf(w, "%s No leftover files or directories found for %s.\n", green.Sprint("Clean:"), gameLabel)
				} else {
					title := fmt.Sprintf("Purged Files & Artifacts for AppID %s", appID)
					if purgeRes.Name != "" {
						title = fmt.Sprintf("Purged Files & Artifacts for %s (%s)", purgeRes.Name, appID)
					}
					o.heading(w, "%s", title)
					t := o.newTable(w)
					t.AppendHeader(table.Row{"Category", "Path", "Size", "Description"})
					t.SetColumnConfigs([]table.ColumnConfig{
						{Number: 3, Align: text.AlignRight},
					})
					for _, art := range purgeRes.Artifacts {
						sizeStr := ""
						if art.BytesFreed > 0 {
							sizeStr = o.sizeCell(art.BytesFreed)
						}
						t.AppendRow(table.Row{
							art.Category,
							truncate(art.Path, 65),
							sizeStr,
							art.Description,
						})
					}
					o.renderTable(t)
					if o.format != "csv" {
						fmt.Fprintf(w, "\n%s Removed %d artifact(s), reclaimed %s.\n",
							green.Sprint("Success:"), len(purgeRes.Artifacts), o.sizeCell(purgeRes.TotalBytes))
					}
				}

				if len(purgeRes.Errors) > 0 {
					for _, e := range purgeRes.Errors {
						fmt.Fprintf(cmd.ErrOrStderr(), "%s %s\n", yellow.Sprint("Warning:"), e)
					}
				}
			})
		},
	}

	cmd.Flags().BoolVarP(&force, "force", "f", false, "Force delete ALL game installation files, prefixes, shaders, downloads, and manifests")
	cmd.Flags().BoolVarP(&purge, "purge", "p", false, "Also find and delete non-Steam game configs, saves, and standalone OS directories")
	cmd.Flags().StringArrayVar(&roots, "root", nil, "Steam root directory; repeat for multiple installations")
	cmd.Flags().StringVar(&installDirHint, "dir", "", "Explicit game installation directory path if not discoverable via manifest")

	return cmd
}


