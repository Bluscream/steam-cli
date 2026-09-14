package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func execute(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	c := New(strings.NewReader(""), &out, &errOut)
	c.SetArgs(args)
	e := c.ExecuteContext(context.Background())
	return out.String(), e
}
func cleanEnv(t *testing.T) {
	t.Helper()
	t.Setenv("STEAM_WEB_API_KEY", "")
	t.Setenv("STEAM_API_KEY", "")
	t.Setenv("ASF_IPC_PASSWORD", "")
	t.Setenv("STEAM_CLI_CACHE_DIR", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("STEAM_WEB_URL", "")
	t.Setenv("STEAM_ASF_URL", "")
}
func TestJSONKeepsSteamIDsExact(t *testing.T) {
	cleanEnv(t)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"steamid":76561198000000001}`)) }))
	defer s.Close()
	out, e := execute(t, "web", "--url", s.URL, "call", "ITest", "Read", "--method", "GET")
	if e != nil || !strings.Contains(out, "76561198000000001") {
		t.Fatal(out, e)
	}
}
func TestSteamCMDDryRunWorksOffline(t *testing.T) {
	cleanEnv(t)
	out, e := execute(t, "--offline", "cmd", "download", "730", "--dir", filepath.Join(t.TempDir(), "space folder"), "--dry-run")
	if e != nil {
		t.Fatal(e)
	}
	var d struct{ Arguments []string }
	if e = json.Unmarshal([]byte(out), &d); e != nil || len(d.Arguments) == 0 {
		t.Fatal(out, e)
	}
}
func TestSteamIDConversions(t *testing.T) {
	for _, id := range []string{"76561197960287930", "STEAM_0:0:11101", "[U:1:22202]"} {
		d, e := convertID(id)
		if e != nil || d["steamid64"] != "76561197960287930" {
			t.Fatal(d, e)
		}
	}
	for _, id := range []string{"0", "76561197960265728", "[U:1:4294967296]", "STEAM_0:2:1", "STEAM_0:0:2147483648"} {
		if _, e := convertID(id); e == nil {
			t.Fatal("accepted", id)
		}
	}
}
func TestASFCommandFailureHasNonzeroStatusAndJSON(t *testing.T) {
	cleanEnv(t)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"Success":false,"Message":"invalid command"}`))
	}))
	defer s.Close()
	out, e := execute(t, "asf", "--url", s.URL, "command", "status")
	if e == nil || !strings.Contains(out, `"Success": false`) {
		t.Fatal(out, e)
	}
}
func TestCompletionIsOffline(t *testing.T) {
	cleanEnv(t)
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		out, e := execute(t, "completion", shell)
		if e != nil || len(out) < 100 {
			t.Fatal(shell, e)
		}
	}
}
func TestInvalidOutputStopsRequest(t *testing.T) {
	cleanEnv(t)
	hits := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer s.Close()
	_, e := execute(t, "--output", "bad", "web", "--url", s.URL, "server-info")
	if e == nil || hits != 0 {
		t.Fatal("invalid output allowed request")
	}
}

func TestStatusAndWorkshopCLI(t *testing.T) {
	cleanEnv(t)
	// Offline status should fail fast
	_, err := execute(t, "--offline", "status")
	if err == nil {
		t.Fatal("expected status to fail when offline")
	}

	// Workshop sub requires arguments
	_, err = execute(t, "workshop", "sub")
	if err == nil {
		t.Fatal("expected workshop sub without args to fail")
	}

	// Workshop collection requires ID
	_, err = execute(t, "workshop", "collection")
	if err == nil {
		t.Fatal("expected workshop collection without args to fail")
	}
}

