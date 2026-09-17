package library

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// PurgedArtifact represents a specific file or directory removed during uninstallation.
type PurgedArtifact struct {
	Path        string `json:"path"`
	Category    string `json:"category"` // "common", "manifest", "compatdata", "shadercache", "downloading", "workshop", "userdata", "cache"
	BytesFreed  int64  `json:"bytes_freed"`
	Description string `json:"description"`
}

// PurgeResult summarizes the outcome of a forced purge of an app.
type PurgeResult struct {
	AppID          string           `json:"appid"`
	Name           string           `json:"name,omitempty"`
	Artifacts      []PurgedArtifact `json:"artifacts"`
	TotalBytes     int64            `json:"total_bytes"`
	TotalFiles     int              `json:"total_files"`
	Errors         []string         `json:"errors,omitempty"`
	ClientNotified bool             `json:"client_notified"`
}

// calculateDirSize computes the total byte size and file count of a file or directory tree.
func calculateDirSize(path string) (int64, int) {
	fi, err := os.Lstat(path)
	if err != nil {
		return 0, 0
	}
	if !fi.IsDir() {
		return fi.Size(), 1
	}

	var size int64
	var count int
	_ = filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err == nil {
			count++
			if !info.IsDir() {
				size += info.Size()
			}
		}
		return nil
	})
	return size, count
}

// PurgeAppFiles thoroughly finds and removes all leftover files, directories, prefixes,
// caches, downloads, workshop items, and manifests associated with appID across all libraries and roots.
func PurgeAppFiles(roots []string, appID string, installDirHint string) (*PurgeResult, error) {
	appID = strings.TrimSpace(appID)
	if appID == "" {
		return nil, fmt.Errorf("appID cannot be empty")
	}
	if _, err := strconv.ParseUint(appID, 10, 64); err != nil {
		return nil, fmt.Errorf("invalid appID %q: must be positive integer", appID)
	}

	if len(roots) == 0 {
		roots = Defaults()
	}

	rep, err := Scan(roots)
	var libraries []string
	if err == nil {
		libraries = rep.Libraries
	}
	// Always include the roots themselves as candidate libraries/steam homes
	seenLibs := make(map[string]bool)
	for _, l := range libraries {
		clean := filepath.Clean(l)
		seenLibs[clean] = true
		libraries = append(libraries, clean)
	}
	for _, r := range roots {
		clean := filepath.Clean(r)
		if !seenLibs[clean] {
			seenLibs[clean] = true
			libraries = append(libraries, clean)
		}
	}

	result := &PurgeResult{
		AppID: appID,
	}

	var candidateTargets []PurgedArtifact

	// 1. Check scanned installed apps to find game name and exact InstallDir
	matchedName := ""
	matchedInstallDir := ""
	if err == nil {
		for _, a := range rep.Apps {
			if a.AppID == appID {
				if matchedName == "" && a.Name != "" {
					matchedName = a.Name
				}
				if a.InstallDir != "" {
					matchedInstallDir = a.InstallDir
				}
				if a.Manifest != "" {
					candidateTargets = append(candidateTargets, PurgedArtifact{
						Path:        a.Manifest,
						Category:    "manifest",
						Description: "App manifest ACF",
					})
				}
			}
		}
	}
	result.Name = matchedName
	if installDirHint != "" && matchedInstallDir == "" {
		matchedInstallDir = installDirHint
	}

	// 2. Scan every library folder
	for _, lib := range libraries {
		steamapps := filepath.Join(lib, "steamapps")
		if _, err := os.Stat(steamapps); err != nil {
			// Maybe lib itself is steamapps?
			if filepath.Base(lib) == "steamapps" {
				steamapps = lib
			} else {
				continue
			}
		}

		// App manifest(s) and temporary manifest files
		manifestGlob := filepath.Join(steamapps, fmt.Sprintf("appmanifest_%s.acf*", appID))
		if matches, err := filepath.Glob(manifestGlob); err == nil {
			for _, m := range matches {
				candidateTargets = append(candidateTargets, PurgedArtifact{
					Path:        m,
					Category:    "manifest",
					Description: "App manifest (" + filepath.Base(m) + ")",
				})
			}
		}

		// Compatdata prefix (Proton wineprefix)
		compatPath := filepath.Join(steamapps, "compatdata", appID)
		if _, err := os.Lstat(compatPath); err == nil {
			candidateTargets = append(candidateTargets, PurgedArtifact{
				Path:        compatPath,
				Category:    "compatdata",
				Description: "Proton / Wine compatdata prefix",
			})
		}

		// Shadercache
		shaderPath := filepath.Join(steamapps, "shadercache", appID)
		if _, err := os.Lstat(shaderPath); err == nil {
			candidateTargets = append(candidateTargets, PurgedArtifact{
				Path:        shaderPath,
				Category:    "shadercache",
				Description: "Fossilize & graphics shader cache",
			})
		}

		// Downloading staging folder and delta files
		dlDir := filepath.Join(steamapps, "downloading", appID)
		if _, err := os.Lstat(dlDir); err == nil {
			candidateTargets = append(candidateTargets, PurgedArtifact{
				Path:        dlDir,
				Category:    "downloading",
				Description: "Download staging directory",
			})
		}
		// Downloading patch files: e.g. <appid>_*.patch, state_<appid>_*.patch, *.delta
		patchGlob := filepath.Join(steamapps, "downloading", fmt.Sprintf("*%s*", appID))
		if matches, err := filepath.Glob(patchGlob); err == nil {
			for _, p := range matches {
				if p != dlDir {
					candidateTargets = append(candidateTargets, PurgedArtifact{
						Path:        p,
						Category:    "downloading",
						Description: "Download patch artifact (" + filepath.Base(p) + ")",
					})
				}
			}
		}

		// Workshop items content & downloads
		wsContent := filepath.Join(steamapps, "workshop", "content", appID)
		if _, err := os.Lstat(wsContent); err == nil {
			candidateTargets = append(candidateTargets, PurgedArtifact{
				Path:        wsContent,
				Category:    "workshop",
				Description: "Subscribed workshop content",
			})
		}
		wsDownloads := filepath.Join(steamapps, "workshop", "downloads", appID)
		if _, err := os.Lstat(wsDownloads); err == nil {
			candidateTargets = append(candidateTargets, PurgedArtifact{
				Path:        wsDownloads,
				Category:    "workshop",
				Description: "Workshop download staging",
			})
		}
		// Workshop manifest: appworkshop_<appid>.acf
		wsManifest := filepath.Join(steamapps, "workshop", fmt.Sprintf("appworkshop_%s.acf", appID))
		if _, err := os.Lstat(wsManifest); err == nil {
			candidateTargets = append(candidateTargets, PurgedArtifact{
				Path:        wsManifest,
				Category:    "workshop",
				Description: "Workshop app manifest",
			})
		}

		// Temp folder artifacts
		tempGlob := filepath.Join(steamapps, "temp", fmt.Sprintf("*%s*", appID))
		if matches, err := filepath.Glob(tempGlob); err == nil {
			for _, t := range matches {
				candidateTargets = append(candidateTargets, PurgedArtifact{
					Path:        t,
					Category:    "temp",
					Description: "Temporary update files",
				})
			}
		}

		// Common install dir:
		// If matchedInstallDir is known, add it
		if matchedInstallDir != "" {
			if _, err := os.Lstat(matchedInstallDir); err == nil {
				candidateTargets = append(candidateTargets, PurgedArtifact{
					Path:        matchedInstallDir,
					Category:    "common",
					Description: "Game installation directory",
				})
			}
		}

		// If matchedName is known and no install dir yet, check common/<Name>
		if matchedName != "" {
			commonCandidate := filepath.Join(steamapps, "common", matchedName)
			if _, err := os.Lstat(commonCandidate); err == nil {
				candidateTargets = append(candidateTargets, PurgedArtifact{
					Path:        commonCandidate,
					Category:    "common",
					Description: "Game installation directory (matched name)",
				})
			}
		}
	}

	// 3. Check Steam root directories for userdata, appcache, and librarycache
	for _, root := range roots {
		// userdata/<steamid>/<appid>
		userPattern := filepath.Join(root, "userdata", "*", appID)
		if matches, err := filepath.Glob(userPattern); err == nil {
			for _, u := range matches {
				candidateTargets = append(candidateTargets, PurgedArtifact{
					Path:        u,
					Category:    "userdata",
					Description: "User cloud save / screenshots cache",
				})
			}
		}

		// appcache/librarycache/<appid>*
		cachePattern := filepath.Join(root, "appcache", "librarycache", fmt.Sprintf("%s*", appID))
		if matches, err := filepath.Glob(cachePattern); err == nil {
			for _, c := range matches {
				candidateTargets = append(candidateTargets, PurgedArtifact{
					Path:        c,
					Category:    "cache",
					Description: "UI library banner & icon cache",
				})
			}
		}

		// userdata/*/config/librarycache/<appid>*
		userCachePattern := filepath.Join(root, "userdata", "*", "config", "librarycache", fmt.Sprintf("%s*", appID))
		if matches, err := filepath.Glob(userCachePattern); err == nil {
			for _, c := range matches {
				candidateTargets = append(candidateTargets, PurgedArtifact{
					Path:        c,
					Category:    "cache",
					Description: "User librarycache metadata",
				})
			}
		}
	}

	// Deduplicate targets by normalized path and apply strict safety guard
	seenPaths := make(map[string]bool)
	var safeTargets []PurgedArtifact

	for _, t := range candidateTargets {
		clean := filepath.Clean(t.Path)
		if seenPaths[clean] {
			continue
		}
		seenPaths[clean] = true

		if !isSafePurgePath(clean) {
			result.Errors = append(result.Errors, fmt.Sprintf("refusing to purge dangerous path: %s", clean))
			continue
		}

		sz, cnt := calculateDirSize(clean)
		t.Path = clean
		t.BytesFreed = sz
		result.TotalBytes += sz
		result.TotalFiles += cnt
		safeTargets = append(safeTargets, t)
	}

	// Now delete all safe targets
	for _, t := range safeTargets {
		err := os.RemoveAll(t.Path)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("failed to remove %s: %v", t.Path, err))
		} else {
			result.Artifacts = append(result.Artifacts, t)
		}
	}

	return result, nil
}

// isSafePurgePath performs sanity checks to prevent accidental deletions of critical system or library root directories.
func isSafePurgePath(p string) bool {
	p = filepath.Clean(p)
	// Must be an absolute path
	if !filepath.IsAbs(p) {
		return false
	}
	// Refuse root or home directories
	if p == "/" || p == "." || p == ".." {
		return false
	}
	home, _ := os.UserHomeDir()
	if home != "" && (p == home || p == filepath.Clean(home)) {
		return false
	}

	// Must not be a critical Steam directory itself
	base := filepath.Base(p)
	parent := filepath.Dir(p)
	parentBase := filepath.Base(parent)

	criticalDirs := map[string]bool{
		"steamapps":    true,
		"common":       true,
		"compatdata":   true,
		"shadercache":  true,
		"downloading":  true,
		"workshop":     true,
		"content":      true,
		"downloads":    true,
		"userdata":     true,
		"Steam":        true,
		"appcache":     true,
		"librarycache": true,
		"config":       true,
	}

	if criticalDirs[base] {
		return false
	}

	// If deleting something directly in "common", verify it is not common itself
	if parentBase == "common" && (base == "" || base == "." || base == "/") {
		return false
	}

	return true
}
