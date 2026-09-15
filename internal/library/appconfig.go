package library

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"steamcli.local/steam/internal/steamvdf"
)

// DLC is one downloadable-content depot recorded in an app's manifest.
type DLC struct {
	AppID string `json:"appid"`
	// Depot is the depot carrying this DLC's files.
	Depot string `json:"depot"`
	// Size is the depot's size on disk in bytes, as Steam recorded it.
	Size int64 `json:"size,omitempty"`
	// Enabled is false when the AppID appears in the manifest's DisabledDLC
	// list, which is how the client records an unticked box.
	Enabled bool `json:"enabled"`
}

// Branch is the beta participation recorded for an installed app.
type Branch struct {
	// Requested is the branch the user selected. Empty means the public branch.
	Requested string `json:"requested,omitempty"`
	// Mounted is the branch whose files are actually installed. It lags
	// Requested until Steam next updates the app.
	Mounted string `json:"mounted,omitempty"`
}

// Public is the name shown for an app on no beta branch.
func (b Branch) Public() bool { return b.Requested == "" }

// Pending reports whether a branch change has been requested but not yet
// downloaded. Writing a branch only records the request; Steam or SteamCMD has
// to fetch the files.
func (b Branch) Pending() bool { return b.Requested != b.Mounted }

// AppConfig is the mutable per-app state the client keeps on disk.
type AppConfig struct {
	AppID         string `json:"appid"`
	Name          string `json:"name,omitempty"`
	Manifest      string `json:"manifest"`
	Branch        Branch `json:"branch"`
	DLC           []DLC  `json:"dlc,omitempty"`
	LaunchOptions string `json:"launch_options,omitempty"`
	// LocalConfig is the localconfig.vdf that LaunchOptions came from, if any.
	LocalConfig string `json:"local_config,omitempty"`
}

// FindManifest locates the appmanifest for an AppID across the given roots.
func FindManifest(roots []string, appID string) (string, error) {
	if _, err := strconv.ParseUint(appID, 10, 64); err != nil {
		return "", fmt.Errorf("AppID must be a number, got %q", appID)
	}
	if len(roots) == 0 {
		roots = Defaults()
	}
	report, err := Scan(roots)
	if err != nil {
		return "", err
	}
	for _, a := range report.Apps {
		if a.AppID == appID {
			return a.Manifest, nil
		}
	}
	return "", fmt.Errorf("app %s is not installed in any scanned library", appID)
}

// LoadAppConfig reads everything this package can change about one installed app.
func LoadAppConfig(roots []string, appID string) (AppConfig, error) {
	manifest, err := FindManifest(roots, appID)
	if err != nil {
		return AppConfig{}, err
	}
	m, err := steamvdf.Parse(manifest)
	if err != nil {
		return AppConfig{}, err
	}
	state := steamvdf.Obj(m, "AppState")
	if state == nil {
		return AppConfig{}, fmt.Errorf("%s has no AppState section", manifest)
	}
	cfg := AppConfig{
		AppID:    appID,
		Name:     steamvdf.Str(steamvdf.Get(state, "name")),
		Manifest: manifest,
		Branch:   readBranch(state),
		DLC:      readDLC(state),
	}
	if path, opts, ok := launchOptionsFor(roots, appID); ok {
		cfg.LaunchOptions, cfg.LocalConfig = opts, path
	}
	return cfg, nil
}

// readBranch pulls the requested and mounted branch out of an AppState.
//
// UserConfig holds what was asked for and MountedConfig what is installed;
// Steam has spelled the key both "BetaKey" and "betakey" over the years.
func readBranch(state map[string]any) Branch {
	return Branch{
		Requested: steamvdf.Str(steamvdf.Get(steamvdf.Obj(state, keyOf(state, "UserConfig")), "BetaKey")),
		Mounted:   steamvdf.Str(steamvdf.Get(steamvdf.Obj(state, keyOf(state, "MountedConfig")), "BetaKey")),
	}
}

func keyOf(m map[string]any, key string) string {
	k, _ := steamvdf.CaseKey(m, key)
	return k
}

// readDLC lists the DLC depots in an AppState. A DLC depot is one that names
// the AppID it belongs to; the rest are the base game's own depots.
func readDLC(state map[string]any) []DLC {
	depots := steamvdf.Obj(state, keyOf(state, "InstalledDepots"))
	if depots == nil {
		return nil
	}
	disabled := map[string]bool{}
	for _, id := range splitList(steamvdf.Str(steamvdf.Get(steamvdf.Obj(state, keyOf(state, "UserConfig")), "DisabledDLC"))) {
		disabled[id] = true
	}
	var out []DLC
	for depot, v := range depots {
		entry, _ := v.(map[string]any)
		id := steamvdf.Str(steamvdf.Get(entry, "dlcappid"))
		if id == "" {
			continue
		}
		out = append(out, DLC{
			AppID:   id,
			Depot:   depot,
			Size:    steamvdf.Atoi64(steamvdf.Get(entry, "size")),
			Enabled: !disabled[id],
		})
	}
	sort.Slice(out, func(i, j int) bool {
		a, ea := strconv.Atoi(out[i].AppID)
		b, eb := strconv.Atoi(out[j].AppID)
		if ea == nil && eb == nil {
			return a < b
		}
		return out[i].AppID < out[j].AppID
	})
	return out
}

// splitList parses the comma-separated AppID lists Steam uses for DisabledDLC.
func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// SetBranch records a beta branch request for an app, or returns to the public
// branch when branch is empty.
//
// This only writes the request. The manifest's BetaKey is a record of what
// Steam was told to fetch, not an instruction that moves files, so the app
// stays on its current build until Steam or SteamCMD updates it.
func SetBranch(roots []string, appID, branch string) (manifest string, err error) {
	if strings.ContainsAny(branch, "\"\\\n\r") {
		return "", fmt.Errorf("branch name contains characters KeyValues cannot hold: %q", branch)
	}
	return editManifest(roots, appID, func(state map[string]any) error {
		user := steamvdf.Section(state, keyOf(state, "UserConfig"))
		if branch == "" {
			steamvdf.Delete(user, "BetaKey")
		} else {
			steamvdf.Set(user, "BetaKey", branch)
		}
		return nil
	})
}

// SetDLCEnabled ticks or unticks one DLC for an installed app.
//
// The client records unticked DLC as a comma-separated DisabledDLC list in the
// manifest's UserConfig. Steam re-reads the manifest when it next starts, so
// the client must not be running for this to stick.
func SetDLCEnabled(roots []string, appID, dlcAppID string, enabled bool) (manifest string, err error) {
	if _, e := strconv.ParseUint(dlcAppID, 10, 64); e != nil {
		return "", fmt.Errorf("DLC AppID must be a number, got %q", dlcAppID)
	}
	return editManifest(roots, appID, func(state map[string]any) error {
		known := false
		for _, d := range readDLC(state) {
			if d.AppID == dlcAppID {
				known = true
				break
			}
		}
		if !known {
			return fmt.Errorf("app %s has no installed DLC with AppID %s", appID, dlcAppID)
		}
		user := steamvdf.Section(state, keyOf(state, "UserConfig"))
		var kept []string
		for _, id := range splitList(steamvdf.Str(steamvdf.Get(user, "DisabledDLC"))) {
			if id != dlcAppID {
				kept = append(kept, id)
			}
		}
		if !enabled {
			kept = append(kept, dlcAppID)
		}
		sort.Strings(kept)
		if len(kept) == 0 {
			steamvdf.Delete(user, "DisabledDLC")
		} else {
			steamvdf.Set(user, "DisabledDLC", strings.Join(kept, ","))
		}
		return nil
	})
}

func editManifest(roots []string, appID string, edit func(state map[string]any) error) (string, error) {
	manifest, err := FindManifest(roots, appID)
	if err != nil {
		return "", err
	}
	m, err := steamvdf.Parse(manifest)
	if err != nil {
		return manifest, err
	}
	key, ok := steamvdf.CaseKey(m, "AppState")
	if !ok {
		return manifest, fmt.Errorf("%s has no AppState section", manifest)
	}
	state, _ := m[key].(map[string]any)
	if state == nil {
		return manifest, fmt.Errorf("%s has an unreadable AppState section", manifest)
	}
	if err := edit(state); err != nil {
		return manifest, err
	}
	return manifest, steamvdf.Write(manifest, m)
}

// appsSection returns the per-app section of a parsed localconfig.vdf, which
// Steam has spelled both "apps" and "Apps".
func appsSection(m map[string]any, create bool) map[string]any {
	store := steamvdf.Obj(m, "UserLocalConfigStore")
	if store == nil {
		store = m
	}
	steam := steamvdf.Obj(store, caseChain(store, "Software", "Valve", "Steam")...)
	if steam == nil {
		if !create {
			return nil
		}
		steam = steamvdf.Section(store, "Software", "Valve", "Steam")
	}
	key, ok := steamvdf.CaseKey(steam, "apps")
	if !ok && !create {
		return nil
	}
	apps, _ := steam[key].(map[string]any)
	if apps == nil {
		apps = map[string]any{}
		steam[key] = apps
	}
	return apps
}

// LocalConfigFiles returns every user's localconfig.vdf under the given roots.
func LocalConfigFiles(roots []string) []string {
	if len(roots) == 0 {
		roots = Defaults()
	}
	var out []string
	for _, root := range roots {
		matches, err := filepath.Glob(filepath.Join(root, "userdata", "*", "config", "localconfig.vdf"))
		if err != nil {
			continue
		}
		out = append(out, matches...)
	}
	sort.Strings(out)
	return out
}

// launchOptionsFor finds the launch options set for an app, and the file they
// came from.
func launchOptionsFor(roots []string, appID string) (string, string, bool) {
	for _, path := range LocalConfigFiles(roots) {
		m, err := steamvdf.Parse(path)
		if err != nil {
			continue
		}
		apps := appsSection(m, false)
		if apps == nil {
			continue
		}
		key, ok := steamvdf.CaseKey(apps, appID)
		if !ok {
			continue
		}
		entry, _ := apps[key].(map[string]any)
		if opts := steamvdf.Str(steamvdf.Get(entry, "LaunchOptions")); opts != "" {
			return path, opts, true
		}
	}
	return "", "", false
}

// SetLaunchOptions writes an app's launch options, or clears them when opts is
// empty.
//
// accountID selects whose localconfig.vdf to write when more than one Steam
// account has signed in on this machine; when it is empty and exactly one
// account exists, that one is used.
func SetLaunchOptions(roots []string, accountID, appID, opts string) (path string, err error) {
	if _, e := strconv.ParseUint(appID, 10, 64); e != nil {
		return "", fmt.Errorf("AppID must be a number, got %q", appID)
	}
	if strings.ContainsAny(opts, "\n\r") {
		return "", fmt.Errorf("launch options cannot contain newlines")
	}
	files := LocalConfigFiles(roots)
	if accountID != "" {
		var matched []string
		for _, f := range files {
			if filepath.Base(filepath.Dir(filepath.Dir(f))) == accountID {
				matched = append(matched, f)
			}
		}
		if len(matched) == 0 {
			return "", fmt.Errorf("no localconfig.vdf for account %s", accountID)
		}
		files = matched
	}
	switch len(files) {
	case 0:
		return "", fmt.Errorf("no localconfig.vdf found; sign in to the desktop client at least once")
	case 1:
	default:
		var ids []string
		for _, f := range files {
			ids = append(ids, filepath.Base(filepath.Dir(filepath.Dir(f))))
		}
		return "", fmt.Errorf("several accounts have signed in on this machine (%s); pass --account to choose one", strings.Join(ids, ", "))
	}

	path = files[0]
	m, err := steamvdf.Parse(path)
	if err != nil {
		return path, err
	}
	apps := appsSection(m, true)
	key, _ := steamvdf.CaseKey(apps, appID)
	entry, _ := apps[key].(map[string]any)
	if entry == nil {
		entry = map[string]any{}
		apps[key] = entry
	}
	if opts == "" {
		steamvdf.Delete(entry, "LaunchOptions")
	} else {
		steamvdf.Set(entry, "LaunchOptions", opts)
	}
	return path, steamvdf.Write(path, m)
}

// AccountIDs lists the Steam account IDs with a localconfig.vdf under the roots.
func AccountIDs(roots []string) []string {
	var out []string
	for _, f := range LocalConfigFiles(roots) {
		out = append(out, filepath.Base(filepath.Dir(filepath.Dir(f))))
	}
	return out
}

// SteamRunning reports whether a desktop Steam client is running, which matters
// because it owns these files and rewrites them from memory when it exits.
func SteamRunning() bool {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		b, err := os.ReadFile(filepath.Join("/proc", e.Name(), "comm"))
		if err != nil {
			continue
		}
		if strings.TrimSpace(string(b)) == "steam" {
			return true
		}
	}
	return false
}
