package cli

import (
	"fmt"
	"io"
	"path/filepath"
	"steamcli.local/steam/internal/steamvdf"
	"strconv"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
	"steamcli.local/steam/internal/library"
)

// rootsFlag wires the --root flag that every local-config command shares.
func rootsFlag(c *cobra.Command, roots *[]string) {
	c.Flags().StringArrayVar(roots, "root", nil, "Steam root directory; repeat for multiple installations")
}

var steamRunning = library.SteamRunning

// confirmWrite gates a change to a file the desktop client owns.
//
// Steam keeps config.vdf, localconfig.vdf and the app manifests in memory while
// it runs and rewrites them on exit, so an edit made underneath a running client
// is silently lost. Refusing by default is the only way this reliably works.
func confirmWrite(cmd *cobra.Command, force bool, what string) error {
	if !steamRunning() {
		return nil
	}
	if force {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s Steam is running; it may overwrite %s when it exits.\n",
			yellow.Sprint("Warning:"), what)
		return nil
	}
	return fmt.Errorf("Steam is running (or process inspection is unavailable); cannot safely edit %s.\n"+
		"Close the desktop client first, or pass --force to write anyway", what)
}

// --- compat -----------------------------------------------------------------

func compatCommand(o *options) *cobra.Command {
	c := &cobra.Command{
		Use:     "compat",
		Aliases: []string{"proton", "compat-tool"},
		Short:   "Inspect and change the compatibility tool games run under",
	}
	c.AddCommand(compatListCommand(o), compatGetCommand(o), compatSetCommand(o))
	return c
}

func compatListCommand(o *options) *cobra.Command {
	var roots []string
	c := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls", "tools"},
		Short:   "List the compatibility tools this installation can use",
		Long: "List the compatibility tools this installation can use.\n\n" +
			"Tools in compatibilitytools.d carry a manifest naming themselves. Valve's\n" +
			"Proton builds install as ordinary apps, and their internal names live in a\n" +
			"binary cache this program does not read, so a Proton build newer than this\n" +
			"release may be listed without a name and marked unverified.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			tools := library.AvailableCompatTools(roots)
			global := library.GlobalCompatTool(roots)
			return o.emit(cmd, map[string]any{"tools": tools, "global": global}, func(w io.Writer) {
				t := o.newTable(w)
				t.AppendHeader(table.Row{"Name", "Display Name", "Source", "Apps", "Path"})
				t.SetColumnConfigs([]table.ColumnConfig{{Number: 4, Align: text.AlignRight}})
				for _, tool := range tools {
					name := tool.Name
					if name == "" {
						name = faint("(name unknown)")
					} else if !tool.Verified {
						name += faint(" ?")
					}
					apps := ""
					if tool.InUse > 0 {
						apps = strconv.Itoa(tool.InUse)
					}
					t.AppendRow(table.Row{name, truncate(tool.DisplayName, 30), tool.Source, apps, truncate(tool.Path, 50)})
				}
				o.renderTable(t)
				if o.format == "csv" {
					return
				}
				if global != "" {
					fmt.Fprintf(w, "%s\n", faint("Default for all titles: "+global))
				} else {
					fmt.Fprintf(w, "%s\n", faint("No global default; each title uses its own setting."))
				}
				for _, tool := range tools {
					if !tool.Verified {
						fmt.Fprintf(w, "%s %s was not verified from an installed tool manifest; check it in the client.\n",
							yellow.Sprint("Note:"), tool.DisplayName)
					}
				}
			})
		},
	}
	rootsFlag(c, &roots)
	return c
}

func compatGetCommand(o *options) *cobra.Command {
	var roots []string
	c := &cobra.Command{
		Use:   "get APPID",
		Short: "Show which compatibility tool an app is set to use",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			appID := args[0]
			tool := library.ScanCompatTools(roots)[appID]
			global := library.GlobalCompatTool(roots)
			effective, source := tool, "app"
			if effective == "" {
				effective, source = global, "global default"
			}
			if effective == "" {
				source = "none (Steam decides)"
			}
			return o.emit(cmd, map[string]any{"appid": appID, "tool": tool, "global": global, "effective": effective, "source": source}, func(w io.Writer) {
				t := o.newDetail(w)
				detailRows(t,
					kv("AppID", appID),
					kv("Tool for this app", tool),
					kv("Global default", global),
					kv("Effective", effective),
					kv("From", source),
				)
				o.renderTable(t)
			})
		},
	}
	rootsFlag(c, &roots)
	return c
}

func compatSetCommand(o *options) *cobra.Command {
	var roots []string
	var force, global bool
	c := &cobra.Command{
		Use:   "set APPID [TOOL]",
		Short: "Set or clear the compatibility tool for an app",
		Long: "Set or clear the compatibility tool for an app.\n\n" +
			"Omit TOOL to clear the mapping and let Steam decide. Use --global to change\n" +
			"the default applied to every title without its own setting.\n\n" +
			"Steam must not be running: it holds config.vdf in memory and rewrites it on exit.",
		Example: "  steamcli library compat set 438100 proton_experimental\n" +
			"  steamcli library compat set 438100\n" +
			"  steamcli library compat set --global \"Proton-GE Latest\"",
		Args: cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			appID, tool := "0", ""
			if !global && len(args) == 0 {
				return fmt.Errorf("APPID is required unless --global is used")
			}
			if global {
				if len(args) > 0 {
					tool = args[0]
				}
				if len(args) == 2 {
					return fmt.Errorf("--global takes only a tool name")
				}
			} else {
				appID = args[0]
				if len(args) == 2 {
					tool = args[1]
				}
			}
			if err := confirmWrite(cmd, force, "config.vdf"); err != nil {
				return err
			}
			if tool != "" {
				known := false
				for _, t := range library.AvailableCompatTools(roots) {
					if t.Name == tool {
						known = true
						break
					}
				}
				if !known {
					// Not fatal: a tool can be installed in a library this scan
					// did not cover, or added after this call.
					fmt.Fprintf(cmd.ErrOrStderr(), "%s %q was not found among the installed tools; run \"steamcli library compat list\" to check the spelling.\n",
						yellow.Sprint("Warning:"), tool)
				}
			}
			path, err := library.SetCompatTool(roots, appID, tool)
			if err != nil {
				return err
			}
			action := "cleared"
			if tool != "" {
				action = "set to " + tool
			}
			if o.human() {
				fmt.Fprintf(cmd.OutOrStdout(), "App %s %s in %s\n", appID, action, path)
				fmt.Fprintf(cmd.OutOrStdout(), "%s\n", faint("Start Steam for the change to take effect."))
				return nil
			}
			return o.print(cmd, map[string]any{"appid": appID, "tool": tool, "file": path})
		},
	}
	rootsFlag(c, &roots)
	c.Flags().BoolVar(&global, "global", false, "Change the default for every title instead of one app")
	c.Flags().BoolVar(&force, "force", false, "Write even though Steam is running")
	return c
}

// --- launch options ---------------------------------------------------------

func launchCommand(o *options) *cobra.Command {
	c := &cobra.Command{
		Use:     "launch",
		Aliases: []string{"launch-options", "opts"},
		Short:   "Inspect and change per-game launch options",
	}
	c.AddCommand(launchGetCommand(o), launchSetCommand(o))
	return c
}

func launchGetCommand(o *options) *cobra.Command {
	var roots []string
	var showSecrets bool
	var account string
	c := &cobra.Command{
		Use:     "get APPID",
		Aliases: []string{"show"},
		Short:   "Show the launch options set for an app",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := library.LoadAppConfigForAccount(roots, account, args[0])
			if err != nil {
				return err
			}
			opts := cfg.LaunchOptions
			risky := library.LaunchOptionsLookRisky(opts)
			if !showSecrets {
				opts = library.RedactLaunchOptions(opts)
			}
			return o.emit(cmd, map[string]any{"appid": cfg.AppID, "name": cfg.Name, "launch_options": opts, "file": cfg.LocalConfig}, func(w io.Writer) {
				t := o.newDetail(w)
				detailRows(t,
					kv("AppID", cfg.AppID),
					kv("Name", cfg.Name),
					kv("Launch options", opts),
					kv("Stored in", cfg.LocalConfig),
				)
				o.renderTable(t)
				if opts == "" {
					fmt.Fprintf(w, "%s\n", faint("No launch options set."))
				}
				if risky && !showSecrets && o.format != "csv" {
					fmt.Fprintf(w, "%s\n", faint("A value that looks like a credential was hidden; pass --show-secrets to print it."))
				}
			})
		},
	}
	rootsFlag(c, &roots)
	c.Flags().StringVar(&account, "account", "", "Steam account ID whose launch options to read")
	c.Flags().BoolVar(&showSecrets, "show-secrets", false, "Print credential-like values instead of redacting them")
	return c
}

func launchSetCommand(o *options) *cobra.Command {
	var roots []string
	var account string
	var force bool
	c := &cobra.Command{
		Use:   "set APPID [OPTIONS]",
		Short: "Set or clear the launch options for an app",
		Long: "Set or clear the launch options for an app.\n\n" +
			"Omit OPTIONS to clear them. Pass the whole option string as a single\n" +
			"argument, and use -- to stop flag parsing when it starts with a dash.\n\n" +
			"Steam must not be running: it holds localconfig.vdf in memory and rewrites\n" +
			"it on exit.",
		Example: "  steamcli library launch set 438100 -- \"-console -novid %command%\"\n" +
			"  steamcli library launch set 438100",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			appID, opts := args[0], ""
			if len(args) == 2 {
				opts = args[1]
			}
			if err := confirmWrite(cmd, force, "localconfig.vdf"); err != nil {
				return err
			}
			path, err := library.SetLaunchOptions(roots, account, appID, opts)
			if err != nil {
				return err
			}
			if o.human() {
				if opts == "" {
					fmt.Fprintf(cmd.OutOrStdout(), "Launch options cleared for %s in %s\n", appID, path)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "Launch options for %s set to %s\n", appID, library.RedactLaunchOptions(opts))
					fmt.Fprintf(cmd.OutOrStdout(), "%s\n", faint("Stored in "+path))
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s\n", faint("Start Steam for the change to take effect."))
				return nil
			}
			return o.print(cmd, map[string]any{"appid": appID, "launch_options": library.RedactLaunchOptions(opts), "file": path})
		},
	}
	rootsFlag(c, &roots)
	c.Flags().StringVar(&account, "account", "", "Steam account ID to write for, when several have signed in")
	c.Flags().BoolVar(&force, "force", false, "Write even though Steam is running")
	return c
}

// --- DLC --------------------------------------------------------------------

func dlcCommand(o *options) *cobra.Command {
	c := &cobra.Command{
		Use:   "dlc",
		Short: "List and toggle the installed DLC of a game",
	}
	c.AddCommand(dlcListCommand(o), dlcToggleCommand(o, true), dlcToggleCommand(o, false))
	return c
}

func dlcListCommand(o *options) *cobra.Command {
	var roots []string
	c := &cobra.Command{
		Use:     "list APPID",
		Aliases: []string{"ls"},
		Short:   "List locally recorded DLC depots and disabled selections",
		Long: "List locally recorded DLC depots and disabled selections for a game.\n\n" +
			"This is local metadata, not a complete list of owned DLC.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := library.LoadManifestConfig(roots, args[0])
			if err != nil {
				return err
			}
			return o.emit(cmd, map[string]any{"appid": cfg.AppID, "name": cfg.Name, "dlc": cfg.DLC}, func(w io.Writer) {
				if len(cfg.DLC) == 0 {
					fmt.Fprintf(w, "%s\n", faint(fmt.Sprintf("%s has no locally recorded DLC.", nameOr(cfg))))
					return
				}
				o.heading(w, "DLC for %s", nameOr(cfg))
				t := o.newTable(w)
				t.AppendHeader(table.Row{"AppID", "Depot", "Size", "Enabled"})
				t.SetColumnConfigs([]table.ColumnConfig{{Number: 3, Align: text.AlignRight}})
				var total int64
				for _, d := range cfg.DLC {
					total += d.Size
					t.AppendRow(table.Row{d.AppID, d.Depot, o.sizeCell(d.Size), colorBool(d.Enabled)})
				}
				o.renderTable(t)
				if o.format != "csv" {
					fmt.Fprintf(w, "%s\n", faint(fmt.Sprintf("%d DLC, %s on disk.", len(cfg.DLC), humanBytes(total))))
				}
			})
		},
	}
	rootsFlag(c, &roots)
	return c
}

func dlcToggleCommand(o *options, enable bool) *cobra.Command {
	var roots []string
	var force bool
	verb, past := "enable", "enabled"
	if !enable {
		verb, past = "disable", "disabled"
	}
	c := &cobra.Command{
		Use:   verb + " APPID DLC_APPID",
		Short: strings.ToUpper(verb[:1]) + verb[1:] + " one DLC of an installed game",
		Long: strings.ToUpper(verb[:1]) + verb[1:] + " one DLC of an installed game.\n\n" +
			"This writes the manifest's DisabledDLC list, which is where the client records\n" +
			"an unticked box. Steam re-reads the manifest when it starts, so it must not be\n" +
			"running. This records a selection; Steam controls downloads and the game controls DLC use.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := confirmWrite(cmd, force, "the app manifest"); err != nil {
				return err
			}
			manifest, err := library.SetDLCEnabled(roots, args[0], args[1], enable)
			if err != nil {
				return err
			}
			if o.human() {
				fmt.Fprintf(cmd.OutOrStdout(), "DLC %s %s for app %s\n", args[1], past, args[0])
				fmt.Fprintf(cmd.OutOrStdout(), "%s\n", faint("Written to "+manifest+"; start Steam for the change to take effect."))
				return nil
			}
			return o.print(cmd, map[string]any{"appid": args[0], "dlc": args[1], "enabled": enable, "file": manifest})
		},
	}
	rootsFlag(c, &roots)
	c.Flags().BoolVar(&force, "force", false, "Write even though Steam is running")
	return c
}

// --- branch -----------------------------------------------------------------

func branchCommand(o *options) *cobra.Command {
	c := &cobra.Command{
		Use:     "branch",
		Aliases: []string{"beta"},
		Short:   "Inspect and change the beta branch an installed game is on",
	}
	c.AddCommand(branchGetCommand(o), branchSetCommand(o), branchDownloadCommand(o))
	return c
}

func branchGetCommand(o *options) *cobra.Command {
	var roots []string
	c := &cobra.Command{
		Use:     "get APPID",
		Aliases: []string{"show"},
		Short:   "Show the beta branch an installed game is on",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := library.LoadManifestConfig(roots, args[0])
			if err != nil {
				return err
			}
			return o.emit(cmd, map[string]any{"appid": cfg.AppID, "name": cfg.Name, "branch": cfg.Branch}, func(w io.Writer) {
				t := o.newDetail(w)
				detailRows(t,
					kv("AppID", cfg.AppID),
					kv("Name", cfg.Name),
					kv("Requested branch", branchLabel(cfg.Branch.Requested)),
					kv("Installed branch", branchLabel(cfg.Branch.Mounted)),
					kv("Manifest", cfg.Manifest),
				)
				o.renderTable(t)
				if cfg.Branch.Pending() {
					fmt.Fprintf(w, "%s The requested branch is not the one installed; run an update to fetch it.\n", yellow.Sprint("Note:"))
				}
			})
		},
	}
	rootsFlag(c, &roots)
	return c
}

func branchLabel(s string) string {
	if s == "" {
		return "public"
	}
	return s
}

func branchSetCommand(o *options) *cobra.Command {
	var roots []string
	var force bool
	c := &cobra.Command{
		Use:   "set APPID [BRANCH]",
		Short: "Record a beta branch for an installed game",
		Long: "Record a beta branch for an installed game. Omit BRANCH to return to public.\n\n" +
			"This only records the request. The manifest's BetaKey is Steam's note of what\n" +
			"it was told to fetch, not an instruction that moves files, so the game stays on\n" +
			"its current build until it is updated. To download the branch directly, use\n" +
			"SteamCMD:\n\n" +
			"  steamcli cmd run +login USER +app_update APPID -beta BRANCH +quit\n\n" +
			"For a managed download into the existing game directory, use:\n\n" +
			"  steamcli library branch download APPID BRANCH --user USER\n\n" +
			"A branch needing a password can only be set in the client or through SteamCMD's\n" +
			"-betapassword, which this command does not write.",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			branch := ""
			if len(args) == 2 {
				branch = args[1]
			}
			if branch == "public" {
				branch = ""
			}
			if err := confirmWrite(cmd, force, "the app manifest"); err != nil {
				return err
			}
			manifest, err := library.SetBranch(roots, args[0], branch)
			if err != nil {
				return err
			}
			if o.human() {
				fmt.Fprintf(cmd.OutOrStdout(), "App %s set to branch %s\n", args[0], branchLabel(branch))
				fmt.Fprintf(cmd.OutOrStdout(), "%s\n", faint("Written to "+manifest))
				fmt.Fprintf(cmd.OutOrStdout(), "%s Nothing has been downloaded. Start Steam, or run SteamCMD's app_update, to fetch the branch.\n", yellow.Sprint("Note:"))
				return nil
			}
			return o.print(cmd, map[string]any{"appid": args[0], "branch": branch, "file": manifest, "downloaded": false})
		},
	}
	rootsFlag(c, &roots)
	c.Flags().BoolVar(&force, "force", false, "Write even though Steam is running")
	return c
}

// --- app ---------------------------------------------------------------------

func nameOr(cfg library.AppConfig) string {
	if cfg.Name != "" {
		return cfg.Name
	}
	return cfg.AppID
}

// appConfigCommand shows everything the other subcommands can change, in one
// view, which is the closest equivalent to the client's per-game properties
// dialog.
func appConfigCommand(o *options) *cobra.Command {
	var roots []string
	var showSecrets bool
	var account string
	c := &cobra.Command{
		Use:     "app APPID",
		Aliases: []string{"properties", "props"},
		Short:   "Show the local settings of one installed game",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := library.LoadAppConfigForAccount(roots, account, args[0])
			if err != nil {
				return err
			}
			tool := library.ScanCompatTools(roots)[cfg.AppID]
			global := library.GlobalCompatTool(roots)
			opts := cfg.LaunchOptions
			if !showSecrets {
				opts = library.RedactLaunchOptions(opts)
				cfg.LaunchOptions = opts
			}
			effective := tool
			if effective == "" {
				effective = global
			}
			return o.emit(cmd, map[string]any{"app": cfg, "compat_tool": tool, "compat_tool_effective": effective}, func(w io.Writer) {
				t := o.newDetail(w)
				detailRows(t,
					kv("AppID", cfg.AppID),
					kv("Name", cfg.Name),
					kv("Branch", branchLabel(cfg.Branch.Requested)),
					kv("Installed branch", branchLabel(cfg.Branch.Mounted)),
					kv("Compatibility tool", effective),
					kv("Launch options", opts),
					kv("Installed DLC", dlcSummary(cfg.DLC)),
					kv("Manifest", cfg.Manifest),
				)
				o.renderTable(t)
				if tool == "" && global != "" && o.format != "csv" {
					fmt.Fprintf(w, "%s\n", faint("Compatibility tool comes from the global default."))
				}
				if cfg.Branch.Pending() {
					fmt.Fprintf(w, "%s A branch change is recorded but not downloaded.\n", yellow.Sprint("Note:"))
				}
			})
		},
	}
	rootsFlag(c, &roots)
	c.Flags().StringVar(&account, "account", "", "Steam account ID whose launch options to read")
	c.Flags().BoolVar(&showSecrets, "show-secrets", false, "Print credential-like launch option values instead of redacting them")
	return c
}

func dlcSummary(dlc []library.DLC) string {
	if len(dlc) == 0 {
		return ""
	}
	enabled := 0
	for _, d := range dlc {
		if d.Enabled {
			enabled++
		}
	}
	if enabled == len(dlc) {
		return fmt.Sprintf("%d", len(dlc))
	}
	return fmt.Sprintf("%d (%d disabled)", len(dlc), len(dlc)-enabled)
}

// Reuse the managed SteamCMD download command, including login, bootstrap,
// success-marker checking, timeout flags and post-download manifest validation.
func branchDownloadCommand(o *options) *cobra.Command {
	source := cmdCommand(o)
	var c *cobra.Command
	for _, candidate := range source.Commands() {
		if candidate.Name() == "download" {
			c = candidate
			break
		}
	}
	source.RemoveCommand(c)
	c.Flags().AddFlagSet(source.PersistentFlags())
	run := c.RunE
	var roots []string
	c.Use = "download APPID [BRANCH]"
	c.Short = "Download a branch into the installed game's directory using SteamCMD"
	c.Long = "Download a branch with the existing managed SteamCMD workflow. Omit BRANCH for public.\nSteam must be closed. SteamCMD handles account login and Steam Guard.\nThis updates files and verifies SteamCMD's manifest; the desktop client's own manifest\nis not rewritten to claim a completed branch change. Restart Steam to reconcile it."
	c.Args = cobra.RangeArgs(1, 2)
	c.Flags().MarkHidden("dir")
	c.Flags().MarkHidden("beta")
	rootsFlag(c, &roots)
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if cmd.Flags().Changed("dir") || cmd.Flags().Changed("beta") {
			return fmt.Errorf("use --root and the BRANCH argument with library branch download")
		}
		manifest, err := library.FindManifest(roots, args[0])
		if err != nil {
			return err
		}
		m, err := steamvdf.Parse(manifest)
		if err != nil {
			return err
		}
		state := steamvdf.Obj(m, "AppState")
		dir := steamvdf.Str(steamvdf.Get(state, "installdir"))
		if dir == "" || filepath.Base(dir) != dir || dir == ".." {
			return fmt.Errorf("invalid install directory in app manifest")
		}
		dir = filepath.Join(filepath.Dir(manifest), "common", dir)
		branch := "public"
		if len(args) == 2 {
			branch = args[1]
		}
		dry, _ := cmd.Flags().GetBool("dry-run")
		if !dry {
			if err := confirmWrite(cmd, false, "game files"); err != nil {
				return err
			}
		}
		if err := cmd.Flags().Set("dir", dir); err != nil {
			return err
		}
		if err := cmd.Flags().Set("beta", branch); err != nil {
			return err
		}
		return run(cmd, args[:1])
	}
	return c
}
