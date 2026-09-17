package library

import (
	"context"
	"encoding/csv"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// SteamRunning fails closed when process inspection is unavailable. Writers
// may explicitly override this conservative guard with --force.
func SteamRunning() bool {
	if runtime.GOOS == "linux" {
		entries, err := os.ReadDir("/proc")
		if err != nil {
			return true
		}
		for _, e := range entries {
			if _, err := strconv.Atoi(e.Name()); err != nil {
				continue
			}
			b, err := os.ReadFile(filepath.Join("/proc", e.Name(), "comm"))
			if err == nil && strings.EqualFold(strings.TrimSpace(string(b)), "steam") {
				return true
			}
		}
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if runtime.GOOS == "windows" {
		b, err := exec.CommandContext(ctx, "tasklist.exe", "/FO", "CSV", "/NH").Output()
		if err != nil {
			return true
		}
		rows, err := csv.NewReader(strings.NewReader(string(b))).ReadAll()
		if err != nil {
			return true
		}
		for _, row := range rows {
			if len(row) > 0 && strings.EqualFold(row[0], "steam.exe") {
				return true
			}
		}
		return false
	}
	b, err := exec.CommandContext(ctx, "ps", "-axo", "comm=").Output()
	if err != nil {
		return true
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.EqualFold(filepath.Base(strings.TrimSpace(line)), "steam") {
			return true
		}
	}
	return false
}

// KillSteam attempts to gracefully terminate running Steam processes.
func KillSteam() {
	if runtime.GOOS == "linux" {
		entries, err := os.ReadDir("/proc")
		if err == nil {
			for _, e := range entries {
				pid, err := strconv.Atoi(e.Name())
				if err != nil {
					continue
				}
				comm, err := os.ReadFile(filepath.Join("/proc", e.Name(), "comm"))
				if err == nil && strings.EqualFold(strings.TrimSpace(string(comm)), "steam") {
					if proc, err := os.FindProcess(pid); err == nil {
						_ = proc.Signal(os.Interrupt)
					}
				}
			}
		}
		return
	}
	if runtime.GOOS == "windows" {
		_ = exec.Command("taskkill", "/IM", "steam.exe").Run()
		return
	}
	_ = exec.Command("pkill", "-TERM", "steam").Run()
}


func uniqueFiles(paths []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range paths {
		canonical, err := filepath.EvalSymlinks(p)
		if err != nil {
			canonical = p
		}
		canonical, _ = filepath.Abs(canonical)
		if !seen[canonical] {
			seen[canonical] = true
			out = append(out, canonical)
		}
	}
	return out
}
