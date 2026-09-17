package library

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// DownloadStatus represents the state of a download or pending update.
type DownloadStatus string

const (
	StatusActive     DownloadStatus = "downloading"
	StatusStaging    DownloadStatus = "staging"
	StatusCommitting DownloadStatus = "committing"
	StatusValidating DownloadStatus = "validating"
	StatusPaused     DownloadStatus = "paused"
	StatusQueued     DownloadStatus = "queued"
	StatusScheduled  DownloadStatus = "scheduled"
	StatusCorrupt    DownloadStatus = "corrupt"
	StatusError      DownloadStatus = "error"
	StatusCompleted  DownloadStatus = "completed"
)

// DownloadItem represents a single app, workshop item, or shader cache download/update entry.
type DownloadItem struct {
	AppID           string         `json:"appid"`
	Name            string         `json:"name"`
	Type            string         `json:"type"` // "game", "workshop", "shader"
	Status          DownloadStatus `json:"status"`
	BytesDownloaded int64          `json:"bytes_downloaded"`
	BytesTotal      int64          `json:"bytes_total"`
	Progress        float64        `json:"progress"` // 0.0 - 100.0
	ScheduledTime   int64          `json:"scheduled_time,omitempty"`
	ScheduledHuman  string         `json:"scheduled_human,omitempty"`
	UpdateResult    int            `json:"update_result"`
	ErrorDetail     string         `json:"error_detail,omitempty"`
	Library         string         `json:"library,omitempty"`
	Source          string         `json:"source"` // "manifest", "downloading_dir", "workshop_acf", "content_log"
}

// DownloadsReport aggregates all discovered download items and diagnostic notes.
type DownloadsReport struct {
	Active     []DownloadItem `json:"active,omitempty"`
	Validating []DownloadItem `json:"validating,omitempty"`
	Scheduled  []DownloadItem `json:"scheduled,omitempty"`
	Paused     []DownloadItem `json:"paused,omitempty"`
	Corrupt    []DownloadItem `json:"corrupt,omitempty"`
	RecentDone []DownloadItem `json:"recent_completed,omitempty"`
	All        []DownloadItem `json:"all"`
	Warnings   []string       `json:"warnings,omitempty"`
}

// StateFlags bitmask constants from Steam appmanifest
const (
	StateUninstalled        = 1
	StateUpdateRequired     = 2
	StateFullyInstalled     = 4
	StateEncrypted          = 8
	StateLocked             = 16
	StateFilesMissing       = 32
	StateAppRunning         = 64
	StateFilesCorrupt       = 128
	StateUpdateRunning      = 256
	StateUpdatePaused       = 512
	StateUpdateStarted      = 1024
	StateUninstalling       = 2048
	StateBackupRunning      = 4096
	StateReconfiguring      = 65536
	StateValidating         = 131072
	StateAddingFiles        = 262144
	StatePreallocating      = 524288
	StateDownloading        = 1048576
	StateStaging            = 2097152
	StateCommitting         = 4194304
	StateUpdateStopping     = 8388608
)

// UpdateResult error codes from Steam
var updateResultMap = map[int]string{
	0:  "",
	1:  "Unknown Error",
	2:  "Not enough disk space",
	3:  "No internet connection",
	4:  "Disk write/IO error",
	5:  "Corrupt game files",
	6:  "Content servers unreachable",
	7:  "Timeout waiting for chunks",
	8:  "Missing configuration",
	9:  "Disk read error",
	10: "App running / locked files",
	11: "Shared library locked",
	12: "Corrupt download data",
	13: "Missing download files",
}

// ScanDownloads scans Steam libraries, manifest files, downloading directories,
// workshop metadata, and client logs to report download states.
func ScanDownloads(roots []string) (DownloadsReport, error) {
	if len(roots) == 0 {
		roots = Defaults()
	}

	rep, err := Scan(roots)
	var warnings []string
	if err != nil {
		warnings = append(warnings, err.Error())
	}
	warnings = append(warnings, rep.Warnings...)

	var items []DownloadItem
	itemMap := make(map[string]*DownloadItem) // key: type:appid

	// 1. Scan appmanifest files across all known library folders
	for _, lib := range rep.Libraries {
		steamapps := filepath.Join(lib, "steamapps")
		manifests, err := filepath.Glob(filepath.Join(steamapps, "appmanifest_*.acf"))
		if err != nil {
			continue
		}

		for _, mf := range manifests {
			m, err := parse(mf)
			if err != nil {
				continue
			}
			a, ok := m["AppState"].(map[string]interface{})
			if !ok {
				continue
			}

			appID := str(a["appid"])
			if appID == "" {
				continue
			}
			name := str(a["name"])

			var flags int64
			if fStr := str(a["StateFlags"]); fStr != "" {
				flags, _ = strconv.ParseInt(fStr, 10, 64)
			}

			var bDownloaded, bTotal int64
			if s := str(a["BytesDownloaded"]); s != "" {
				bDownloaded, _ = strconv.ParseInt(s, 10, 64)
			}
			if s := str(a["BytesToDownload"]); s != "" {
				bTotal, _ = strconv.ParseInt(s, 10, 64)
			}

			var sched int64
			if s := str(a["ScheduledAutoUpdate"]); s != "" {
				sched, _ = strconv.ParseInt(s, 10, 64)
			}

			var res int
			if s := str(a["UpdateResult"]); s != "" {
				res, _ = strconv.Atoi(s)
			}

			// Determine status
			status := StatusCompleted
			isDownloading := (flags&StateDownloading != 0) || (flags&StateUpdateRunning != 0)
			isStaging := (flags&StateStaging != 0)
			isCommitting := (flags&StateCommitting != 0)
			isPaused := (flags&StateUpdatePaused != 0)
			isCorrupt := (flags&StateFilesCorrupt != 0) || res == 5 || res == 12 || (flags&StateFilesMissing != 0)
			isError := res != 0 && !isCorrupt

			if isCorrupt {
				status = StatusCorrupt
			} else if isError {
				status = StatusError
			} else if isCommitting {
				status = StatusCommitting
			} else if isStaging {
				status = StatusStaging
			} else if isDownloading {
				status = StatusActive
			} else if isPaused {
				status = StatusPaused
			} else if sched > 0 {
				status = StatusScheduled
			} else if (flags&StateUpdateRequired != 0) || (flags&StateUpdateStarted != 0) {
				status = StatusQueued
			} else if flags == StateFullyInstalled {
				continue // fully installed, nothing pending
			}

			// If it's fully installed without updates, skip unless error
			if status == StatusCompleted && flags&StateUpdateRequired == 0 && res == 0 {
				continue
			}

			prog := 0.0
			if bTotal > 0 {
				prog = (float64(bDownloaded) / float64(bTotal)) * 100.0
			}

			schedHuman := ""
			if sched > 0 {
				t := time.Unix(sched, 0)
				schedHuman = t.Format("2006-01-02 15:04")
			}

			errDesc := ""
			if msg, ok := updateResultMap[res]; ok && msg != "" {
				errDesc = msg
			}

			it := DownloadItem{
				AppID:           appID,
				Name:            name,
				Type:            "game",
				Status:          status,
				BytesDownloaded: bDownloaded,
				BytesTotal:      bTotal,
				Progress:        prog,
				ScheduledTime:   sched,
				ScheduledHuman:  schedHuman,
				UpdateResult:    res,
				ErrorDetail:     errDesc,
				Library:         lib,
				Source:          "manifest",
			}
			itemMap["game:"+appID] = &it
		}
	}

	// 2. Scan steamapps/downloading/ and shadercache/*/downloads/ directories for active delta staging
	for _, lib := range rep.Libraries {
		dlDir := filepath.Join(lib, "steamapps", "downloading")
		entries, err := os.ReadDir(dlDir)
		if err == nil {
			for _, de := range entries {
				if !de.IsDir() {
					continue
				}
				appID := de.Name()
				if _, err := strconv.Atoi(appID); err != nil {
					continue
				}

				// Check if this downloading folder actually has files (not just an empty folder)
				appDlPath := filepath.Join(dlDir, appID)
				subEntries, err := os.ReadDir(appDlPath)
				hasFiles := (err == nil && len(subEntries) > 0)

				key := "game:" + appID
				if existing, exists := itemMap[key]; exists {
					if hasFiles && (existing.Status == StatusQueued || existing.Status == StatusCompleted) {
						existing.Status = StatusActive
					}
				} else if hasFiles {
					// Discovered in downloading/ folder with actual staging files
					name := "AppID " + appID
					for _, a := range rep.Apps {
						if a.AppID == appID {
							name = a.Name
							break
						}
					}
					itemMap[key] = &DownloadItem{
						AppID:       appID,
						Name:        name,
						Type:        "game",
						Status:      StatusActive,
						Library:     lib,
						Source:      "downloading_dir",
						ErrorDetail: "Staging in downloading directory",
					}
				}
			}
		}

		// Shadercache downloads
		scDir := filepath.Join(lib, "steamapps", "shadercache")
		scEntries, err := os.ReadDir(scDir)
		if err == nil {
			for _, sc := range scEntries {
				if !sc.IsDir() {
					continue
				}
				appID := sc.Name()
				dlPath := filepath.Join(scDir, appID, "downloads")
				if fi, err := os.Stat(dlPath); err == nil && fi.IsDir() {
					files, _ := os.ReadDir(dlPath)
					if len(files) > 0 {
						key := "shader:" + appID
						name := "AppID " + appID
						for _, a := range rep.Apps {
							if a.AppID == appID {
								name = a.Name + " (Shader Cache)"
								break
							}
						}
						itemMap[key] = &DownloadItem{
							AppID:   appID,
							Name:    name,
							Type:    "shader",
							Status:  StatusActive,
							Library: lib,
							Source:  "shadercache_downloads",
						}
					}
				}
			}
		}

		// Workshop downloads directory
		wsDlDir := filepath.Join(lib, "steamapps", "workshop", "downloads")
		if entries, err := os.ReadDir(wsDlDir); err == nil && len(entries) > 0 {
			for _, de := range entries {
				if !de.IsDir() {
					continue
				}
				appID := de.Name()
				key := "workshop_app:" + appID
				itemMap[key] = &DownloadItem{
					AppID:   appID,
					Name:    "AppID " + appID + " Workshop Items",
					Type:    "workshop",
					Status:  StatusActive,
					Library: lib,
					Source:  "workshop_downloads",
				}
			}
		}
	}

	// 3. Scan workshop manifest appworkshop_*.acf
	for _, lib := range rep.Libraries {
		wsACFs, err := filepath.Glob(filepath.Join(lib, "steamapps", "workshop", "appworkshop_*.acf"))
		if err != nil {
			continue
		}
		for _, acf := range wsACFs {
			m, err := parse(acf)
			if err != nil {
				continue
			}
			top, ok := m["AppWorkshop"].(map[string]interface{})
			if !ok {
				continue
			}
			appID := str(top["appid"])
			needsUpdate := str(top["NeedsUpdate"]) == "1"
			needsDownload := str(top["NeedsDownload"]) == "1"

			if needsUpdate || needsDownload {
				key := "workshop_app:" + appID
				status := StatusQueued
				if needsDownload {
					status = StatusActive
				}
				itemMap[key] = &DownloadItem{
					AppID:   appID,
					Name:    "AppID " + appID + " Workshop Content",
					Type:    "workshop",
					Status:  status,
					Library: lib,
					Source:  "workshop_acf",
				}
			}
		}
	}

	// 4. Parse content_log.txt and workshop_log.txt for recent errors or download state changes
	scanRecentLogs(roots, itemMap, &rep)

	// Collect into report
	for _, it := range itemMap {
		if it.Status == StatusCompleted && it.Source == "manifest" && it.UpdateResult == 0 {
			continue
		}
		items = append(items, *it)
	}

	sort.Slice(items, func(i, j int) bool {
		// Group priority: Active -> Validating -> Corrupt -> Error -> Paused -> Queued -> Scheduled -> Completed
		pI := statusPriority(items[i].Status)
		pJ := statusPriority(items[j].Status)
		if pI != pJ {
			return pI < pJ
		}
		return items[i].AppID < items[j].AppID
	})

	repOut := DownloadsReport{
		All:      items,
		Warnings: warnings,
	}
	for _, it := range items {
		switch it.Status {
		case StatusActive, StatusStaging, StatusCommitting:
			repOut.Active = append(repOut.Active, it)
		case StatusValidating:
			repOut.Validating = append(repOut.Validating, it)
		case StatusScheduled:
			repOut.Scheduled = append(repOut.Scheduled, it)
		case StatusPaused:
			repOut.Paused = append(repOut.Paused, it)
		case StatusCorrupt, StatusError:
			repOut.Corrupt = append(repOut.Corrupt, it)
		case StatusCompleted:
			repOut.RecentDone = append(repOut.RecentDone, it)
		}
	}

	return repOut, nil
}

func statusPriority(s DownloadStatus) int {
	switch s {
	case StatusActive:
		return 1
	case StatusValidating:
		return 2
	case StatusStaging:
		return 3
	case StatusCommitting:
		return 4
	case StatusCorrupt:
		return 5
	case StatusError:
		return 6
	case StatusPaused:
		return 7
	case StatusQueued:
		return 8
	case StatusScheduled:
		return 9
	case StatusCompleted:
		return 10
	default:
		return 11
	}
}

var (
	rxContentError       = regexp.MustCompile(`\[([^\]]+)\] AppID (\d+) update canceled : (.*)`)
	rxContentState       = regexp.MustCompile(`\[([^\]]+)\] AppID (\d+) state changed : (.*)`)
	rxContentUpdatePhase = regexp.MustCompile(`\[([^\]]+)\] AppID (\d+)(?: (App|Workshop|Shader))? update changed : (.*)`)
)

func scanRecentLogs(roots []string, itemMap map[string]*DownloadItem, rep *Report) {
	for _, root := range roots {
		contentLog := filepath.Join(root, "logs", "content_log.txt")
		f, err := os.Open(contentLog)
		if err != nil {
			continue
		}
		defer f.Close()

		// Read last 300 lines
		lines := tailLines(f, 300)
		for _, line := range lines {
			if m := rxContentError.FindStringSubmatch(line); m != nil {
				appID := m[2]
				reason := strings.TrimSpace(m[3])
				key := "game:" + appID
				if it, ok := itemMap[key]; ok {
					if strings.Contains(strings.ToLower(reason), "disabled") || strings.Contains(strings.ToLower(reason), "suspended") {
						it.Status = StatusPaused
					} else if strings.Contains(strings.ToLower(reason), "corrupt") {
						it.Status = StatusCorrupt
					} else {
						it.Status = StatusError
					}
					it.ErrorDetail = reason
				}
			}

			// Track validation in progress: "App update changed : Running Update,Verifying Installed,"
			if m := rxContentUpdatePhase.FindStringSubmatch(line); m != nil {
				appID := m[2]
				phase := strings.TrimSpace(m[4])
				key := "game:" + appID
				if strings.Contains(phase, "Verifying") {
					name := "AppID " + appID
					if rep != nil {
						for _, a := range rep.Apps {
							if a.AppID == appID {
								name = a.Name
								break
							}
						}
					}
					if it, ok := itemMap[key]; ok {
						it.Status = StatusValidating
						if it.ErrorDetail == "" || it.ErrorDetail == "-" {
							it.ErrorDetail = "Validating files"
						}
					} else {
						itemMap[key] = &DownloadItem{
							AppID:       appID,
							Name:        name,
							Type:        "game",
							Status:      StatusValidating,
							Source:      "content_log",
							ErrorDetail: "Validating files",
						}
					}
				} else if phase == "None" {
					// Finished update or validation
					if it, ok := itemMap[key]; ok && it.Status == StatusValidating {
						if it.Source == "content_log" {
							delete(itemMap, key)
						} else {
							it.Status = StatusCompleted
							it.ErrorDetail = "Validation finished"
						}
					}
				}
			}
		}
	}
}

func tailLines(r io.Reader, maxLines int) []string {
	scanner := bufio.NewScanner(r)
	var ring []string
	for scanner.Scan() {
		ring = append(ring, scanner.Text())
		if len(ring) > maxLines {
			ring = ring[1:]
		}
	}
	return ring
}

// DownloadDetail contains complete diagnostic context for one AppID.
type DownloadDetail struct {
	Item            DownloadItem `json:"item"`
	ManifestPath    string       `json:"manifest_path,omitempty"`
	StateFlagsRaw   int64        `json:"state_flags_raw"`
	StateFlagNames  []string     `json:"state_flag_names"`
	BuildID         string       `json:"build_id,omitempty"`
	TargetBuildID   string       `json:"target_build_id,omitempty"`
	BytesToStage    int64        `json:"bytes_to_stage,omitempty"`
	BytesStaged     int64        `json:"bytes_staged,omitempty"`
	AutoUpdate      string       `json:"auto_update_behavior,omitempty"`
	StagingDirs     []string     `json:"staging_dirs,omitempty"`
	StagingFiles    []string     `json:"staging_files,omitempty"`
	TotalArtifactSz int64        `json:"total_artifact_size_bytes"`
	RecentLogs      []string     `json:"recent_logs,omitempty"`
}

// StateFlagDescriptions maps StateFlags bits to human readable names.
var StateFlagDescriptions = map[int64]string{
	StateUninstalled:    "Uninstalled",
	StateUpdateRequired: "UpdateRequired",
	StateFullyInstalled: "FullyInstalled",
	StateEncrypted:      "Encrypted",
	StateLocked:         "Locked",
	StateFilesMissing:   "FilesMissing",
	StateAppRunning:     "AppRunning",
	StateFilesCorrupt:   "FilesCorrupt",
	StateUpdateRunning:  "UpdateRunning",
	StateUpdatePaused:   "UpdatePaused",
	StateUpdateStarted:  "UpdateStarted",
	StateUninstalling:   "Uninstalling",
	StateBackupRunning:  "BackupRunning",
	StateReconfiguring:  "Reconfiguring",
	StateValidating:     "Validating",
	StateAddingFiles:    "AddingFiles",
	StatePreallocating:  "Preallocating",
	StateDownloading:    "Downloading",
	StateStaging:        "Staging",
	StateCommitting:     "Committing",
	StateUpdateStopping: "UpdateStopping",
}

// GetDownloadDetail finds full details about a specific AppID across libraries.
func GetDownloadDetail(roots []string, appID string) (*DownloadDetail, error) {
	if len(roots) == 0 {
		roots = Defaults()
	}

	rep, _ := ScanDownloads(roots)
	var foundItem *DownloadItem
	for _, it := range rep.All {
		if it.AppID == appID {
			itemCopy := it
			foundItem = &itemCopy
			break
		}
	}

	detail := &DownloadDetail{
		Item: DownloadItem{AppID: appID, Name: "AppID " + appID, Type: "game", Status: StatusCompleted},
	}
	if foundItem != nil {
		detail.Item = *foundItem
	}

	// Inspect manifest
	libRep, err := Scan(roots)
	if err == nil {
		for _, a := range libRep.Apps {
			if a.AppID == appID {
				detail.Item.Name = a.Name
				detail.ManifestPath = a.Manifest
				detail.Item.Library = a.Library
				if m, err := parse(a.Manifest); err == nil {
					if appState, ok := m["AppState"].(map[string]interface{}); ok {
						detail.BuildID = str(appState["buildid"])
						detail.TargetBuildID = str(appState["TargetBuildID"])
						detail.AutoUpdate = str(appState["AutoUpdateBehavior"])
						if s := str(appState["BytesToStage"]); s != "" {
							detail.BytesToStage, _ = strconv.ParseInt(s, 10, 64)
						}
						if s := str(appState["BytesStaged"]); s != "" {
							detail.BytesStaged, _ = strconv.ParseInt(s, 10, 64)
						}
						if s := str(appState["StateFlags"]); s != "" {
							detail.StateFlagsRaw, _ = strconv.ParseInt(s, 10, 64)
						}
					}
				}
				break
			}
		}
	}

	// Deconstruct StateFlags
	for bit, name := range StateFlagDescriptions {
		if detail.StateFlagsRaw&bit != 0 {
			detail.StateFlagNames = append(detail.StateFlagNames, name)
		}
	}
	sort.Strings(detail.StateFlagNames)

	// Inspect artifacts in downloading/ and shadercache
	for _, lib := range libRep.Libraries {
		dlAppDir := filepath.Join(lib, "steamapps", "downloading", appID)
		if fi, err := os.Stat(dlAppDir); err == nil && fi.IsDir() {
			detail.StagingDirs = append(detail.StagingDirs, dlAppDir)
			_ = filepath.Walk(dlAppDir, func(path string, info os.FileInfo, err error) error {
				if err == nil && !info.IsDir() {
					detail.TotalArtifactSz += info.Size()
				}
				return nil
			})
		}

		// Check .delta files matching this app or its depots
		dlDir := filepath.Join(lib, "steamapps", "downloading")
		deltas, _ := filepath.Glob(filepath.Join(dlDir, "*.delta"))
		for _, d := range deltas {
			if fi, err := os.Stat(d); err == nil {
				// If delta filename contains appID
				base := filepath.Base(d)
				if strings.Contains(base, appID) {
					detail.StagingFiles = append(detail.StagingFiles, d)
					detail.TotalArtifactSz += fi.Size()
				}
			}
		}

		// Check workshop downloading
		wsDir := filepath.Join(lib, "steamapps", "workshop", "downloads", appID)
		if fi, err := os.Stat(wsDir); err == nil && fi.IsDir() {
			detail.StagingDirs = append(detail.StagingDirs, wsDir)
			_ = filepath.Walk(wsDir, func(path string, info os.FileInfo, err error) error {
				if err == nil && !info.IsDir() {
					detail.TotalArtifactSz += info.Size()
				}
				return nil
			})
		}

		// Check shadercache downloads
		scDir := filepath.Join(lib, "steamapps", "shadercache", appID, "downloads")
		if fi, err := os.Stat(scDir); err == nil && fi.IsDir() {
			detail.StagingDirs = append(detail.StagingDirs, scDir)
			_ = filepath.Walk(scDir, func(path string, info os.FileInfo, err error) error {
				if err == nil && !info.IsDir() {
					detail.TotalArtifactSz += info.Size()
				}
				return nil
			})
		}
	}

	// Recent log mentions
	for _, root := range roots {
		contentLog := filepath.Join(root, "logs", "content_log.txt")
		if f, err := os.Open(contentLog); err == nil {
			scanner := bufio.NewScanner(f)
			target := "AppID " + appID
			for scanner.Scan() {
				text := scanner.Text()
				if strings.Contains(text, target) {
					detail.RecentLogs = append(detail.RecentLogs, text)
					if len(detail.RecentLogs) > 10 {
						detail.RecentLogs = detail.RecentLogs[1:]
					}
				}
			}
			f.Close()
		}
	}

	return detail, nil
}

// CleanDownloadArtifacts removes staging directories, delta files, and temporary cache
// for an AppID without touching the game installation directory.
func CleanDownloadArtifacts(roots []string, appID string) (int, int64, error) {
	if len(roots) == 0 {
		roots = Defaults()
	}

	detail, err := GetDownloadDetail(roots, appID)
	if err != nil {
		return 0, 0, err
	}

	removedCount := 0
	var freedBytes int64

	for _, d := range detail.StagingDirs {
		var sz int64
		_ = filepath.Walk(d, func(_ string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() {
				sz += info.Size()
			}
			return nil
		})
		if err := os.RemoveAll(d); err == nil {
			removedCount++
			freedBytes += sz
		}
	}

	for _, f := range detail.StagingFiles {
		if fi, err := os.Stat(f); err == nil {
			sz := fi.Size()
			if err := os.Remove(f); err == nil {
				removedCount++
				freedBytes += sz
			}
		}
	}

	return removedCount, freedBytes, nil
}

// CleanLingeringArtifacts scans libraries for empty downloading folders or unmapped patches
// that do not belong to active updates.
func CleanLingeringArtifacts(roots []string) (int, int64) {
	if len(roots) == 0 {
		roots = Defaults()
	}

	rep, err := Scan(roots)
	if err != nil {
		return 0, 0
	}

	removed := 0
	var freed int64

	for _, lib := range rep.Libraries {
		dlDir := filepath.Join(lib, "steamapps", "downloading")
		entries, err := os.ReadDir(dlDir)
		if err != nil {
			continue
		}

		for _, de := range entries {
			p := filepath.Join(dlDir, de.Name())
			if de.IsDir() {
				// If directory is empty, remove it
				sub, err := os.ReadDir(p)
				if err == nil && len(sub) == 0 {
					if err := os.Remove(p); err == nil {
						removed++
					}
				}
			}
		}
	}

	return removed, freed
}

