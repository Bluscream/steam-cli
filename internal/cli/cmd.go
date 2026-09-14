package cli

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"steamcli.local/steam/internal/steamcmd"
)

func cmdCommand(o *options) *cobra.Command {
	var path, checksum string
	var noDownload bool
	var runTimeout time.Duration
	root := &cobra.Command{Use: "cmd", Short: "Manage and run Valve SteamCMD (automatically downloaded on first use)"}
	root.PersistentFlags().StringVar(&path, "steamcmd-path", "", "Use a specific executable or compatibility wrapper")
	root.PersistentFlags().BoolVar(&noDownload, "no-download", false, "Do not automatically download the SteamCMD bootstrap")
	root.PersistentFlags().DurationVar(&runTimeout, "run-timeout", 0, "Subprocess time limit (0 = unlimited)")
	manager := func(cmd *cobra.Command) (*steamcmd.Manager, error) {
		s, e := o.settings()
		if e != nil {
			return nil, e
		}
		p := s.SteamCMDPath
		if path != "" {
			p = path
		}
		return &steamcmd.Manager{HTTP: o.http(), DataDir: s.DataDir, Path: p, SHA256: checksum, NoDownload: noDownload, Progress: cmd.ErrOrStderr()}, nil
	}
	execute := func(cmd *cobra.Command, args []string, success string) error {
		if runTimeout < 0 {
			return errors.New("--run-timeout must not be negative")
		}
		m, e := manager(cmd)
		if e != nil {
			return e
		}
		ctx := cmd.Context()
		if runTimeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, runTimeout)
			defer cancel()
		}
		return m.Run(ctx, args, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(), success)
	}
	install := &cobra.Command{Use: "install", Short: "Download the official bootstrap if SteamCMD is absent", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		m, e := manager(cmd)
		if e != nil {
			return e
		}
		p, e := m.Ensure(cmd.Context())
		if e != nil {
			return e
		}
		return o.emit(cmd, map[string]string{"path": p}, func(w io.Writer) {
			t := o.newDetail(w)
			detailRows(t,
				kv("SteamCMD path", p),
				kv("Status", green.Sprint("installed")),
			)
			t.Render()
		})
	}}
	install.Flags().StringVar(&checksum, "sha256", "", "Require this SHA-256 when downloading a new bootstrap")
	locate := &cobra.Command{Use: "path", Short: "Locate SteamCMD without downloading or executing it", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		m, e := manager(cmd)
		if e != nil {
			return e
		}
		p, e := m.Find()
		if e != nil {
			return e
		}
		return o.emit(cmd, map[string]string{"path": p}, func(w io.Writer) {
			t := o.newDetail(w)
			detailRows(t,
				kv("SteamCMD path", p),
				kv("Status", green.Sprint("found")),
			)
			t.Render()
		})
	}}
	run := &cobra.Command{Use: "run -- [STEAMCMD_ARGS...]", Short: "Pass arguments directly; omit arguments for an interactive console", Example: "  steam cmd run -- +login anonymous +app_info_print 730 +quit", Args: cobra.ArbitraryArgs, RunE: func(cmd *cobra.Command, args []string) error { return execute(cmd, args, "") }}
	update := &cobra.Command{Use: "update", Short: "Start SteamCMD, allow its self-update, then quit", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error { return execute(cmd, []string{"+quit"}, "") }}
	root.AddCommand(install, locate, run, update)
	for _, workshop := range []bool{false, true} {
		var d steamcmd.Download
		var dry bool
		use, short, n := "download APPID", "Install or update an app", 1
		if workshop {
			use, short, n = "workshop APPID ITEMID", "Download a workshop item", 2
		}
		c := &cobra.Command{Use: use, Short: short, Args: cobra.ExactArgs(n), RunE: func(cmd *cobra.Command, args []string) error {
			d.AppID = args[0]
			if workshop {
				d.ItemID = args[1]
			}
			if d.Dir != "" {
				p, e := filepath.Abs(d.Dir)
				if e != nil {
					return e
				}
				d.Dir = p
			} else if !workshop {
				return errors.New("--dir is required for app downloads")
			}
			a, success, e := d.Args()
			if e != nil {
				return e
			}
			if dry {
				return o.print(cmd, map[string]any{"arguments": a, "success_marker": success})
			}
			if e = execute(cmd, a, success); e != nil {
				return e
			}
			if !workshop {
				return steamcmd.VerifyApp(d.Dir, d.AppID)
			}
			return nil
		}}
		f := c.Flags()
		f.StringVar(&d.Dir, "dir", "", "Absolute or relative installation directory")
		f.StringVar(&d.User, "user", "anonymous", "Steam account name; Valve prompts for password/Steam Guard")
		f.BoolVar(&d.Validate, "validate", false, "Validate downloaded files")
		f.StringVar(&d.Platform, "platform", "", "Target platform: windows, linux, macos")
		if !workshop {
			f.StringVar(&d.Branch, "beta", "", "Beta branch name")
		}
		f.BoolVar(&dry, "dry-run", false, "Print arguments without downloading or running SteamCMD")
		root.AddCommand(c)
	}
	return root
}
