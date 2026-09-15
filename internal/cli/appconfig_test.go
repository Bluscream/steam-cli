package cli

import (
	"os"
	"path/filepath"
	"steamcli.local/steam/internal/steamvdf"
	"strings"
	"testing"
)

func TestLocalConfigCLIWriteGuardAndAccount(t *testing.T) {
	cleanEnv(t)
	previous := steamRunning
	steamRunning = func() bool { return true }
	defer func() { steamRunning = previous }()
	root := t.TempDir()
	p := filepath.Join(root, "userdata/7/config/localconfig.vdf")
	os.MkdirAll(filepath.Dir(p), 0700)
	original := `"UserLocalConfigStore" {"Software" {"Valve" {"Steam" {"apps" {}}}}}`
	os.WriteFile(p, []byte(original), 0600)
	args := []string{"-o", "json", "library", "launch", "set", "42", "--root", root, "--account", "7"}
	if _, e := execute(t, append(args, "--", "-novid")...); e == nil || !strings.Contains(e.Error(), "Steam is running") {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(p)
	if string(b) != original {
		t.Fatal("guard changed config")
	}
	out, e := execute(t, append(args, "--force", "--", `-novid -password secret-test`)...)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(out, "secret-test") {
		t.Fatal("secret echoed")
	}
	m, e := steamvdf.Parse(p)
	if e != nil {
		t.Fatal(e)
	}
	entry := steamvdf.Obj(m, "UserLocalConfigStore", "Software", "Valve", "Steam", "apps", "42")
	if entry["LaunchOptions"] != `-novid -password secret-test` {
		t.Fatal("options were changed")
	}
}

func TestBranchDownloadUsesInstalledDirectory(t *testing.T) {
	cleanEnv(t)
	root := t.TempDir()
	p := filepath.Join(root, "steamapps/appmanifest_42.acf")
	os.MkdirAll(filepath.Dir(p), 0700)
	os.WriteFile(p, []byte(`"AppState" {"appid" "42" "name" "Example" "installdir" "Example Game"}`), 0600)
	out, e := execute(t, "--offline", "-o", "json", "library", "branch", "download", "42", "testing", "--root", root, "--user", "testuser", "--dry-run")
	if e != nil {
		t.Fatal(e)
	}
	for _, want := range []string{"Example Game", "-beta", "testing", "testuser", "+force_install_dir"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %s: %s", want, out)
		}
	}
	b, _ := os.ReadFile(p)
	if strings.Contains(string(b), "testing") {
		t.Fatal("dry run changed manifest")
	}
}

func TestServerEdit(t *testing.T) {
	cleanEnv(t)
	root := serverFixture(t)
	previous := steamRunning
	steamRunning = func() bool { return false }
	defer func() { steamRunning = previous }()
	out, e := execute(t, "-o", "json", "server", "edit", "10.0.0.1:27015", "--root", root, "--name", "Renamed", "--appid", "42")
	if e != nil || !strings.Contains(out, "Renamed") {
		t.Fatal(out, e)
	}
	out, e = execute(t, "-o", "json", "server", "favorites", "--root", root)
	if e != nil || !strings.Contains(out, "Renamed") {
		t.Fatal(out, e)
	}
}
