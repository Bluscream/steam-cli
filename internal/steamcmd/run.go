package steamcmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

type ExitError struct{ Code int }

func (e *ExitError) Error() string { return fmt.Sprintf("SteamCMD exited with status %d", e.Code) }

// Run inherits the terminal for Steam Guard and password prompts. It never uses a shell.
func (m *Manager) Run(ctx context.Context, args []string, in io.Reader, out, errOut io.Writer, success string) error {
	if m.HTTP.Offline {
		return errors.New("--offline cannot constrain Valve's subprocess; SteamCMD execution is disabled")
	}
	l, e := m.lock(ctx)
	if e != nil {
		return e
	}
	defer l.Unlock()
	path, e := m.ensure(ctx)
	if e != nil {
		return e
	}
	c := exec.CommandContext(ctx, path, args...)
	c.Stdin = in
	configureProcess(c, in)
	c.Dir = filepath.Dir(path)
	// API credentials have no purpose in the Valve subprocess.
	for _, v := range os.Environ() {
		k, _, _ := strings.Cut(v, "=")
		if k != "STEAM_WEB_API_KEY" && k != "STEAM_API_KEY" && k != "ASF_IPC_PASSWORD" {
			c.Env = append(c.Env, v)
		}
	}
	watcher := &outputWatch{dst: out, success: success}
	c.Stdout = watcher
	c.Stderr = errOut
	if e = c.Run(); e != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var x *exec.ExitError
		if errors.As(e, &x) {
			return &ExitError{x.ExitCode()}
		}
		return errors.New("cannot start SteamCMD; run steam doctor (Linux needs the 32-bit loader; Apple Silicon needs Rosetta)")
	}
	if success != "" && !watcher.found {
		return errors.New("SteamCMD did not report successful completion; inspect its output (exit 0 alone is insufficient)")
	}
	return nil
}

// Retain only a bounded tail; SteamCMD can emit arbitrarily large download logs.
type outputWatch struct {
	mu            sync.Mutex
	dst           io.Writer
	success, tail string
	found         bool
}

func (w *outputWatch) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	s := w.tail + string(b)
	if w.success != "" && strings.Contains(s, w.success) {
		w.found = true
	}
	if len(s) > 4096 {
		s = s[len(s)-4096:]
	}
	w.tail = s
	return w.dst.Write(b)
}

var idPattern = regexp.MustCompile(`^[1-9][0-9]*$`)

func ValidID(id string) bool { return idPattern.MatchString(id) && len(id) <= 20 }
func SafeValue(s string) error {
	if strings.ContainsAny(s, "\x00\r\n\"") || strings.HasPrefix(s, "+") {
		return errors.New("SteamCMD value contains unsupported quotes, control characters, or command prefix")
	}
	return nil
}

type Download struct {
	AppID, ItemID, Dir, User, Platform, Branch string
	Validate                                   bool
}

func (d Download) Args() ([]string, string, error) {
	if !ValidID(d.AppID) || (d.ItemID != "" && !ValidID(d.ItemID)) {
		return nil, "", errors.New("app and workshop IDs must be positive decimal integers")
	}
	for _, v := range []string{d.Dir, d.User, d.Branch} {
		if e := SafeValue(v); e != nil {
			return nil, "", e
		}
	}
	if d.User == "" {
		d.User = "anonymous"
	}
	a := []string{"+@ShutdownOnFailedCommand", "1"}
	if d.User == "anonymous" {
		a = append(a, "+@NoPromptForPassword", "1")
	}
	if d.Platform != "" {
		switch d.Platform {
		case "windows", "linux", "macos":
		default:
			return nil, "", errors.New("platform must be windows, linux, or macos")
		}
		a = append(a, "+@sSteamCmdForcePlatformType", d.Platform)
	}
	if d.Dir != "" {
		a = append(a, "+force_install_dir", d.Dir)
	}
	a = append(a, "+login", d.User)
	success := "Success! App '" + d.AppID + "' fully installed."
	if d.ItemID != "" {
		a = append(a, "+workshop_download_item", d.AppID, d.ItemID)
		success = "Success. Downloaded item " + d.ItemID
	} else {
		a = append(a, "+app_update", d.AppID)
		if d.Branch != "" {
			a = append(a, "-beta", d.Branch)
		}
	}
	if d.Validate {
		a = append(a, "validate")
	}
	a = append(a, "+quit")
	return a, success, nil
}
