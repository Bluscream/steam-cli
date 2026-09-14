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
	AppID      string `json:"appid"`
	Name       string `json:"name"`
	InstallDir string `json:"install_dir"`
	Library    string `json:"library"`
	Manifest   string `json:"manifest"`
	StateFlags string `json:"state_flags"`
	SizeOnDisk string `json:"size_on_disk,omitempty"`
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
			r.Apps = append(r.Apps, App{str(a["appid"]), str(a["name"]), filepath.Join(lib, "steamapps", "common", install), lib, path, str(a["StateFlags"]), str(a["SizeOnDisk"])})
		}
	}
	sort.Slice(r.Apps, func(i, j int) bool { return r.Apps[i].AppID < r.Apps[j].AppID })
	return r, nil
}
