package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"time"

	"github.com/spf13/cobra"
	"steamcli.local/steam/internal/asf"
	"steamcli.local/steam/internal/config"
	"steamcli.local/steam/internal/httpx"
)

var Version = "0.1.0-dev"

type options struct {
	configPath, profile, format string
	timeout                     time.Duration
	offline, allowHTTP          bool
}

func New(in io.Reader, out, errOut io.Writer) *cobra.Command {
	o := &options{}
	r := &cobra.Command{Use: "steam", Short: "Private Steam toolkit: Web API, SteamCMD, ASF, and local libraries", Version: Version, SilenceUsage: true, SilenceErrors: true}
	r.SetIn(in)
	r.SetOut(out)
	r.SetErr(errOut)
	f := r.PersistentFlags()
	f.StringVar(&o.configPath, "config", "", "Configuration JSON path")
	f.StringVar(&o.profile, "profile", "", "Named configuration profile")
	f.StringVar(&o.format, "output", "json", "Output: json, compact, raw, parsed")
	f.DurationVar(&o.timeout, "timeout", 30*time.Second, "Timeout per HTTP attempt (not game downloads)")
	f.BoolVar(&o.offline, "offline", false, "Disable network and external SteamCMD execution; use cached metadata")
	f.BoolVar(&o.allowHTTP, "allow-http", false, "Allow plaintext HTTP outside loopback on a trusted network")
	r.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if o.timeout <= 0 {
			return errors.New("--timeout must be positive")
		}
		switch o.format {
		case "json", "compact", "raw", "parsed":
			return nil
		}
		return errors.New("--output must be json, compact, raw, or parsed")
	}
	r.AddCommand(statusCommand(o), workshopCommand(o), webCommand(o), asfCommand(o), cmdCommand(o), configCommand(o), doctorCommand(o), libraryCommand(o), idCommand(o))
	return r
}
func (o *options) settings() (config.Settings, error) {
	s, e := config.Load(o.configPath, o.profile)
	if e == nil && s.AllowHTTP {
		// A profile may opt its own hosts into plaintext; the flag is still
		// able to turn it on, never off.
		o.allowHTTP = true
	}
	return s, e
}
func (o *options) http() *httpx.Client { return httpx.New(o.timeout, o.offline, o.allowHTTP) }
func (o *options) print(cmd *cobra.Command, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	return o.printBytes(cmd, b)
}
func (o *options) printBytes(cmd *cobra.Command, b []byte) error {
	// "parsed" reduces an ASF envelope to the value behind it. Any other
	// payload falls through and is printed as JSON.
	if o.format == "parsed" && len(bytes.TrimSpace(b)) > 0 {
		if lines, ok := asf.Parse(b); ok {
			w := cmd.OutOrStdout()
			for _, l := range lines {
				var e error
				if l.Bot == "" || len(lines) == 1 {
					_, e = fmt.Fprintln(w, l.Value)
				} else {
					_, e = fmt.Fprintf(w, "%s: %s\n", l.Bot, l.Value)
				}
				if e != nil {
					return e
				}
			}
			return nil
		}
	}
	if o.format != "raw" && o.format != "parsed" && len(bytes.TrimSpace(b)) > 0 {
		var dst bytes.Buffer
		var e error
		if o.format == "compact" {
			e = json.Compact(&dst, b)
		} else {
			e = json.Indent(&dst, b, "", "  ")
		}
		if e != nil {
			return errors.New("response is not JSON; use --output raw for XML, VDF, or text")
		}
		b = dst.Bytes()
	}
	if len(b) == 0 {
		return nil
	}
	if _, e := cmd.OutOrStdout().Write(b); e != nil {
		return e
	}
	if b[len(b)-1] != '\n' {
		_, e := fmt.Fprintln(cmd.OutOrStdout())
		return e
	}
	return nil
}
func params(values []string) (url.Values, error) {
	p := make(url.Values)
	for _, s := range values {
		k, v, ok := bytes.Cut([]byte(s), []byte("="))
		if !ok || len(k) == 0 {
			return nil, errors.New("parameters must be NAME=VALUE")
		}
		p.Add(string(k), string(v))
	}
	return p, nil
}
func bodyInput(cmd *cobra.Command, s string) ([]byte, error) {
	if s == "" {
		return nil, nil
	}
	if s == "-" {
		return readBounded(cmd.InOrStdin())
	}
	if s[0] == '@' {
		f, e := os.Open(s[1:])
		if e != nil {
			return nil, e
		}
		defer f.Close()
		return readBounded(f)
	}
	return []byte(s), nil
}
func readBounded(r io.Reader) ([]byte, error) {
	b, e := io.ReadAll(io.LimitReader(r, 8<<20+1))
	if e != nil {
		return nil, e
	}
	if len(b) > 8<<20 {
		return nil, errors.New("input exceeds 8 MiB")
	}
	return b, nil
}
