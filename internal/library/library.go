// Package library reads local Steam metadata without launching Steam.
package library

import (
	"errors"
	"fmt"
	"github.com/andygrunwald/vdf"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

type App struct {
	AppID         string `json:"appid"`
	Name          string `json:"name"`
	InstallDir    string `json:"install_dir"`
	Library       string `json:"library"`
	Manifest      string `json:"manifest"`
	StateFlags    string `json:"state_flags"`
	SizeOnDisk    string `json:"size_on_disk,omitempty"`
	CompatTool    string `json:"compat_tool,omitempty"`
	LaunchOptions string `json:"launch_options,omitempty"`
}
type Report struct {
	Libraries []string `json:"libraries"`
	Apps      []App    `json:"apps"`
	Warnings  []string `json:"warnings,omitempty"`
}

func Defaults() []string {
	h, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "windows":
		return []string{filepath.Join(os.Getenv("ProgramFiles(x86)"), "Steam"), filepath.Join(os.Getenv("ProgramFiles"), "Steam")}
	case "darwin":
		return []string{filepath.Join(h, "Library", "Application Support", "Steam")}
	default:
		return []string{filepath.Join(h, ".steam", "steam"), filepath.Join(h, ".local", "share", "Steam"), filepath.Join(h, ".var", "app", "com.valvesoftware.Steam", ".local", "share", "Steam")}
	}
}

// LoggedInUser inspects loginusers.vdf across the provided Steam roots (or Defaults() if empty)
// and returns the SteamID64 of the most recently active or autologin user.
func LoggedInUser(roots []string) (string, error) {
	if len(roots) == 0 {
		roots = Defaults()
	}
	type candidate struct {
		steamID   string
		autoLogin bool
		timestamp int64
	}
	var best *candidate
	for _, root := range roots {
		path := filepath.Join(root, "config", "loginusers.vdf")
		m, err := parse(path)
		if err != nil {
			continue
		}
		users, ok := m["users"].(map[string]interface{})
		if !ok {
			users, _ = m["Users"].(map[string]interface{})
		}
		for id, u := range users {
			data, ok := u.(map[string]interface{})
			if !ok {
				continue
			}
			auto := str(data["AutoLogin"]) == "1" || str(data["MostRecent"]) == "1"
			var ts int64
			if tStr := str(data["Timestamp"]); tStr != "" {
				ts, _ = strconv.ParseInt(tStr, 10, 64)
			}
			c := &candidate{steamID: id, autoLogin: auto, timestamp: ts}
			if best == nil {
				best = c
				continue
			}
			if c.autoLogin && !best.autoLogin {
				best = c
			} else if c.autoLogin == best.autoLogin && c.timestamp > best.timestamp {
				best = c
			}
		}
	}
	if best != nil && best.steamID != "" {
		return best.steamID, nil
	}
	return "", errors.New("no logged-in Steam user found in loginusers.vdf")
}
func parse(path string) (map[string]interface{}, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	s, e := f.Stat()
	if e != nil {
		return nil, e
	}
	if s.Size() > 16<<20 {
		return nil, errors.New("VDF file exceeds 16 MiB")
	}
	return vdf.NewParser(io.LimitReader(f, 16<<20)).Parse()
}
func str(v any) string { s, _ := v.(string); return s }
func Scan(roots []string) (Report, error) {
	r := Report{Libraries: []string{}, Apps: []App{}}
	seen := map[string]bool{}
	add := func(p string) {
		p, e := filepath.Abs(p)
		if e != nil {
			return
		}
		if real, e := filepath.EvalSymlinks(p); e == nil {
			p = real
		}
		if !seen[p] {
			seen[p] = true
			r.Libraries = append(r.Libraries, p)
		}
	}
	for _, root := range roots {
		if s, e := os.Stat(filepath.Join(root, "steamapps")); e != nil || !s.IsDir() {
			continue
		}
		add(root)
		path := filepath.Join(root, "steamapps", "libraryfolders.vdf")
		m, e := parse(path)
		if e != nil {
			if !errors.Is(e, os.ErrNotExist) {
				r.Warnings = append(r.Warnings, fmt.Sprintf("cannot parse %s", path))
			}
			continue
		}
		libs, ok := m["libraryfolders"].(map[string]interface{})
		if !ok {
			libs, _ = m["LibraryFolders"].(map[string]interface{})
		}
		for k, v := range libs {
			if _, e := strconv.Atoi(k); e != nil {
				continue
			}
			if p := str(v); p != "" {
				add(p)
			} else if entry, ok := v.(map[string]interface{}); ok {
				if p := str(entry["path"]); p != "" {
					add(p)
				}
			}
		}
	}
	if len(r.Libraries) == 0 {
		return r, errors.New("no Steam libraries found; pass --root /path/to/Steam")
	}
	sort.Strings(r.Libraries)
	for _, lib := range r.Libraries {
		paths, e := filepath.Glob(filepath.Join(lib, "steamapps", "appmanifest_*.acf"))
		if e != nil {
			return r, e
		}
		for _, path := range paths {
			m, e := parse(path)
			if e != nil {
				r.Warnings = append(r.Warnings, "cannot parse "+path)
				continue
			}
			a, ok := m["AppState"].(map[string]interface{})
			if !ok {
				r.Warnings = append(r.Warnings, "missing AppState in "+path)
				continue
			}
			install := str(a["installdir"])
			if !filepath.IsLocal(install) || strings.Contains(install, "\\") {
				r.Warnings = append(r.Warnings, "invalid installation path in "+path)
				continue
			}
			app := App{
				AppID:      str(a["appid"]),
				Name:       str(a["name"]),
				InstallDir: filepath.Join(lib, "steamapps", "common", install),
				Library:    lib,
				Manifest:   path,
				StateFlags: str(a["StateFlags"]),
				SizeOnDisk: str(a["SizeOnDisk"]),
			}
			r.Apps = append(r.Apps, app)
		}
	}

	// Enrich apps with custom compatibility tools and launch options
	compatTools := ScanCompatTools(roots)
	launchOpts := ScanLaunchOptions(roots)
	for i := range r.Apps {
		id := r.Apps[i].AppID
		if ct, ok := compatTools[id]; ok {
			r.Apps[i].CompatTool = ct
		}
		if lo, ok := launchOpts[id]; ok {
			r.Apps[i].LaunchOptions = lo
		}
	}

	sort.Slice(r.Apps, func(i, j int) bool { return r.Apps[i].AppID < r.Apps[j].AppID })
	return r, nil
}

// ScanCompatTools inspects config.vdf in the given Steam roots and returns a map of AppID to custom compatibility tool name.
func ScanCompatTools(roots []string) map[string]string {
	if len(roots) == 0 {
		roots = Defaults()
	}
	out := make(map[string]string)
	for _, root := range roots {
		path := filepath.Join(root, "config", "config.vdf")
		m, err := parse(path)
		if err != nil {
			continue
		}
		// Structure: InstallConfigStore -> Software -> Valve -> Steam -> CompatToolMapping
		ics, ok := m["InstallConfigStore"].(map[string]interface{})
		if !ok {
			ics = m
		}
		software, _ := ics["Software"].(map[string]interface{})
		valve, _ := software["Valve"].(map[string]interface{})
		steam, _ := valve["Steam"].(map[string]interface{})
		ctm, _ := steam["CompatToolMapping"].(map[string]interface{})
		for id, val := range ctm {
			if id == "0" {
				continue // 0 is global default
			}
			if entry, ok := val.(map[string]interface{}); ok {
				name := str(entry["name"])
				if name != "" {
					out[id] = name
				}
			}
		}
	}
	return out
}

// ScanLaunchOptions inspects userdata/*/config/localconfig.vdf in the given Steam roots and returns a map of AppID to custom launch options.
func ScanLaunchOptions(roots []string) map[string]string {
	if len(roots) == 0 {
		roots = Defaults()
	}
	out := make(map[string]string)
	for _, root := range roots {
		pattern := filepath.Join(root, "userdata", "*", "config", "localconfig.vdf")
		files, err := filepath.Glob(pattern)
		if err != nil {
			continue
		}
		for _, file := range files {
			m, err := parse(file)
			if err != nil {
				continue
			}
			// UserLocalConfigStore -> Software -> Valve -> Steam -> apps / Apps
			ulcs, ok := m["UserLocalConfigStore"].(map[string]interface{})
			if !ok {
				ulcs = m
			}
			software, _ := ulcs["Software"].(map[string]interface{})
			valve, _ := software["Valve"].(map[string]interface{})
			steam, _ := valve["Steam"].(map[string]interface{})
			apps, ok := steam["apps"].(map[string]interface{})
			if !ok {
				apps, _ = steam["Apps"].(map[string]interface{})
			}
			for id, appData := range apps {
				if ad, ok := appData.(map[string]interface{}); ok {
					lo := str(ad["LaunchOptions"])
					if lo != "" {
						out[id] = lo
					}
				}
			}
		}
	}
	return out
}
