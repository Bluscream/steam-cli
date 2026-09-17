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
	StatusActive    DownloadStatus = "downloading"
	StatusStaging   DownloadStatus = "staging"
	StatusCommitting DownloadStatus = "committing"
	StatusPaused    DownloadStatus = "paused"
	StatusQueued    DownloadStatus = "queued"
	StatusScheduled DownloadStatus = "scheduled"
	StatusCorrupt   DownloadStatus = "corrupt"
	StatusError     DownloadStatus = "error"
	StatusCompleted DownloadStatus = "completed"
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

				key := "game:" + appID
				if existing, exists := itemMap[key]; exists {
					if existing.Status == StatusQueued || existing.Status == StatusCompleted {
						existing.Status = StatusActive
					}
				} else {
					// Discovered in downloading/ folder without a queued manifest
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
	scanRecentLogs(roots, itemMap)

	// Collect into report
	for _, it := range itemMap {
		items = append(items, *it)
	}

	sort.Slice(items, func(i, j int) bool {
		// Group priority: Active -> Corrupt -> Error -> Paused -> Queued -> Scheduled -> Completed
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
	case StatusStaging:
		return 2
	case StatusCommitting:
		return 3
	case StatusCorrupt:
		return 4
	case StatusError:
		return 5
	case StatusPaused:
		return 6
	case StatusQueued:
		return 7
	case StatusScheduled:
		return 8
	case StatusCompleted:
		return 9
	default:
		return 10
	}
}

var (
	rxContentError = regexp.MustCompile(`\[([^\]]+)\] AppID (\d+) update canceled : (.*)`)
	rxContentState = regexp.MustCompile(`\[([^\]]+)\] AppID (\d+) state changed : (.*)`)
)

func scanRecentLogs(roots []string, itemMap map[string]*DownloadItem) {
	for _, root := range roots {
		contentLog := filepath.Join(root, "logs", "content_log.txt")
		f, err := os.Open(contentLog)
		if err != nil {
			continue
		}
		defer f.Close()

		// Read last 200 lines
		lines := tailLines(f, 200)
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
