package idle

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"steamcli.local/steam/internal/asf"
	"steamcli.local/steam/internal/sdk"
)

type Engine struct {
	ASFClient *asf.Client
	DataDir   string
	Helper    sdk.Build
	HasHelper bool
}

type IdleResult struct {
	Method  string `json:"method"` // "asf" or "sdk"
	Message string `json:"message"`
	AppIDs  []int  `json:"appids,omitempty"`
	Text    string `json:"text,omitempty"`
	PID     int    `json:"pid,omitempty"`
}

// Start starts idling the specified AppIDs and/or displaying custom text.
// It attempts ASF first; if ASF is unavailable or fails, it falls back to the native SDK.
func (e *Engine) Start(ctx context.Context, appIDs []int, customText string, bot string) (IdleResult, error) {
	var asfErr error
	if e.ASFClient != nil && e.ASFClient.BaseURL != "" {
		res, err := e.ASFClient.Play(ctx, bot, appIDs, customText)
		if err == nil {
			return IdleResult{
				Method:  "asf",
				Message: res,
				AppIDs:  appIDs,
				Text:    customText,
			}, nil
		}
		asfErr = err
	}

	// Fallback to Native SDK
	if len(appIDs) == 0 {
		if asfErr != nil {
			return IdleResult{}, fmt.Errorf("ASF idling failed (%v) and native SDK cannot idle custom text without at least one AppID", asfErr)
		}
		return IdleResult{}, errors.New("idling requires at least one AppID when ASF is not configured")
	}

	// For native SDK, we launch an idle session using the first AppID
	appID := appIDs[0]
	pid, err := e.startSDKIdle(ctx, appID)
	if err != nil {
		if asfErr != nil {
			return IdleResult{}, fmt.Errorf("ASF idling failed (%v) and SDK fallback failed: %w", asfErr, err)
		}
		return IdleResult{}, fmt.Errorf("native SDK idling failed: %w", err)
	}

	return IdleResult{
		Method:  "sdk",
		Message: fmt.Sprintf("Started native Steamworks SDK idling for AppID %d (PID %d)", appID, pid),
		AppIDs:  []int{appID},
		PID:     pid,
	}, nil
}

// Stop stops idling on ASF and/or stops any running background SDK idle processes.
func (e *Engine) Stop(ctx context.Context, bot string) (string, error) {
	var messages []string

	// 1. ASF resume
	if e.ASFClient != nil && e.ASFClient.BaseURL != "" {
		msg, err := e.ASFClient.Resume(ctx, bot)
		if err == nil {
			messages = append(messages, "ASF: "+msg)
		}
	}

	// 2. Terminate background SDK PID if running
	if e.DataDir != "" {
		pidFile := filepath.Join(e.DataDir, "idle.pid")
		if b, err := os.ReadFile(pidFile); err == nil {
			pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
			if err == nil && pid > 0 {
				_ = syscall.Kill(pid, syscall.SIGTERM)
				_ = os.Remove(pidFile)
				messages = append(messages, fmt.Sprintf("Stopped native SDK idle process (PID %d)", pid))
			}
		}
	}

	if len(messages) == 0 {
		return "No active idle processes were stopped", nil
	}
	return strings.Join(messages, "; "), nil
}

func (e *Engine) startSDKIdle(ctx context.Context, appID int) (int, error) {
	if !e.HasHelper || e.Helper.Helper == "" {
		return 0, errors.New("native SDK helper is not built; run 'steamcli sdk build --sdk-dir <path>' or configure ASF")
	}

	pidFile := filepath.Join(e.DataDir, "idle.pid")
	// Stop existing if running
	if b, err := os.ReadFile(pidFile); err == nil {
		if oldPID, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && oldPID > 0 {
			_ = syscall.Kill(oldPID, syscall.SIGTERM)
		}
	}

	// Launch helper in background
	cmd := exec.Command(e.Helper.Helper)
	cmd.Env = append(os.Environ(), fmt.Sprintf("SteamAppId=%d", appID), fmt.Sprintf("STEAM_SDK_LIBRARY=%s", e.Helper.Library))
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return 0, err
	}
	cmd.Stdout = nil
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		return 0, err
	}

	// Send init operation
	_, _ = stdin.Write([]byte("{\"op\":\"init\"}\n"))

	_ = os.WriteFile(pidFile, []byte(fmt.Sprintf("%d\n", cmd.Process.Pid)), 0o644)
	return cmd.Process.Pid, nil
}
