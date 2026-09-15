package library

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestModernAndLegacyLibraries(t *testing.T) {
	for _, modern := range []bool{true, false} {
		t.Run(fmt.Sprint(modern), func(t *testing.T) {
			root, other := t.TempDir(), t.TempDir()
			for _, p := range []string{root, other} {
				os.Mkdir(filepath.Join(p, "steamapps"), 0700)
			}
			escaped := strings.ReplaceAll(other, `\`, `\\`)
			value := fmt.Sprintf(`"1" "%s"`, escaped)
			if modern {
				value = fmt.Sprintf(`"1" { "path" "%s" "apps" { "730" "100" } }`, escaped)
			}
			os.WriteFile(filepath.Join(root, "steamapps", "libraryfolders.vdf"), []byte(`"libraryfolders" { `+value+` }`), 0600)
			os.WriteFile(filepath.Join(other, "steamapps", "appmanifest_730.acf"), []byte(`"AppState" { "appid" "730" "name" "Counter-Strike 2" "installdir" "Counter-Strike Global Offensive" "StateFlags" "4" "SizeOnDisk" "50000000000" }`), 0600)
			r, e := Scan([]string{root})
			if e != nil || len(r.Libraries) != 2 || len(r.Apps) != 1 || r.Apps[0].SizeOnDisk != "50000000000" {
				t.Fatalf("%+v %v", r, e)
			}
		})
	}
}
func TestBadManifestReported(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "steamapps"), 0700)
	os.WriteFile(filepath.Join(root, "steamapps", "appmanifest_1.acf"), []byte(`"AppState" { "installdir" "../escape" }`), 0600)
	r, e := Scan([]string{root})
	if e != nil || len(r.Apps) != 0 || len(r.Warnings) != 1 {
		t.Fatalf("%+v %v", r, e)
	}
}

func TestRedactLaunchOptions(t *testing.T) {
	cases := []struct{ in, want string }{
		{"-novid -high", "-novid -high"},
		{"--password hunter2 -novid", "--password <redacted> -novid"},
		{"-rcon_password s3cret", "-rcon_password <redacted>"},
		{"--api-key=abc123 %command%", "--api-key=<redacted> %command%"},
		{"%command% --token xyz", "%command% --token <redacted>"},
		{"curl https://host/x?token=abc123", "curl https://host/x?token=<redacted>"},
		{"https://user:pa55@host/path", "https://user:<redacted>@host/path"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := RedactLaunchOptions(tc.in); got != tc.want {
			t.Errorf("RedactLaunchOptions(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// A value must never survive redaction, and ordinary options must not be
// mangled into looking redacted.
func TestRedactLaunchOptionsDoesNotLeakOrOverreach(t *testing.T) {
	secret := "sup3rs3cret"
	if got := RedactLaunchOptions("-password " + secret); strings.Contains(got, secret) {
		t.Errorf("secret survived redaction: %q", got)
	}
	plain := "-console -novid -w 1920 -h 1080 +exec autoexec.cfg"
	if got := RedactLaunchOptions(plain); got != plain {
		t.Errorf("ordinary options were altered: %q", got)
	}
	if !LaunchOptionsLookRisky("-password x") {
		t.Error("a credential-bearing option should be reported as risky")
	}
	if LaunchOptionsLookRisky(plain) {
		t.Error("ordinary options should not be reported as risky")
	}
}

// --- loginusers.vdf -----------------------------------------------------

func writeVDF(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

const loginUsers = `"users"
{
	"76561198000000001"
	{
		"AccountName"		"old"
		"MostRecent"		"0"
		"Timestamp"		"1000"
	}
	"76561198000000002"
	{
		"AccountName"		"recent"
		"MostRecent"		"1"
		"Timestamp"		"2000"
	}
	"76561198000000003"
	{
		"AccountName"		"newest-but-not-current"
		"MostRecent"		"0"
		"Timestamp"		"9999"
	}
}
`

// The account Steam marks MostRecent wins even when another has a later
// timestamp: the timestamp only breaks ties within the same flag.
func TestLoggedInUserPrefersMostRecentFlag(t *testing.T) {
	root := t.TempDir()
	writeVDF(t, root, "config/loginusers.vdf", loginUsers)

	got, err := LoggedInUser([]string{root})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "76561198000000002" {
		t.Errorf("LoggedInUser = %q, want the MostRecent account", got)
	}
}

func TestLoggedInUserFallsBackToTimestamp(t *testing.T) {
	root := t.TempDir()
	writeVDF(t, root, "config/loginusers.vdf", `"users"
{
	"76561198000000001" { "Timestamp" "1000" }
	"76561198000000002" { "Timestamp" "3000" }
}
`)
	got, err := LoggedInUser([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	if got != "76561198000000002" {
		t.Errorf("LoggedInUser = %q, want the latest timestamp", got)
	}
}

func TestLoggedInUserReportsAbsence(t *testing.T) {
	if _, err := LoggedInUser([]string{t.TempDir()}); err == nil {
		t.Fatal("expected an error when no loginusers.vdf exists")
	}
	root := t.TempDir()
	writeVDF(t, root, "config/loginusers.vdf", `"users" { }`)
	if _, err := LoggedInUser([]string{root}); err == nil {
		t.Fatal("expected an error when the file lists no users")
	}
}

// --- compat tools and launch options ------------------------------------

func TestScanCompatToolsSkipsGlobalDefault(t *testing.T) {
	root := t.TempDir()
	writeVDF(t, root, "config/config.vdf", `"InstallConfigStore"
{
	"Software"
	{
		"Valve"
		{
			"Steam"
			{
				"CompatToolMapping"
				{
					"0" { "name" "proton_experimental" }
					"220" { "name" "Proton-GE" }
					"440" { "name" "" }
				}
			}
		}
	}
}
`)
	got := ScanCompatTools([]string{root})
	if got["220"] != "Proton-GE" {
		t.Errorf("per-app tool = %q", got["220"])
	}
	// "0" is the global default, not a per-app override.
	if _, ok := got["0"]; ok {
		t.Error("the global default should not appear as an app override")
	}
	// An empty name is not an override either.
	if _, ok := got["440"]; ok {
		t.Error("an empty tool name should be ignored")
	}
}

func TestScanLaunchOptions(t *testing.T) {
	root := t.TempDir()
	writeVDF(t, root, "userdata/123/config/localconfig.vdf", `"UserLocalConfigStore"
{
	"Software"
	{
		"Valve"
		{
			"Steam"
			{
				"apps"
				{
					"220" { "LaunchOptions" "-novid %command%" }
					"440" { "LaunchOptions" "" }
				}
			}
		}
	}
}
`)
	got := ScanLaunchOptions([]string{root})
	if got["220"] != "-novid %command%" {
		t.Errorf("launch options = %q", got["220"])
	}
	if _, ok := got["440"]; ok {
		t.Error("an empty launch option should be ignored")
	}
}

func TestScanEnrichesAppsWithOverrides(t *testing.T) {
	root := t.TempDir()
	writeVDF(t, root, "steamapps/libraryfolders.vdf",
		`"libraryfolders" { "0" { "path" "`+root+`" } }`)
	writeVDF(t, root, "steamapps/appmanifest_220.acf",
		`"AppState" { "appid" "220" "name" "Half-Life 2" "installdir" "hl2" "StateFlags" "4" }`)
	writeVDF(t, root, "config/config.vdf",
		`"InstallConfigStore" { "Software" { "Valve" { "Steam" { "CompatToolMapping" { "220" { "name" "Proton-GE" } } } } } }`)
	writeVDF(t, root, "userdata/1/config/localconfig.vdf",
		`"UserLocalConfigStore" { "Software" { "Valve" { "Steam" { "apps" { "220" { "LaunchOptions" "-novid" } } } } } }`)

	rep, err := Scan([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Apps) != 1 {
		t.Fatalf("got %d apps, want 1", len(rep.Apps))
	}
	if rep.Apps[0].CompatTool != "Proton-GE" {
		t.Errorf("CompatTool = %q", rep.Apps[0].CompatTool)
	}
	if rep.Apps[0].LaunchOptions != "-novid" {
		t.Errorf("LaunchOptions = %q", rep.Apps[0].LaunchOptions)
	}
}

// A malformed or absent config must not fail the scan; overrides are optional.
func TestScanSurvivesMissingOverrideFiles(t *testing.T) {
	root := t.TempDir()
	writeVDF(t, root, "steamapps/libraryfolders.vdf",
		`"libraryfolders" { "0" { "path" "`+root+`" } }`)
	writeVDF(t, root, "steamapps/appmanifest_220.acf",
		`"AppState" { "appid" "220" "name" "HL2" "installdir" "hl2" "StateFlags" "4" }`)
	writeVDF(t, root, "config/config.vdf", "this is not vdf {{{")

	rep, err := Scan([]string{root})
	if err != nil {
		t.Fatalf("a malformed config.vdf must not fail the scan: %v", err)
	}
	if len(rep.Apps) != 1 || rep.Apps[0].CompatTool != "" {
		t.Errorf("apps = %+v", rep.Apps)
	}
}
