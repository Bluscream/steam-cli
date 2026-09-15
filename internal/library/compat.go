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

// CompatTool is one compatibility tool the client can be told to run a game
// under.
type CompatTool struct {
	// Name is the internal name, which is the string CompatToolMapping wants.
	Name string `json:"name"`
	// DisplayName is what the client's dropdown shows, when it is known.
	DisplayName string `json:"display_name,omitempty"`
	// Source records where this tool was found: "custom" for
	// compatibilitytools.d, "steam" for one installed as an app, and "in-use"
	// for a name that only appears in the existing mapping.
	Source string `json:"source"`
	Path   string `json:"path,omitempty"`
	AppID  string `json:"appid,omitempty"`
	// InUse counts the apps currently mapped to this tool.
	InUse int `json:"in_use"`
	// Verified is true when Name came from an installed manifest. Existing
	// mappings alone do not establish that a tool is available.
	Verified bool `json:"verified"`
}

// AvailableCompatTools lists every compatibility tool the client could be set
// to use, across the given Steam roots.
//
// Three sources are merged. Tools dropped into compatibilitytools.d carry their
// own manifest and so name themselves. Valve's Proton builds are installed as
// ordinary apps and are recognised by a toolmanifest.vdf declaring the "proton"
// compatibility layer. Finally, any name already present in CompatToolMapping
// is reported as configured, without claiming its tool is available on disk.
func AvailableCompatTools(roots []string) []CompatTool {
	if len(roots) == 0 {
		roots = Defaults()
	}
	byName := map[string]*CompatTool{}
	var unnamed []CompatTool

	add := func(t CompatTool) {
		if t.Name == "" {
			unnamed = append(unnamed, t)
			return
		}
		if prev, ok := byName[t.Name]; ok {
			// A tool found on disk outranks one known only from the mapping.
			if prev.Source == "in-use" && t.Source != "in-use" {
				t.InUse = prev.InUse
				*prev = t
			}
			return
		}
		c := t
		byName[t.Name] = &c
	}

	for _, root := range roots {
		for _, t := range customCompatTools(filepath.Join(root, "compatibilitytools.d")) {
			add(t)
		}
	}
	for _, t := range installedCompatTools(roots) {
		add(t)
	}

	for name, count := range compatToolUsage(roots) {
		if prev, ok := byName[name]; ok {
			prev.InUse = count
			continue
		}
		add(CompatTool{Name: name, Source: "in-use", InUse: count, Verified: false})
	}

	out := make([]CompatTool, 0, len(byName)+len(unnamed))
	for _, t := range byName {
		out = append(out, *t)
	}
	out = append(out, unnamed...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Source != out[j].Source {
			return out[i].Source < out[j].Source
		}
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Path < out[j].Path
	})
	return out
}

// customCompatTools reads the manifests under a compatibilitytools.d directory.
// A tool may be a subdirectory holding compatibilitytool.vdf, or the manifest
// may sit directly in the directory.
func customCompatTools(dir string) []CompatTool {
	var out []CompatTool
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		if st, err := os.Stat(path); err == nil && st.IsDir() {
			path = filepath.Join(path, "compatibilitytool.vdf")
		} else if !strings.EqualFold(filepath.Ext(e.Name()), ".vdf") {
			continue
		}
		m, err := steamvdf.Parse(path)
		if err != nil {
			continue
		}
		tools := steamvdf.Obj(m, "compatibilitytools", "compat_tools")
		if tools == nil {
			continue
		}
		for name, v := range tools {
			entry, _ := v.(map[string]any)
			out = append(out, CompatTool{
				Name:        name,
				DisplayName: steamvdf.Str(steamvdf.Get(entry, "display_name")),
				Source:      "custom",
				Path:        filepath.Dir(path),
				Verified:    true,
			})
		}
	}
	return out
}

// installedCompatTools finds Valve's Proton builds, which ship as apps whose
// install directory contains a toolmanifest.vdf.
//
// The manifest's compatmanager_layer_name distinguishes a tool the user can
// select ("proton") from a container runtime that Proton pulls in as a
// dependency and that never appears in the client's dropdown.
func installedCompatTools(roots []string) []CompatTool {
	report, err := Scan(roots)
	if err != nil {
		return nil
	}
	var out []CompatTool
	for _, app := range report.Apps {
		manifest := filepath.Join(app.InstallDir, "toolmanifest.vdf")
		m, err := steamvdf.Parse(manifest)
		if err != nil {
			continue
		}
		layer := steamvdf.Str(steamvdf.Get(steamvdf.Obj(m, "manifest"), "compatmanager_layer_name"))
		if layer != "proton" {
			continue
		}
		name, known := "", false
		for _, tool := range customCompatTools(app.InstallDir) {
			out = append(out, CompatTool{Name: tool.Name, DisplayName: tool.DisplayName, Source: "steam", Path: app.InstallDir, AppID: app.AppID, Verified: true})
			known = true
		}
		if known {
			continue
		}
		out = append(out, CompatTool{
			Name:        name,
			DisplayName: app.Name,
			Source:      "steam",
			Path:        app.InstallDir,
			AppID:       app.AppID,
			Verified:    known,
		})
	}
	return out
}

// compatToolUsage counts how many apps each tool name is mapped to.
func compatToolUsage(roots []string) map[string]int {
	out := map[string]int{}
	for _, name := range ScanCompatTools(roots) {
		out[name]++
	}
	if name := GlobalCompatTool(roots); name != "" {
		out[name]++
	}
	return out
}

// compatMapping returns the parsed config.vdf and its CompatToolMapping section
// for a single root, creating the section if a caller intends to write.
func compatMapping(root string, create bool) (string, map[string]any, map[string]any, error) {
	path := filepath.Join(root, "config", "config.vdf")
	m, err := steamvdf.Parse(path)
	if err != nil {
		return path, nil, nil, err
	}
	store := steamvdf.Obj(m, "InstallConfigStore")
	if store == nil {
		store = m
	}
	steam := steamvdf.Obj(store, "Software", "Valve", "Steam")
	if steam == nil {
		// Capitalisation of this chain has varied; fall back to a scan.
		steam = steamvdf.Obj(store, caseChain(store, "Software", "Valve", "Steam")...)
	}
	if steam == nil {
		if !create {
			return path, m, nil, nil
		}
		steam = steamvdf.Section(store, caseChain(store, "Software", "Valve", "Steam")...)
	}
	key, ok := steamvdf.CaseKey(steam, "CompatToolMapping")
	if !ok && !create {
		return path, m, nil, nil
	}
	mapping, _ := steam[key].(map[string]any)
	if mapping == nil {
		mapping = map[string]any{}
		steam[key] = mapping
	}
	return path, m, mapping, nil
}

func caseChain(m map[string]any, path ...string) []string {
	out := make([]string, 0, len(path))
	for _, k := range path {
		if m == nil {
			return append(out, path[len(out):]...)
		}
		actual, ok := steamvdf.CaseKey(m, k)
		if !ok {
			return append(out, path[len(out):]...)
		}
		out = append(out, actual)
		m, _ = m[actual].(map[string]any)
	}
	return out
}

// GlobalCompatTool returns the tool applied to every title that does not have
// its own mapping, which the client stores under AppID 0.
func GlobalCompatTool(roots []string) string {
	if len(roots) == 0 {
		roots = Defaults()
	}
	for _, root := range roots {
		_, _, mapping, err := compatMapping(root, false)
		if err != nil || mapping == nil {
			continue
		}
		entry, _ := mapping["0"].(map[string]any)
		if name := steamvdf.Str(steamvdf.Get(entry, "name")); name != "" {
			return name
		}
	}
	return ""
}

// SetCompatTool points one AppID at a compatibility tool, or clears the
// mapping when tool is empty. Pass "0" to set the global default.
//
// The tool name is not validated against what is installed: a name may be
// legitimately absent from disk (a shared library, a tool installed after this
// call). Callers that want a check should consult AvailableCompatTools and warn.
func SetCompatTool(roots []string, appID, tool string) (path string, err error) {
	if _, e := strconv.ParseUint(appID, 10, 64); e != nil {
		return "", fmt.Errorf("AppID must be a number, got %q", appID)
	}
	if strings.ContainsAny(tool, "\"\\\n\r") {
		return "", fmt.Errorf("tool name contains characters KeyValues cannot hold: %q", tool)
	}
	if len(roots) == 0 {
		roots = Defaults()
	}
	root, err := configRoot(roots)
	if err != nil {
		return "", err
	}
	unlock, err := steamvdf.Lock(filepath.Join(root, "config", "config.vdf"))
	if err != nil {
		return "", err
	}
	defer unlock()
	path, m, mapping, err := compatMapping(root, true)
	if err != nil {
		return path, err
	}
	if tool == "" {
		key, ok := steamvdf.CaseKey(mapping, appID)
		if !ok {
			return path, nil // already unset; nothing to write
		}
		delete(mapping, key)
	} else {
		key, _ := steamvdf.CaseKey(mapping, appID)
		entry, _ := mapping[key].(map[string]any)
		if entry == nil {
			// priority 250 is what the client writes for a per-title choice.
			entry = map[string]any{"config": "", "priority": "250"}
			mapping[key] = entry
		}
		steamvdf.Set(entry, "name", tool)
	}
	return path, steamvdf.Write(path, m)
}

// configRoot picks the Steam installation that owns config/config.vdf. Only one
// installation is ever the live one, and writing to a second copy would have no
// effect the user could see.
func configRoot(roots []string) (string, error) {
	var files []string
	for _, root := range roots {
		p := filepath.Join(root, "config", "config.vdf")
		if _, err := os.Stat(p); err == nil {
			files = append(files, p)
		}
	}
	files = uniqueFiles(files)
	if len(files) != 1 {
		return "", fmt.Errorf("expected one Steam config, found %d; pass --root /path/to/Steam", len(files))
	}
	return filepath.Dir(filepath.Dir(files[0])), nil
}
