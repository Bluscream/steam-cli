package steamclient

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeExecutable(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFindPrefersExplicitPath(t *testing.T) {
	dir := t.TempDir()
	want := writeExecutable(t, dir, "mysteam", "#!/bin/sh\n")

	l := Locator{Path: want, LookPath: func(string) (string, error) {
		t.Error("PATH must not be consulted when an explicit path is set")
		return "", errors.New("no")
	}}
	got, err := l.Find()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Errorf("Find() = %q, want %q", got, want)
	}
}

func TestFindRejectsMissingExplicitPath(t *testing.T) {
	l := Locator{Path: filepath.Join(t.TempDir(), "absent")}
	if _, err := l.Find(); err == nil {
		t.Fatal("expected an error for a path that does not exist")
	}
}

func TestFindUsesPath(t *testing.T) {
	l := Locator{LookPath: func(s string) (string, error) {
		if s != "steam" {
			t.Errorf("looked up %q, want steam", s)
		}
		return "/usr/bin/steam", nil
	}}
	got, err := l.Find()
	if err != nil {
		t.Fatal(err)
	}
	if got != "/usr/bin/steam" {
		t.Errorf("Find() = %q", got)
	}
}

// Installing this CLI as "steam" must not make the client command re-invoke
// the CLI. The candidate is skipped and the error explains why.
func TestFindRefusesToInvokeItself(t *testing.T) {
	dir := t.TempDir()
	self := writeExecutable(t, dir, "steam", "#!/bin/sh\n")

	l := Locator{
		Self:     self,
		LookPath: func(string) (string, error) { return self, nil },
		Stat:     func(string) (os.FileInfo, error) { return nil, os.ErrNotExist },
	}
	_, err := l.Find()
	if err == nil {
		t.Fatal("expected Find to refuse this CLI as the Steam client")
	}
	if !strings.Contains(err.Error(), "this CLI") {
		t.Errorf("error should explain the collision, got %v", err)
	}
}

func TestFindRefusesSelfThroughSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	dir := t.TempDir()
	self := writeExecutable(t, dir, "steamcli", "#!/bin/sh\n")
	link := filepath.Join(dir, "steam")
	if err := os.Symlink(self, link); err != nil {
		t.Fatal(err)
	}

	l := Locator{
		Self:     self,
		LookPath: func(string) (string, error) { return link, nil },
		Stat:     func(string) (os.FileInfo, error) { return nil, os.ErrNotExist },
	}
	if _, err := l.Find(); err == nil {
		t.Fatal("a symlink back to this CLI must also be refused")
	}
}

func TestFindRejectsExplicitSelf(t *testing.T) {
	dir := t.TempDir()
	self := writeExecutable(t, dir, "steamcli", "#!/bin/sh\n")

	l := Locator{Path: self, Self: self}
	_, err := l.Find()
	if err == nil || !strings.Contains(err.Error(), "this CLI") {
		t.Fatalf("explicit self-path should be refused, got %v", err)
	}
}

func TestFindReportsAbsence(t *testing.T) {
	l := Locator{
		LookPath: func(string) (string, error) { return "", errors.New("not found") },
		Stat:     func(string) (os.FileInfo, error) { return nil, os.ErrNotExist },
	}
	_, err := l.Find()
	if err == nil || !strings.Contains(err.Error(), "STEAM_CLIENT_PATH") {
		t.Fatalf("error should name the override, got %v", err)
	}
}

func TestRunForwardsArgumentsAndOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script stand-in is POSIX-only")
	}
	dir := t.TempDir()
	fake := writeExecutable(t, dir, "fakesteam", "#!/bin/sh\necho \"args:$*\"\n")

	var out strings.Builder
	l := Locator{Path: fake}
	if err := l.Run(context.Background(), []string{"steam://run/730"}, nil, &out, io.Discard); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.TrimSpace(out.String()) != "args:steam://run/730" {
		t.Errorf("output = %q", out.String())
	}
}

func TestRunPropagatesExitCode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script stand-in is POSIX-only")
	}
	dir := t.TempDir()
	fake := writeExecutable(t, dir, "fakesteam", "#!/bin/sh\nexit 7\n")

	err := Locator{Path: fake}.Run(context.Background(), nil, nil, io.Discard, io.Discard)
	var x *ExitError
	if !errors.As(err, &x) || x.Code != 7 {
		t.Fatalf("err = %v, want ExitError{7}", err)
	}
}

func TestURLBuildsAndValidates(t *testing.T) {
	got, err := URL("run", "730")
	if err != nil || got != "steam://run/730" {
		t.Fatalf("URL = %q, %v", got, err)
	}
	// An AppID is interpolated into a string handed to a protocol handler, so
	// anything but an integer must be refused.
	for _, bad := range []string{"730/../install/220", "abc", "", "-1", "7 3 0", "730;x"} {
		if _, err := URL("run", bad); err == nil {
			t.Errorf("APPID %q should have been rejected", bad)
		}
	}
	if _, err := URL("delete-everything", "730"); err == nil {
		t.Error("unsupported verbs should be rejected")
	}
}

func TestValidateURL(t *testing.T) {
	if err := ValidateURL("steam://open/console"); err != nil {
		t.Errorf("valid URL rejected: %v", err)
	}
	for _, bad := range []string{
		"http://example.com",
		"file:///etc/passwd",
		"steam://run/730 ; rm -rf /",
		"steam://run/730\nsteam://uninstall/220",
		"",
	} {
		if err := ValidateURL(bad); err == nil {
			t.Errorf("%q should have been rejected", bad)
		}
	}
}
