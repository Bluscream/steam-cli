package cli

import (
	"fmt"
	"github.com/spf13/cobra"
	"io"
	"os"
	"runtime"
	"steamcli.local/steam/internal/config"
	"steamcli.local/steam/internal/steamcmd"
)

func configCommand(o *options) *cobra.Command {
	root := &cobra.Command{Use: "config", Short: "Manage non-secret profiles; credentials remain in environment or separate files"}
	root.AddCommand(&cobra.Command{Use: "init", Short: "Create a private example configuration without overwriting existing files", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		p, _, _, e := config.Paths()
		if e != nil {
			return e
		}
		if o.configPath != "" {
			p = o.configPath
		}
		if e = config.Init(p); e != nil {
			return e
		}
		return o.print(cmd, map[string]string{"config": p})
	}})
	root.AddCommand(&cobra.Command{Use: "show", Short: "Show effective non-secret settings (never resolves credentials)", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		s, e := o.settings()
		if e != nil {
			return e
		}
		data := map[string]any{"profile": s.ProfileName, "config": s.ConfigPath, "settings": s.Profile, "data_dir": s.DataDir, "cache_dir": s.CacheDir}
		return o.emit(cmd, data, func(w io.Writer) {
			t := o.newDetail(w)
			detailRows(t,
				kv("Profile", s.ProfileName),
				kv("Config", s.ConfigPath),
				kv("Data dir", s.DataDir),
				kv("Cache dir", s.CacheDir),
				kv("Web URL", s.WebURL),
				kv("Community URL", s.CommunityURL),
				kv("ASF URL", s.ASFURL),
			)
			t.Render()
		})
	}})
	return root
}
func doctorCommand(o *options) *cobra.Command {
	return &cobra.Command{Use: "doctor", Short: "Report local configuration, credential presence, and platform requirements", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		s, e := o.settings()
		if e != nil {
			return e
		}
		key, ke := s.WebKey()
		password, pe := s.ASFPassword()
		token, te := s.AccessToken()
		cookie, ce := s.CommunityLoginSecure()
		m := &steamcmd.Manager{DataDir: s.DataDir, Path: s.SteamCMDPath}
		p, e := m.Find()
		installed := e == nil
		notes := []string{}
		if runtime.GOOS == "linux" {
			if _, e := os.Stat("/lib/ld-linux.so.2"); e != nil {
				notes = append(notes, "Valve SteamCMD needs a 32-bit glibc loader and libstdc++; /lib/ld-linux.so.2 was not found")
			}
		}
		if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
			notes = append(notes, "Valve's Intel SteamCMD requires Rosetta 2 on Apple Silicon")
		}
		if _, e := steamcmd.ArchiveName(runtime.GOOS, runtime.GOARCH); e != nil {
			notes = append(notes, e.Error())
		}
		if ke != nil {
			notes = append(notes, "Steam Web API secret could not be read")
		}
		if pe != nil {
			notes = append(notes, "ASF IPC secret could not be read")
		}
		if te != nil {
			notes = append(notes, "Steam access token could not be read")
		}
		if ce != nil {
			notes = append(notes, "Steam Community session cookie could not be read")
		}
		if password == "" {
			notes = append(notes, "no ASF IPC password is set ("+s.ASFPasswordEnv+"); 'steam asf' needs a running ArchiSteamFarm instance at "+s.ASFURL)
		}
		if cookie == "" {
			notes = append(notes, "no Community session cookie is set ("+s.CommunityLoginSecureEnv+"); workshop subs, favorites, and collection membership need one")
		}
		out := map[string]any{"platform": runtime.GOOS + "/" + runtime.GOARCH, "version": Version, "go": runtime.Version(), "profile": s.ProfileName, "web_key_present": key != "", "asf_password_present": password != "", "steamcmd_installed": installed, "steamcmd_path": p, "data_dir": s.DataDir, "cache_dir": s.CacheDir, "config_path": s.ConfigPath,
			"web_url": s.WebURL, "community_url": s.CommunityURL, "asf_url": s.ASFURL,
			"access_token_present": token != "", "community_session_present": cookie != "",
			"notes": notes}
		return o.emit(cmd, out, func(w io.Writer) {
			t := o.newDetail(w)
			detailRows(t,
				kv("Version", Version),
				kv("Platform", runtime.GOOS+"/"+runtime.GOARCH),
				kv("Go", runtime.Version()),
				kv("Profile", s.ProfileName),
				kv("Config path", s.ConfigPath),
				kv("Web URL", s.WebURL),
				kv("Community URL", s.CommunityURL),
				kv("ASF URL", s.ASFURL),
				kv("Data dir", s.DataDir),
				kv("Cache dir", s.CacheDir),
				kv("Web API key", colorBool(key != "")),
				kv("Access token", colorBool(token != "")),
				kv("Community session", colorBool(cookie != "")),
				kv("ASF password", colorBool(password != "")),
				kv("SteamCMD installed", colorBool(installed)),
				kv("SteamCMD path", p),
			)
			t.Render()
			for _, n := range notes {
				fmt.Fprintf(w, "%s %s\n", yellow.Sprint("Note:"), n)
			}
		})
	}}
}
