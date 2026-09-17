package cli

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"time"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
	"steamcli.local/steam/internal/account"
	"steamcli.local/steam/internal/asf"
	"steamcli.local/steam/internal/community"
	"steamcli.local/steam/internal/library"
	"steamcli.local/steam/internal/steamclient"
)

func accountCommand(o *options) *cobra.Command {
	var roots []string
	root := &cobra.Command{
		Use:     "account",
		Aliases: []string{"acc", "user"},
		Short:   "Manage Steam accounts, switch users, and edit profile details",
	}

	asfClient := func() (*asf.Client, error) {
		s, err := o.settings()
		if err != nil {
			return nil, err
		}
		pwd, err := s.ASFPassword()
		if err != nil {
			return nil, err
		}
		return &asf.Client{HTTP: o.http(), BaseURL: s.ASFURL, Password: pwd}, nil
	}

	communityClient := func() (*community.Client, error) {
		s, err := o.settings()
		if err != nil {
			return nil, err
		}
		cookie, err := s.CommunityLoginSecure()
		if err != nil {
			return nil, err
		}
		if cookie == "" {
			return nil, community.ErrNoSession
		}
		return &community.Client{HTTP: o.http(), BaseURL: s.CommunityURL, LoginSecure: cookie}, nil
	}

	listCmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List all accounts saved in local Steam client login records",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			r := roots
			if len(r) == 0 {
				r = library.Defaults()
			}
			users, err := account.List(r)
			if err != nil {
				return err
			}

			// Try matching ASF bots to accounts by SteamID
			asfBotMap := make(map[string]string)
			if asfc, err := asfClient(); err == nil {
				if b, err := asfc.Call(cmd.Context(), "GET", "Api/Bot/ASF", nil, nil); err == nil {
					if summaries, ok := asf.Bots(b); ok {
						for _, bot := range summaries {
							if bot.SteamID != "" {
								asfBotMap[bot.SteamID] = bot.Name
							}
						}
					}
				}
			}

			for i := range users {
				if botName, ok := asfBotMap[users[i].SteamID64]; ok {
					users[i].ASFBot = botName
				}
			}

			return o.emit(cmd, users, func(w io.Writer) {
				t := o.newTable(w)
				t.AppendHeader(table.Row{"Active", "Persona Name", "Account Name", "SteamID64", "ASF Bot", "Last Logged In"})
				for _, u := range users {
					activeMarker := ""
					if u.AutoLogin {
						activeMarker = green.Sprint("● active")
					} else if u.MostRecent {
						activeMarker = yellow.Sprint("○ recent")
					}
					lastUsed := "-"
					if u.Timestamp > 0 {
						lastUsed = time.Unix(u.Timestamp, 0).Format("2006-01-02 15:04:05")
					}
					botCol := "-"
					if u.ASFBot != "" {
						botCol = cyan.Sprint(u.ASFBot)
					}
					t.AppendRow(table.Row{activeMarker, u.PersonaName, u.AccountName, u.SteamID64, botCol, lastUsed})
				}
				o.renderTable(t)
			})
		},
	}

	var restartSteam bool
	switchCmd := &cobra.Command{
		Use:     "switch ACCOUNT_OR_STEAMID",
		Aliases: []string{"login"},
		Short:   "Switch the active Steam account for next client launch",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r := roots
			if len(r) == 0 {
				r = library.Defaults()
			}
			if restartSteam {
				// Terminate running Steam instances
				_ = exec.Command("pkill", "-TERM", "steam").Run()
				time.Sleep(500 * time.Millisecond)
			}
			switched, err := account.Switch(r, args[0])
			if err != nil {
				return err
			}
			if restartSteam {
				l := steamclient.Locator{}
				if p, err := l.Find(); err == nil {
					_ = exec.Command(p).Start()
				}
			}
			return o.emit(cmd, switched, func(w io.Writer) {
				fmt.Fprintf(w, "%s Switched active account to %s (%s, SteamID %s)\n",
					green.Sprint("✓"), text.Colors{text.Bold}.Sprint(switched.PersonaName), switched.AccountName, switched.SteamID64)
				if restartSteam {
					fmt.Fprintf(w, "Restarted Steam client.\n")
				}
			})
		},
	}
	switchCmd.Flags().BoolVar(&restartSteam, "restart", false, "Restart Steam client after updating login configuration")

	activeCmd := &cobra.Command{
		Use:     "active",
		Aliases: []string{"whoami", "current"},
		Short:   "Show the currently active / autologin Steam user",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			r := roots
			if len(r) == 0 {
				r = library.Defaults()
			}
			u, err := account.Active(r)
			if err != nil {
				return err
			}
			return o.emit(cmd, u, func(w io.Writer) {
				t := o.newDetail(w)
				detailRows(t,
					kv("Persona Name", u.PersonaName),
					kv("Account Name", u.AccountName),
					kv("SteamID64", u.SteamID64),
					kv("AutoLogin", fmt.Sprint(u.AutoLogin)),
					kv("MostRecent", fmt.Sprint(u.MostRecent)),
				)
				o.renderTable(t)
			})
		},
	}

	forgetCmd := &cobra.Command{
		Use:   "forget ACCOUNT_OR_STEAMID",
		Short: "Remove an account from Steam's saved loginusers list",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r := roots
			if len(r) == 0 {
				r = library.Defaults()
			}
			if err := account.Forget(r, args[0]); err != nil {
				return err
			}
			return o.emit(cmd, map[string]string{"status": "forgotten", "target": args[0]}, func(w io.Writer) {
				fmt.Fprintf(w, "%s Removed account %s from loginusers.vdf\n", green.Sprint("✓"), args[0])
			})
		},
	}

	var nameFlag, bioFlag, realNameFlag, customURLFlag, countryFlag, privacyFlag, botFlag string
	editCmd := &cobra.Command{
		Use:     "edit",
		Aliases: []string{"set", "update"},
		Short:   "Update profile details (nickname, bio, real name, custom URL, privacy)",
		Long: "Update Steam profile details.\n\n" +
			"When ArchiSteamFarm (ASF) is configured and running, nickname and privacy updates\n" +
			"are sent directly to ASF. For full profile edits (bio, real name, custom URL) or when\n" +
			"ASF is absent, updates are made via your authenticated Steam Community session.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if nameFlag == "" && bioFlag == "" && realNameFlag == "" && customURLFlag == "" && countryFlag == "" && privacyFlag == "" {
				return errors.New("specify at least one field to edit: --name, --bio, --real-name, --custom-url, --country, or --privacy")
			}

			var actionsDone []string

			// Try ASF first for nickname / privacy
			asfc, asfErr := asfClient()
			if asfErr == nil && asfc.BaseURL != "" {
				targetBot := botFlag
				if targetBot == "" || targetBot == "ASF" {
					if u, err := account.Active(roots); err == nil && u.SteamID64 != "" {
						if bName, err := asfc.BotNameForSteamID(cmd.Context(), u.SteamID64); err == nil && bName != "" {
							targetBot = bName
						}
					}
				}
				if targetBot == "" {
					targetBot = "ASF"
				}

				if nameFlag != "" && bioFlag == "" && realNameFlag == "" && customURLFlag == "" && countryFlag == "" {
					msg, err := asfc.SetNickname(cmd.Context(), targetBot, nameFlag)
					if err == nil {
						actionsDone = append(actionsDone, fmt.Sprintf("ASF nickname updated: %s", msg))
						return o.emit(cmd, map[string]any{"method": "asf", "result": msg}, func(w io.Writer) {
							fmt.Fprintf(w, "%s %s\n", green.Sprint("✓"), msg)
						})
					}
				}
				if privacyFlag != "" && nameFlag == "" && bioFlag == "" && realNameFlag == "" && customURLFlag == "" && countryFlag == "" {
					msg, err := asfc.SetPrivacy(cmd.Context(), targetBot, privacyFlag)
					if err == nil {
						actionsDone = append(actionsDone, fmt.Sprintf("ASF privacy updated: %s", msg))
						return o.emit(cmd, map[string]any{"method": "asf", "result": msg}, func(w io.Writer) {
							fmt.Fprintf(w, "%s %s\n", green.Sprint("✓"), msg)
						})
					}
				}
			}

			// Community session update for web profile fields
			commClient, err := communityClient()
			if err != nil {
				if len(actionsDone) > 0 {
					return nil
				}
				return fmt.Errorf("profile update requires either a working ASF instance or a Steam Community session (STEAM_LOGIN_SECURE): %w", err)
			}

			fields := community.ProfileFields{
				PersonaName: nameFlag,
				RealName:    realNameFlag,
				Summary:     bioFlag,
				CustomURL:   customURLFlag,
				Country:     countryFlag,
			}
			if err := commClient.EditProfile(cmd.Context(), fields); err != nil {
				return fmt.Errorf("update profile: %w", err)
			}

			return o.emit(cmd, map[string]any{"method": "community", "updated": fields}, func(w io.Writer) {
				fmt.Fprintf(w, "%s Steam Community profile updated successfully\n", green.Sprint("✓"))
			})
		},
	}
	editCmd.Flags().StringVarP(&nameFlag, "name", "n", "", "Profile persona name / nickname")
	editCmd.Flags().StringVar(&bioFlag, "bio", "", "Profile summary / bio")
	editCmd.Flags().StringVar(&realNameFlag, "real-name", "", "Profile real name")
	editCmd.Flags().StringVar(&customURLFlag, "custom-url", "", "Custom vanity URL")
	editCmd.Flags().StringVar(&countryFlag, "country", "", "Country code")
	editCmd.Flags().StringVar(&privacyFlag, "privacy", "", "Privacy settings: Private, FriendsOnly, Public")
	editCmd.Flags().StringVarP(&botFlag, "bot", "b", "", "ASF bot name to target if using ASF (default: matches logged-in user, else ASF)")

	nameCmd := &cobra.Command{
		Use:     "name NEW_NAME",
		Aliases: []string{"nick", "nickname", "set-name", "set-nick"},
		Short:   "Quickly change your Steam nickname / persona name",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			nameFlag = args[0]
			return editCmd.RunE(cmd, nil)
		},
	}
	nameCmd.Flags().StringVarP(&botFlag, "bot", "b", "", "ASF bot name to target if using ASF (default: matches logged-in user, else ASF)")

	privacyCmd := &cobra.Command{
		Use:     "privacy LEVEL",
		Aliases: []string{"set-privacy"},
		Short:   "Quickly set profile privacy (Private, FriendsOnly, Public)",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			privacyFlag = args[0]
			return editCmd.RunE(cmd, nil)
		},
	}
	privacyCmd.Flags().StringVarP(&botFlag, "bot", "b", "", "ASF bot name to target if using ASF (default: matches logged-in user, else ASF)")

	root.RunE = listCmd.RunE
	root.AddCommand(listCmd, switchCmd, activeCmd, forgetCmd, editCmd, nameCmd, privacyCmd)
	return root
}
