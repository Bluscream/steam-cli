package library

import (
	"os"
	"path/filepath"
	"steamcli.local/steam/internal/steamvdf"
	"testing"
)

func configFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"steamapps/appmanifest_42.acf":      `"AppState" {"appid" "42" "name" "Game" "installdir" "Game" "UserConfig" {"BetaKey" "testing" "DisabledDLC" "44"} "MountedConfig" {"BetaKey" "old"} "InstalledDepots" {"100" {"dlcappid" "43" "size" "20"} } "unrelated" "keep"}`,
		"config/config.vdf":                 `"InstallConfigStore" {"software" {"valve" {"steam" {"Other" "keep"} } } }`,
		"userdata/1/config/localconfig.vdf": `"UserLocalConfigStore" {"software" {"valve" {"steam" {"Apps" {"42" {"LaunchOptions" "old" "Other" "keep"}}} } } }`,
	}
	for p, s := range files {
		p = filepath.Join(root, p)
		os.MkdirAll(filepath.Dir(p), 0700)
		if err := os.WriteFile(p, []byte(s), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}
func TestAppSettingsWriters(t *testing.T) {
	root := configFixture(t)
	roots := []string{root}
	cfg, e := LoadAppConfig(roots, "42")
	if e != nil {
		t.Fatal(e)
	}
	if cfg.Branch.Requested != "testing" || !cfg.Branch.Pending() || len(cfg.DLC) != 2 {
		t.Fatalf("bad config: %+v", cfg)
	}
	if _, e = SetDLCEnabled(roots, "42", "44", true); e != nil {
		t.Fatal(e)
	}
	if _, e = SetDLCEnabled(roots, "42", "43", false); e != nil {
		t.Fatal(e)
	}
	if _, e = SetBranch(roots, "42", ""); e != nil {
		t.Fatal(e)
	}
	p, e := SetLaunchOptions(roots, "1", "42", `env X="quoted" %command% -path C:\games`)
	if e != nil {
		t.Fatal(e)
	}
	m, e := steamvdf.Parse(p)
	if e != nil {
		t.Fatal(e)
	}
	entry := steamvdf.Obj(appsSection(m, false), "42")
	if steamvdf.Str(entry["Other"]) != "keep" {
		t.Fatal("lost unrelated app setting")
	}
	p, e = SetCompatTool(roots, "42", "custom-proton")
	if e != nil {
		t.Fatal(e)
	}
	m, _ = steamvdf.Parse(p)
	if len(steamvdf.Obj(m, "InstallConfigStore")) != 1 {
		t.Fatal("created competing case spelling")
	}
	if ScanCompatTools(roots)["42"] != "custom-proton" {
		t.Fatal("mapping not readable")
	}
	if _, e = SetCompatTool(roots, "42", ""); e != nil {
		t.Fatal(e)
	}
	cfg, e = LoadAppConfig(roots, "42")
	if e != nil {
		t.Fatal(e)
	}
	if cfg.Branch.Requested != "" || cfg.Branch.Mounted != "old" {
		t.Fatal("modified mounted state")
	}
}
func TestAccountSelectionAndRootAliases(t *testing.T) {
	root := configFixture(t)
	p := filepath.Join(root, "userdata/2/config/localconfig.vdf")
	os.MkdirAll(filepath.Dir(p), 0700)
	os.WriteFile(p, []byte(`"UserLocalConfigStore" {"Software" {"Valve" {"Steam" {"apps" {"42" {"LaunchOptions" "second"}}}}}}`), 0600)
	if _, e := LoadAppConfigForAccount([]string{root}, "", "42"); e == nil {
		t.Fatal("silently selected account")
	}
	c, e := LoadAppConfigForAccount([]string{root}, "2", "42")
	if e != nil || c.LaunchOptions != "second" {
		t.Fatal(c, e)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if os.Symlink(root, alias) == nil {
		if len(LocalConfigFiles([]string{root, alias})) != 2 {
			t.Fatal("duplicate physical files")
		}
	}
}
func TestCustomToolSymlink(t *testing.T) {
	root := configFixture(t)
	dir := filepath.Join(t.TempDir(), "Proton")
	os.MkdirAll(dir, 0700)
	os.WriteFile(filepath.Join(dir, "compatibilitytool.vdf"), []byte(`"compatibilitytools" {"compat_tools" {"custom-proton" {"display_name" "Custom"}}}`), 0600)
	os.MkdirAll(filepath.Join(root, "compatibilitytools.d"), 0700)
	if e := os.Symlink(dir, filepath.Join(root, "compatibilitytools.d", "Latest")); e != nil {
		t.Skip(e)
	}
	tools := AvailableCompatTools([]string{root})
	if len(tools) != 1 || tools[0].Name != "custom-proton" {
		t.Fatal(tools)
	}
}

// Explicit opt-in: read real files, then exercise writers only on temporary copies.
func TestRealConfigurationCopies(t *testing.T) {
	root := os.Getenv("STEAMCLI_AUDIT_ROOT")
	if root == "" {
		t.Skip("set STEAMCLI_AUDIT_ROOT for local read-only validation")
	}
	paths := []string{filepath.Join(root, "config/config.vdf")}
	paths = append(paths, LocalConfigFiles([]string{root})...)
	manifests, _ := filepath.Glob(filepath.Join(root, "steamapps/appmanifest_*.acf"))
	paths = append(paths, manifests...)
	for _, p := range paths {
		b, e := os.ReadFile(p)
		if e != nil {
			t.Fatal(e)
		}
		m, e := steamvdf.ParseBytes(b)
		if e != nil {
			t.Fatalf("%s: %v", filepath.Base(p), e)
		}
		copyPath := filepath.Join(t.TempDir(), "copy.vdf")
		os.WriteFile(copyPath, b, 0600)
		if e = steamvdf.Write(copyPath, m); e != nil {
			t.Fatal(e)
		}
	}
	t.Logf("validated %d real files using temporary copies", len(paths))
}
