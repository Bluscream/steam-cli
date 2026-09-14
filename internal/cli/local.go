package cli

import (
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"

	"github.com/spf13/cobra"
	"steamcli.local/steam/internal/library"
)

func libraryCommand(o *options) *cobra.Command {
	var roots []string
	c := &cobra.Command{Use: "library", Short: "Inspect installed games and Steam library folders offline", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		r := roots
		if len(r) == 0 {
			r = library.Defaults()
		}
		v, e := library.Scan(r)
		if e != nil {
			return e
		}
		return o.emit(cmd, v, func(w io.Writer) {
			t := tw(w)
			fmt.Fprintln(t, "APPID\tNAME\tSIZE\tLIBRARY")
			for _, a := range v.Apps {
				size := ""
				if n, err := strconv.ParseInt(a.SizeOnDisk, 10, 64); err == nil && n > 0 {
					size = humanBytes(n)
				}
				fmt.Fprintf(t, "%s\t%s\t%s\t%s\n", a.AppID, truncate(a.Name, 44), size, a.Library)
			}
			t.Flush()
			fmt.Fprintf(w, "\n%d app(s) across %d librar%s.\n",
				len(v.Apps), len(v.Libraries), map[bool]string{true: "y", false: "ies"}[len(v.Libraries) == 1])
			for _, warn := range v.Warnings {
				fmt.Fprintf(w, "Warning: %s\n", warn)
			}
		})
	}}
	c.Flags().StringArrayVar(&roots, "root", nil, "Steam root directory; repeat for multiple installations")
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
			t := tw(w)
			for _, k := range []string{"steamid64", "steamid3", "steamid2", "account_id", "profile_url"} {
				fmt.Fprintf(t, "%s\t%s\n", k, v[k])
			}
			t.Flush()
		})
	}}
}
