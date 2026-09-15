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
