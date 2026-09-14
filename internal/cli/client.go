package cli

import (
	"os"

	"github.com/spf13/cobra"
	"steamcli.local/steam/internal/steamclient"
)

func clientCommand(o *options) *cobra.Command {
	var path string

	root := &cobra.Command{
		Use:   "client",
		Short: "Drive the desktop Steam client (forwards to Valve's launcher)",
		Long: "Drive the desktop Steam client.\n\n" +
			"These subcommands forward to Valve's own Steam launcher, which owns login,\n" +
			"the overlay, and steam:// protocol handling. The client is located on PATH,\n" +
			"then in the usual install locations; --steam-path, STEAM_CLIENT_PATH, or\n" +
			"steam_client_path in your profile override that. A candidate that resolves\n" +
			"to this CLI is refused, so installing it as \"steam\" cannot cause a loop.",
	}
	root.PersistentFlags().StringVar(&path, "steam-path", "", "Steam client executable or launcher script")

	locator := func() (steamclient.Locator, error) {
		s, err := o.settings()
		if err != nil {
			return steamclient.Locator{}, err
		}
		p := s.SteamClientPath
		if path != "" {
			p = path
		}
		self, _ := os.Executable()
		return steamclient.Locator{Path: p, Self: self}, nil
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
		Short: "Show which Steam client executable would be used",
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
			return o.print(cmd, map[string]string{"path": p})
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

	// steam:// verbs that take an AppID.
	for _, v := range []struct{ use, verb, short string }{
		{"run APPID", "run", "Launch a game"},
		{"install APPID", "install", "Start installing a game"},
		{"uninstall APPID", "uninstall", "Start uninstalling a game"},
		{"validate APPID", "validate", "Verify a game's local files"},
		{"store APPID", "store", "Open a game's store page"},
	} {
		root.AddCommand(&cobra.Command{
			Use:   v.use,
			Short: v.short,
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				u, err := steamclient.URL(v.verb, args[0])
				if err != nil {
					return err
				}
				return run(cmd, []string{u})
			},
		})
	}

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
