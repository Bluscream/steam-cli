// Package steamclient locates and runs the desktop Steam client.
//
// This forwards to Valve's own launcher rather than reimplementing it: the
// client owns login, the overlay, and protocol handling. The CLI only needs to
// find the right executable and hand over the arguments.
package steamclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// ExitError carries the client's own exit status.
type ExitError struct{ Code int }

func (e *ExitError) Error() string { return fmt.Sprintf("Steam exited with status %d", e.Code) }

type Locator struct {
	// Path is an explicit executable or wrapper script; it wins outright.
	Path string
	// LookPath and Stat are injectable so discovery can be tested.
	LookPath func(string) (string, error)
	Stat     func(string) (os.FileInfo, error)
	// DefaultArgs are prepended to every launch, before the caller's own
	// arguments, so a flag such as -console applies to each invocation.
	DefaultArgs []string
	// Self is this process's executable. A candidate resolving to it is
	// rejected: installing this CLI as "steam" would otherwise make the
	// client command re-invoke the CLI forever.
	Self string
}

func (l Locator) lookPath(s string) (string, error) {
	if l.LookPath != nil {
		return l.LookPath(s)
	}
	return exec.LookPath(s)
}

func (l Locator) stat(s string) (os.FileInfo, error) {
	if l.Stat != nil {
		return l.Stat(s)
	}
	return os.Stat(s)
}

// candidates lists where a desktop Steam install is normally found.
func (l Locator) candidates() []string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "windows":
		return []string{
			filepath.Join(os.Getenv("ProgramFiles(x86)"), "Steam", "steam.exe"),
			filepath.Join(os.Getenv("ProgramFiles"), "Steam", "steam.exe"),
		}
	case "darwin":
		return []string{"/Applications/Steam.app/Contents/MacOS/steam_osx"}
	default:
		return []string{
			"/usr/bin/steam",
			"/usr/games/steam",
			filepath.Join(home, ".local", "share", "Steam", "steam.sh"),
			"/var/lib/flatpak/exports/bin/com.valvesoftware.Steam",
			filepath.Join(home, ".local", "share", "flatpak", "exports", "bin", "com.valvesoftware.Steam"),
		}
	}
}

// sameAsSelf reports whether a candidate is this very executable.
func (l Locator) sameAsSelf(path string) bool {
	if l.Self == "" {
		return false
	}
	a, e1 := filepath.EvalSymlinks(l.Self)
	b, e2 := filepath.EvalSymlinks(path)
	if e1 != nil || e2 != nil {
		return filepath.Clean(l.Self) == filepath.Clean(path)
	}
	return a == b
}

// Find returns the Steam client executable.
func (l Locator) Find() (string, error) {
	if l.Path != "" {
		if _, e := l.stat(l.Path); e != nil {
			return "", fmt.Errorf("configured Steam client path is not usable: %w", e)
		}
		if l.sameAsSelf(l.Path) {
			return "", errors.New("the configured Steam client path points at this CLI, not at Valve's Steam client")
		}
		return l.Path, nil
	}

	var skippedSelf bool
	if p, e := l.lookPath("steam"); e == nil {
		if l.sameAsSelf(p) {
			skippedSelf = true
		} else {
			return p, nil
		}
	}
	for _, c := range l.candidates() {
		if _, e := l.stat(c); e != nil {
			continue
		}
		if l.sameAsSelf(c) {
			skippedSelf = true
			continue
		}
		return c, nil
	}

	if skippedSelf {
		return "", errors.New("the only \"steam\" on PATH is this CLI; " +
			"install Valve's Steam client, or set STEAM_CLIENT_PATH to its launcher")
	}
	return "", errors.New("could not find the Steam client; " +
		"install it, or set STEAM_CLIENT_PATH (or steam_client_path in your profile) to its launcher")
}

// Run forwards arguments to the Steam client.
func (l Locator) Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	path, err := l.Find()
	if err != nil {
		return err
	}
	full := make([]string, 0, len(l.DefaultArgs)+len(args))
	full = append(full, l.DefaultArgs...)
	full = append(full, args...)
	c := exec.CommandContext(ctx, path, full...)
	c.Stdin, c.Stdout, c.Stderr = stdin, stdout, stderr
	if err := c.Run(); err != nil {
		var x *exec.ExitError
		if errors.As(err, &x) {
			return &ExitError{x.ExitCode()}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	return nil
}

// URL builds a steam:// URL for a verb that takes an AppID.
//
// The AppID is validated rather than interpolated blindly: these strings are
// handed to a protocol handler, so a crafted value must not be able to smuggle
// a different command past it.
func URL(verb, appID string) (string, error) {
	switch verb {
	case "run", "install", "uninstall", "validate", "store", "rungameid":
	default:
		return "", fmt.Errorf("unsupported steam:// verb %q", verb)
	}
	id := strings.TrimSpace(appID)
	if _, e := strconv.ParseUint(id, 10, 64); e != nil {
		return "", errors.New("APPID must be a positive integer")
	}
	return "steam://" + verb + "/" + id, nil
}

// ValidateURL accepts only a steam: URL, so this cannot be turned into a
// launcher for arbitrary protocol handlers.
func ValidateURL(raw string) error {
	if !strings.HasPrefix(raw, "steam://") {
		return errors.New("expected a steam:// URL")
	}
	if strings.ContainsAny(raw, " \t\r\n\x00\"'") {
		return errors.New("steam:// URL contains invalid characters")
	}
	return nil
}
