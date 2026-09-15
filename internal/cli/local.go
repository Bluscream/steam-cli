package cli

import (
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
	"steamcli.local/steam/internal/library"
)

func libraryCommand(o *options) *cobra.Command {
	var roots []string
	var customOnly bool
	var showSecrets bool
	var sortField string
	c := &cobra.Command{Use: "library", Short: "Inspect installed games and Steam library folders offline", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		r := roots
		if len(r) == 0 {
			r = library.Defaults()
		}
		v, e := library.Scan(r)
		if e != nil {
			return e
		}
		apps := v.Apps
		if customOnly {
			var filtered []library.App
			for _, a := range apps {
				if a.CompatTool != "" || a.LaunchOptions != "" {
					filtered = append(filtered, a)
				}
			}
			apps = filtered
		}

		switch strings.ToLower(sortField) {
		case "appid":
			sort.Slice(apps, func(i, j int) bool {
				idI, _ := strconv.Atoi(apps[i].AppID)
				idJ, _ := strconv.Atoi(apps[j].AppID)
				if idI != idJ {
					return idI < idJ
				}
				return apps[i].AppID < apps[j].AppID
			})
		case "name":
			sort.Slice(apps, func(i, j int) bool {
				return strings.ToLower(apps[i].Name) < strings.ToLower(apps[j].Name)
			})
		case "size":
			sort.Slice(apps, func(i, j int) bool {
				sizeI, _ := strconv.ParseInt(apps[i].SizeOnDisk, 10, 64)
				sizeJ, _ := strconv.ParseInt(apps[j].SizeOnDisk, 10, 64)
				if sizeI != sizeJ {
					return sizeI > sizeJ // Largest first
				}
				return apps[i].AppID < apps[j].AppID
			})
		case "library", "path":
			sort.Slice(apps, func(i, j int) bool {
				if apps[i].Library != apps[j].Library {
					return apps[i].Library < apps[j].Library
				}
				return apps[i].Name < apps[j].Name
			})
		case "":
			// Default order: AppID ascending (as returned by library.Scan)
		default:
			return fmt.Errorf("invalid --sort value %q: expected appid, name, size, or library", sortField)
		}

		if !showSecrets {
			// Redact in the data as well as the table: -o json is the form most
			// likely to be piped into a file or an issue.
			redacted := make([]library.App, len(apps))
			copy(redacted, apps)
			for i := range redacted {
				redacted[i].LaunchOptions = library.RedactLaunchOptions(redacted[i].LaunchOptions)
			}
			apps = redacted
		}
		return o.emit(cmd, apps, func(w io.Writer) {
			t := o.newTable(w)
			if customOnly {
				t.AppendHeader(table.Row{"AppID", "Name", "Compat Tool", "Launch Options"})
				for _, a := range apps {
					ct := a.CompatTool
					if ct == "" {
						ct = "-"
					}
					lo := a.LaunchOptions
					if !showSecrets {
						lo = library.RedactLaunchOptions(lo)
					}
					if lo == "" {
						lo = "-"
					}
					t.AppendRow(table.Row{a.AppID, truncate(a.Name, 35), ct, truncate(lo, 45)})
				}
			} else {
				t.AppendHeader(table.Row{"AppID", "Name", "Size", "Library"})
				t.SetColumnConfigs([]table.ColumnConfig{
					{Number: 3, Align: text.AlignRight},
				})
				for _, a := range apps {
					size := ""
					if n, err := strconv.ParseInt(a.SizeOnDisk, 10, 64); err == nil && n > 0 {
						size = o.sizeCell(n)
					}
					t.AppendRow(table.Row{a.AppID, truncate(a.Name, 44), size, a.Library})
				}
			}
			o.renderTable(t)
			if o.format != "csv" {
				if customOnly {
					fmt.Fprintf(w, "%s\n", faint(fmt.Sprintf("%d game(s) with custom compatibility tools or launch options.", len(apps))))
				} else {
					fmt.Fprintf(w, "%s\n", faint(fmt.Sprintf("%d app(s) across %d librar%s.",
						len(apps), len(v.Libraries), map[bool]string{true: "y", false: "ies"}[len(v.Libraries) == 1])))
					for _, warn := range v.Warnings {
						fmt.Fprintf(w, "%s %s\n", yellow.Sprint("Warning:"), warn)
					}
				}
			}
		})
	}}
	c.Flags().StringArrayVar(&roots, "root", nil, "Steam root directory; repeat for multiple installations")
	c.Flags().BoolVar(&customOnly, "custom", false, "Only list games with custom compatibility tools or launch options set")
	c.Flags().StringVar(&sortField, "sort", "", "Sort apps by: appid, name, size, library")
	c.Flags().BoolVar(&showSecrets, "show-secrets", false, "Print credential-like launch option values instead of redacting them")

	customSub := &cobra.Command{
		Use:     "custom",
		// "compat" and "launch-options" belong to the commands that can change
		// those settings, not to this read-only listing.
		Aliases: []string{"overrides", "customised"},
		Short:   "List installed games that have custom compatibility tools or launch options set",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			customOnly = true
			return c.RunE(cmd, args)
		},
	}
	customSub.Flags().StringArrayVar(&roots, "root", nil, "Steam root directory; repeat for multiple installations")
	customSub.Flags().StringVar(&sortField, "sort", "", "Sort apps by: appid, name, size, library")
	customSub.Flags().BoolVar(&showSecrets, "show-secrets", false, "Print credential-like launch option values instead of redacting them")
	c.AddCommand(customSub, compatCommand(o), launchCommand(o), dlcCommand(o), branchCommand(o), appConfigCommand(o))
	return c
}

var steam2 = regexp.MustCompile(`^STEAM_[01]:([01]):([0-9]+)$`)
var steam3 = regexp.MustCompile(`^\[U:1:([0-9]+)\]$`)

const steamIDBase uint64 = 76561197960265728

func convertID(s string) (map[string]string, error) {
	var account uint64
	if m := steam2.FindStringSubmatch(s); m != nil {
		n, e := strconv.ParseUint(m[2], 10, 31)
		if e != nil {
			return nil, errors.New("invalid Steam2 account ID")
		}
		y, _ := strconv.ParseUint(m[1], 10, 1)
		account = n*2 + y
	} else if m := steam3.FindStringSubmatch(s); m != nil {
		n, e := strconv.ParseUint(m[1], 10, 32)
		if e != nil {
			return nil, errors.New("invalid Steam3 account ID")
		}
		account = n
	} else {
		n, e := strconv.ParseUint(s, 10, 64)
		if e != nil || n < steamIDBase || n > steamIDBase+4294967295 {
			return nil, errors.New("expected an individual public SteamID64, STEAM_0:Y:Z, or [U:1:ACCOUNT]")
		}
		account = n - steamIDBase
	}
	if account == 0 {
		return nil, errors.New("zero account ID is invalid")
	}
	id := strconv.FormatUint(steamIDBase+account, 10)
	return map[string]string{"steamid64": id, "steamid2": fmt.Sprintf("STEAM_0:%d:%d", account%2, account/2), "steamid3": fmt.Sprintf("[U:1:%d]", account), "account_id": strconv.FormatUint(account, 10), "profile_url": "https://steamcommunity.com/profiles/" + id}, nil
}
func idCommand(o *options) *cobra.Command {
	return &cobra.Command{Use: "id STEAMID", Short: "Convert individual public Steam IDs offline", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		v, e := convertID(args[0])
		if e != nil {
			return e
		}
		return o.emit(cmd, v, func(w io.Writer) {
			t := o.newDetail(w)
			detailRows(t,
				kv("SteamID64", v["steamid64"]),
				kv("SteamID3", v["steamid3"]),
				kv("SteamID2", v["steamid2"]),
				kv("Account ID", v["account_id"]),
				kv("Profile URL", v["profile_url"]),
			)
			o.renderTable(t)
		})
	}}
}
