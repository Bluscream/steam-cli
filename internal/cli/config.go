package cli

import (
	"github.com/spf13/cobra"
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
		return o.print(cmd, map[string]any{"profile": s.ProfileName, "config": s.ConfigPath, "settings": s.Profile, "data_dir": s.DataDir, "cache_dir": s.CacheDir})
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
		return o.print(cmd, map[string]any{"platform": runtime.GOOS + "/" + runtime.GOARCH, "version": Version, "go": runtime.Version(), "profile": s.ProfileName, "web_key_present": key != "", "asf_password_present": password != "", "steamcmd_installed": installed, "steamcmd_path": p, "data_dir": s.DataDir, "cache_dir": s.CacheDir, "notes": notes})
	}}
}
