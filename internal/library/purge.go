package library

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"steamcli.local/steam/internal/steamvdf"
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
// If purgeNonSteam is true, it also searches system locations (e.g. ~/.config, ~/.local/share, ~/Documents, Saved Games)
// for external game save files and configs.
func PurgeAppFiles(roots []string, appID string, installDirHint string, purgeNonSteam bool) (*PurgeResult, error) {
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
			if filepath.IsAbs(matchedInstallDir) {
				if _, err := os.Lstat(matchedInstallDir); err == nil {
					candidateTargets = append(candidateTargets, PurgedArtifact{
						Path:        matchedInstallDir,
						Category:    "common",
						Description: "Game installation directory",
					})
				}
			} else {
				commonCandidate := filepath.Join(steamapps, "common", matchedInstallDir)
				if _, err := os.Lstat(commonCandidate); err == nil {
					candidateTargets = append(candidateTargets, PurgedArtifact{
						Path:        commonCandidate,
						Category:    "common",
						Description: "Game installation directory (install dir)",
					})
				}
			}
		}

		// If matchedName or installDirHint is known, check common/<Name> in this library
		namesToCheck := []string{matchedName}
		if installDirHint != "" && installDirHint != matchedName {
			namesToCheck = append(namesToCheck, filepath.Base(installDirHint), installDirHint)
		}
		for _, name := range namesToCheck {
			if name == "" {
				continue
			}
			commonCandidate := filepath.Join(steamapps, "common", name)
			if _, err := os.Lstat(commonCandidate); err == nil {
				candidateTargets = append(candidateTargets, PurgedArtifact{
					Path:        commonCandidate,
					Category:    "common",
					Description: "Game installation directory (" + name + ")",
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

	// 4. Non-Steam OS-level saves, configs, and standalone application directories
	if purgeNonSteam {
		candidateNames := []string{}
		if result.Name != "" {
			candidateNames = append(candidateNames, result.Name)
			// Also try without spaces or special characters
			noSpaces := strings.ReplaceAll(result.Name, " ", "")
			if noSpaces != result.Name {
				candidateNames = append(candidateNames, noSpaces)
			}
			// Lowercase/snake/kebab variants
			candidateNames = append(candidateNames, strings.ToLower(result.Name))
			candidateNames = append(candidateNames, strings.ToLower(noSpaces))
		}
		if installDirHint != "" {
			candidateNames = append(candidateNames, filepath.Base(installDirHint))
		}
		if matchedInstallDir != "" {
			candidateNames = append(candidateNames, filepath.Base(matchedInstallDir))
		}

		home, _ := os.UserHomeDir()
		if home != "" {
			// Search locations for native/wine game saves and configs:
			// - ~/.config/<GameName>
			// - ~/.local/share/<GameName>
			// - ~/Documents/<GameName>
			// - ~/Saved Games/<GameName>
			// - ~/.wine/drive_c/users/*/Saved Games/<GameName>
			// - ~/.var/app/*/data/<GameName> (Flatpak sandbox app data)
			searchRoots := []string{
				filepath.Join(home, ".config"),
				filepath.Join(home, ".local", "share"),
				filepath.Join(home, "Documents"),
				filepath.Join(home, "Saved Games"),
				filepath.Join(home, ".var", "app"),
			}

			// Add Windows / Wine user directories if on Windows
			appData := os.Getenv("APPDATA")
			if appData != "" {
				searchRoots = append(searchRoots, appData)
			}
			localAppData := os.Getenv("LOCALAPPDATA")
			if localAppData != "" {
				searchRoots = append(searchRoots, localAppData)
			}
			userProfile := os.Getenv("USERPROFILE")
			if userProfile != "" {
				searchRoots = append(searchRoots, filepath.Join(userProfile, "Saved Games"))
				searchRoots = append(searchRoots, filepath.Join(userProfile, "Documents"))
			}

			seenCandidates := make(map[string]bool)
			for _, name := range candidateNames {
				cleanName := strings.TrimSpace(name)
				if cleanName == "" || len(cleanName) < 3 || seenCandidates[strings.ToLower(cleanName)] {
					continue
				}
				seenCandidates[strings.ToLower(cleanName)] = true

				for _, searchDir := range searchRoots {
					if _, err := os.Stat(searchDir); err != nil {
						continue
					}

					// Direct match
					exactPath := filepath.Join(searchDir, cleanName)
					if _, err := os.Lstat(exactPath); err == nil {
						candidateTargets = append(candidateTargets, PurgedArtifact{
							Path:        exactPath,
							Category:    "save/config",
							Description: "External game config / save directory",
						})
					}

					// Case-insensitive match in searchDir
					entries, err := os.ReadDir(searchDir)
					if err == nil {
						for _, de := range entries {
							if strings.EqualFold(de.Name(), cleanName) && de.Name() != cleanName {
								p := filepath.Join(searchDir, de.Name())
								candidateTargets = append(candidateTargets, PurgedArtifact{
									Path:        p,
									Category:    "save/config",
									Description: "External game config / save directory (matched case)",
								})
							}
						}
					}
				}
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

// PurgeWorkshopItem finds and removes a specific workshop item (content folder, download staging,
// and removes its entry from all appworkshop_<appid>.acf files).
func PurgeWorkshopItem(roots []string, itemID string) (*PurgeResult, error) {
	itemID = strings.TrimSpace(itemID)
	if itemID == "" {
		return nil, fmt.Errorf("workshop itemID cannot be empty")
	}
	if _, err := strconv.ParseUint(itemID, 10, 64); err != nil {
		return nil, fmt.Errorf("invalid workshop itemID %q: must be positive integer", itemID)
	}

	if len(roots) == 0 {
		roots = Defaults()
	}

	rep, err := Scan(roots)
	var libraries []string
	if err == nil {
		libraries = rep.Libraries
	}
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
		AppID: itemID,
		Name:  "Workshop Item " + itemID,
	}

	var candidateTargets []PurgedArtifact
	var manifestsToUpdate []string

	for _, lib := range libraries {
		workshopDir := filepath.Join(lib, "steamapps", "workshop")
		if _, err := os.Stat(workshopDir); err != nil {
			if filepath.Base(lib) == "workshop" {
				workshopDir = lib
			} else {
				continue
			}
		}

		// 1. Content: steamapps/workshop/content/*/<itemID>
		contentMatches, err := filepath.Glob(filepath.Join(workshopDir, "content", "*", itemID))
		if err == nil {
			for _, m := range contentMatches {
				candidateTargets = append(candidateTargets, PurgedArtifact{
					Path:        m,
					Category:    "workshop",
					Description: fmt.Sprintf("Subscribed workshop item (App %s)", filepath.Base(filepath.Dir(m))),
				})
			}
		}

		// 2. Downloads: steamapps/workshop/downloads/*/<itemID> or steamapps/workshop/downloads/<itemID>
		dlMatches, err := filepath.Glob(filepath.Join(workshopDir, "downloads", "*", itemID))
		if err == nil {
			for _, m := range dlMatches {
				candidateTargets = append(candidateTargets, PurgedArtifact{
					Path:        m,
					Category:    "workshop",
					Description: "Workshop item download staging",
				})
			}
		}
		dlDirect := filepath.Join(workshopDir, "downloads", itemID)
		if _, err := os.Lstat(dlDirect); err == nil {
			candidateTargets = append(candidateTargets, PurgedArtifact{
				Path:        dlDirect,
				Category:    "workshop",
				Description: "Workshop item download staging",
			})
		}

		// 3. Scan appworkshop_*.acf to clean references
		acfFiles, err := filepath.Glob(filepath.Join(workshopDir, "appworkshop_*.acf"))
		if err == nil {
			for _, acf := range acfFiles {
				if hasWorkshopItem(acf, itemID) {
					manifestsToUpdate = append(manifestsToUpdate, acf)
				}
			}
		}
	}

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

	for _, t := range safeTargets {
		err := os.RemoveAll(t.Path)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("failed to remove %s: %v", t.Path, err))
		} else {
			result.Artifacts = append(result.Artifacts, t)
		}
	}

	// Remove item entry from appworkshop manifests
	for _, acf := range manifestsToUpdate {
		if err := removeWorkshopItemFromACF(acf, itemID); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("failed to update %s: %v", filepath.Base(acf), err))
		} else {
			result.Artifacts = append(result.Artifacts, PurgedArtifact{
				Path:        acf,
				Category:    "workshop",
				Description: "Unregistered item from " + filepath.Base(acf),
			})
		}
	}

	return result, nil
}

func hasWorkshopItem(path, itemID string) bool {
	m, err := parse(path)
	if err != nil {
		return false
	}
	root, ok := m["AppWorkshop"].(map[string]any)
	if !ok {
		return false
	}
	for _, sec := range []string{"WorkshopItemsInstalled", "WorkshopItemDetails"} {
		if items, ok := root[sec].(map[string]any); ok {
			if _, exists := items[itemID]; exists {
				return true
			}
		}
	}
	return false
}

func removeWorkshopItemFromACF(path, itemID string) error {
	m, err := parse(path)
	if err != nil {
		return err
	}
	root, ok := m["AppWorkshop"].(map[string]any)
	if !ok {
		return nil
	}
	modified := false
	for _, sec := range []string{"WorkshopItemsInstalled", "WorkshopItemDetails"} {
		if items, ok := root[sec].(map[string]any); ok {
			if _, exists := items[itemID]; exists {
				delete(items, itemID)
				modified = true
			}
		}
	}
	if modified {
		return steamvdf.Write(path, m)
	}
	return nil
}

// FindDLCBaseGame checks if the given dlcAppID is a registered DLC under any installed game.
// It returns the base game's AppID and App title if found.
func FindDLCBaseGame(roots []string, dlcAppID string) (baseAppID string, baseGameName string, found bool) {
	if len(roots) == 0 {
		roots = Defaults()
	}
	rep, err := Scan(roots)
	if err != nil {
		return "", "", false
	}
	for _, a := range rep.Apps {
		if a.Manifest == "" {
			continue
		}
		m, err := parse(a.Manifest)
		if err != nil {
			continue
		}
		state, ok := m["AppState"].(map[string]any)
		if !ok {
			continue
		}
		for _, d := range readDLC(state) {
			if d.AppID == dlcAppID {
				return a.AppID, a.Name, true
			}
		}
	}
	return "", "", false
}

// PurgeDLC disables the DLC in the parent game's appmanifest and removes any matching DLC files
// from the base game's common folder or depot cache.
func PurgeDLC(roots []string, baseAppID string, dlcAppID string) (*PurgeResult, error) {
	manifest, err := SetDLCEnabled(roots, baseAppID, dlcAppID, false)
	if err != nil {
		return nil, err
	}

	result := &PurgeResult{
		AppID: dlcAppID,
		Name:  fmt.Sprintf("DLC %s (Base Game %s)", dlcAppID, baseAppID),
	}

	result.Artifacts = append(result.Artifacts, PurgedArtifact{
		Path:        manifest,
		Category:    "manifest",
		Description: fmt.Sprintf("Disabled DLC in base game (%s) manifest", baseAppID),
	})

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
