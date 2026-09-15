package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"steamcli.local/steam/internal/asf"
	"steamcli.local/steam/internal/idle"
	"steamcli.local/steam/internal/sdk"
)

func idleCommand(o *options) *cobra.Command {
	var botFlag string
	var sdkOnly bool

	root := &cobra.Command{
		Use:     "idle [APPID...] [TEXT]",
		Aliases: []string{"spoof"},
		Short:   "Idle games or spoof playing status via ASF (with native SDK fallback)",
		Long: "Idle games or spoof status text.\n\n" +
			"By default, if ArchiSteamFarm (ASF) is configured and running, the command instructs\n" +
			"ASF to idle the given AppIDs and/or display the custom text using the '!play' command,\n" +
			"so it persists in the background without keeping this CLI running.\n\n" +
			"If ASF is not configured, not running, or if --sdk is specified, the CLI falls back to\n" +
			"running a background native Steamworks SDK session to idle the specified AppID.",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := o.settings()
			if err != nil {
				return err
			}

			var appIDs []int
			var textParts []string
			for _, a := range args {
				if id, err := strconv.Atoi(a); err == nil && id > 0 {
					appIDs = append(appIDs, id)
				} else {
					textParts = append(textParts, a)
				}
			}
			customText := strings.Join(textParts, " ")

			if len(appIDs) == 0 && customText == "" {
				return cmd.Help()
			}

			var asfc *asf.Client
			if !sdkOnly {
				pwd, _ := s.ASFPassword()
				asfc = &asf.Client{HTTP: o.http(), BaseURL: s.ASFURL, Password: pwd}
			}

			b, sdkErr := sdk.Load(s.DataDir)
			eng := &idle.Engine{
				ASFClient: asfc,
				DataDir:   s.DataDir,
				Helper:    b,
				HasHelper: sdkErr == nil,
			}

			res, err := eng.Start(cmd.Context(), appIDs, customText, botFlag)
			if err != nil {
				return err
			}

			return o.emit(cmd, res, func(w io.Writer) {
				if res.Method == "asf" {
					fmt.Fprintf(w, "%s %s (via ArchiSteamFarm)\n", green.Sprint("✓"), res.Message)
				} else {
					fmt.Fprintf(w, "%s %s (via native Steamworks SDK)\n", green.Sprint("✓"), res.Message)
				}
			})
		},
	}
	root.Flags().StringVarP(&botFlag, "bot", "b", "", "ASF bot selector to target (default: matches logged-in user, else ASF)")
	root.Flags().BoolVar(&sdkOnly, "sdk", false, "Force using native Steamworks SDK instead of ASF")

	stopCmd := &cobra.Command{
		Use:     "stop",
		Aliases: []string{"resume", "reset"},
		Short:   "Stop idling games and resume normal bot/client operation",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := o.settings()
			if err != nil {
				return err
			}
			pwd, _ := s.ASFPassword()
			asfc := &asf.Client{HTTP: o.http(), BaseURL: s.ASFURL, Password: pwd}

			b, sdkErr := sdk.Load(s.DataDir)
			eng := &idle.Engine{
				ASFClient: asfc,
				DataDir:   s.DataDir,
				Helper:    b,
				HasHelper: sdkErr == nil,
			}

			msg, err := eng.Stop(cmd.Context(), botFlag)
			if err != nil {
				return err
			}
			return o.emit(cmd, map[string]string{"status": "stopped", "message": msg}, func(w io.Writer) {
				fmt.Fprintf(w, "%s %s\n", green.Sprint("✓"), msg)
			})
		},
	}
	stopCmd.Flags().StringVarP(&botFlag, "bot", "b", "", "ASF bot selector to target (default: matches logged-in user, else ASF)")

	root.AddCommand(stopCmd)
	return root
}

