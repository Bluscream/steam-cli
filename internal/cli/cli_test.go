package cli

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
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
	t.Setenv("STEAM_API_KEY", "")
	t.Setenv("ASF_IPC_PASSWORD", "")
	t.Setenv("STEAM_CLI_CACHE_DIR", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("STEAM_WEB_URL", "")
	t.Setenv("STEAM_ASF_URL", "")
	t.Setenv("STEAM_COMMUNITY_URL", "")
	t.Setenv("STEAM_LOGIN_SECURE", "")
	t.Setenv("STEAM_ACCESS_TOKEN", "")
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

// A refused ASF command exits nonzero and still shows why, in whichever form
// the caller asked for.
func TestASFCommandFailureHasNonzeroStatusAndExplanation(t *testing.T) {
	cleanEnv(t)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"Success":false,"Message":"invalid command"}`))
	}))
	defer s.Close()

	// Default output reduces the envelope to its message.
	out, e := execute(t, "asf", "--url", s.URL, "command", "status")
	if e == nil {
		t.Fatal("a refused command must exit nonzero")
	}
	if !strings.Contains(out, "invalid command") {
		t.Errorf("the reason should be shown: %q", out)
	}

	// Asking for JSON still yields the whole envelope.
	out, e = execute(t, "-o", "json", "asf", "--url", s.URL, "command", "status")
	if e == nil || !strings.Contains(out, `"Success": false`) {
		t.Fatalf("json output should carry the envelope: %q %v", out, e)
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

// --- workshop ---

func workshopEnv(t *testing.T, api, comm string) {
	t.Helper()
	cleanEnv(t)
	t.Setenv("STEAM_API_KEY", "k")
	t.Setenv("STEAM_WEB_URL", api)
	t.Setenv("STEAM_COMMUNITY_URL", comm)
	t.Setenv("STEAM_LOGIN_SECURE", "76561197960287930%7C%7Ctok")
}

func TestWorkshopSubReportsPerItemFailure(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.PostForm.Get("publishedfileid") == "222" {
			w.Header().Set("x-eresult", "15")
		} else {
			w.Header().Set("x-eresult", "1")
		}
		w.Write([]byte(`{"response":{}}`))
	}))
	defer api.Close()
	workshopEnv(t, api.URL, api.URL)

	out, err := execute(t, "--allow-http", "-o", "json", "workshop", "sub", "4000", "111", "222")
	if err != nil {
		t.Fatalf("a partial success should not fail the command: %v", err)
	}
	var got struct {
		Succeeded int `json:"succeeded"`
		Failed    int `json:"failed"`
	}
	if e := json.Unmarshal([]byte(out), &got); e != nil {
		t.Fatalf("output is not JSON: %v\n%s", e, out)
	}
	if got.Succeeded != 1 || got.Failed != 1 {
		t.Errorf("summary = %+v, want 1 succeeded and 1 failed\n%s", got, out)
	}
}

func TestWorkshopSubFailsWhenEverythingFails(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-eresult", "15")
		w.Write([]byte(`{"response":{}}`))
	}))
	defer api.Close()
	workshopEnv(t, api.URL, api.URL)

	if _, err := execute(t, "--allow-http", "workshop", "sub", "4000", "111"); err == nil {
		t.Fatal("a batch where every item failed must exit nonzero")
	}
}

func TestWorkshopSubNeedsItems(t *testing.T) {
	cleanEnv(t)
	if _, err := execute(t, "workshop", "sub", "4000"); err == nil {
		t.Fatal("expected an error when no items are given")
	}
}

func TestWorkshopRejectsBadAppID(t *testing.T) {
	cleanEnv(t)
	for _, args := range [][]string{
		{"workshop", "sub", "nope", "1"},
		{"workshop", "installed", "-5"},
	} {
		if _, err := execute(t, args...); err == nil {
			t.Errorf("%v: expected an AppID validation error", args)
		}
	}
}

func TestWorkshopCreateCollectionAddsItems(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-eresult", "1")
		w.Write([]byte(`{"response":{"publishedfileid":"777"}}`))
	}))
	defer api.Close()
	var children []string
	comm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		children = append(children, r.PostForm.Get("childid"))
		w.Write([]byte(`{"success":1}`))
	}))
	defer comm.Close()
	workshopEnv(t, api.URL, comm.URL)

	out, err := execute(t, "--allow-http", "workshop", "create-collection", "4000",
		"--title", "T", "--item", "1", "--item", "2")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	if len(children) != 2 {
		t.Errorf("children added = %v, want two", children)
	}
	if !strings.Contains(out, `"items_added"`) {
		t.Errorf("output must report what was added:\n%s", out)
	}
}

// Creating a populated collection needs a Community session; say so instead of
// reporting a success that did nothing.
func TestWorkshopCreateCollectionWithoutSession(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-eresult", "1")
		w.Write([]byte(`{"response":{"publishedfileid":"777"}}`))
	}))
	defer api.Close()
	workshopEnv(t, api.URL, api.URL)
	t.Setenv("STEAM_LOGIN_SECURE", "")

	_, err := execute(t, "--allow-http", "workshop", "create-collection", "4000", "--title", "T", "--item", "1")
	if err == nil {
		t.Fatal("expected an error explaining that a session is required")
	}
	if !strings.Contains(err.Error(), "STEAM_LOGIN_SECURE") {
		t.Errorf("error should name the missing credential: %v", err)
	}
}

func TestWorkshopCreateCollectionNeedsTitle(t *testing.T) {
	cleanEnv(t)
	if _, err := execute(t, "workshop", "create-collection", "4000"); err == nil {
		t.Fatal("expected --title to be required")
	}
}

func TestWorkshopDeleteRequiresConfirmation(t *testing.T) {
	cleanEnv(t)
	_, err := execute(t, "workshop", "delete-collection", "4000", "123")
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("deletion must be confirmed: %v", err)
	}
}

func TestWorkshopMembershipNeedsSession(t *testing.T) {
	cleanEnv(t)
	t.Setenv("STEAM_LOGIN_SECURE", "")
	for _, sub := range []string{"add-items", "remove-items"} {
		_, err := execute(t, "workshop", sub, "4000", "500", "1")
		if err == nil || !strings.Contains(err.Error(), "STEAM_LOGIN_SECURE") {
			t.Errorf("%s: expected a session error, got %v", sub, err)
		}
	}
}

func TestWorkshopSubsUsesCommunity(t *testing.T) {
	var filter string
	comm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		filter = r.URL.Query().Get("browsefilter")
		w.Write([]byte(`<div id="sharedfile_42"></div>`))
	}))
	defer comm.Close()
	workshopEnv(t, comm.URL, comm.URL)

	out, err := execute(t, "--allow-http", "workshop", "subs", "4000")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if filter != "mysubscriptions" {
		t.Errorf("browsefilter = %q", filter)
	}
	if !strings.Contains(out, "42") {
		t.Errorf("output missing the item:\n%s", out)
	}
}

func TestWorkshopFavoritesUsesCommunity(t *testing.T) {
	var filter string
	comm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		filter = r.URL.Query().Get("browsefilter")
		w.Write([]byte(`<div id="sharedfile_7"></div>`))
	}))
	defer comm.Close()
	workshopEnv(t, comm.URL, comm.URL)

	if _, err := execute(t, "--allow-http", "workshop", "favorites", "4000"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if filter != "myfavorites" {
		t.Errorf("browsefilter = %q, want myfavorites", filter)
	}
}

func TestWorkshopInstalledIsLocal(t *testing.T) {
	cleanEnv(t)
	root := t.TempDir()
	dir := filepath.Join(root, "steamapps", "workshop")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	acf := "\"AppWorkshop\"\n{\n\t\"appid\"\t\t\"4000\"\n\t\"WorkshopItemsInstalled\"\n\t{\n\t\t\"123456\"\n\t\t{\n\t\t\t\"size\"\t\t\"1\"\n\t\t}\n\t}\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "appworkshop_4000.acf"), []byte(acf), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "steamapps", "libraryfolders.vdf"),
		[]byte("\"libraryfolders\"\n{\n\t\"0\"\n\t{\n\t\t\"path\"\t\t\""+root+"\"\n\t}\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := execute(t, "-o", "json", "workshop", "installed", "4000", "--root", root)
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	if !strings.Contains(out, "123456") {
		t.Errorf("installed item missing:\n%s", out)
	}
	// Values inside the item block must not be mistaken for item IDs.
	if strings.Contains(out, `"4000"`) && strings.Count(out, "123456") == 0 {
		t.Errorf("unexpected parse result:\n%s", out)
	}
}

// --- status ---

func TestStatusOfflineFails(t *testing.T) {
	cleanEnv(t)
	if _, err := execute(t, "--offline", "status"); err == nil {
		t.Fatal("status must fail in offline mode")
	}
}

func TestStatusRawOutput(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "GetNumberOfCurrentPlayers"):
			w.Write([]byte(`{"response":{"player_count":1234567,"result":1}}`))
		case strings.Contains(r.URL.Path, "GetGameServersStatus"):
			w.Write([]byte(`{"result":{"services":{"SessionsLogon":"normal"},
				"datacenters":{"EU":{"capacity":"high","load":"low"}}}}`))
		default:
			w.Write([]byte(`{"ok":1}`))
		}
	}))
	defer api.Close()
	cleanEnv(t)
	t.Setenv("STEAM_API_KEY", "k")
	t.Setenv("STEAM_WEB_URL", api.URL)
	t.Setenv("STEAM_COMMUNITY_URL", api.URL)

	out, err := execute(t, "--allow-http", "--output", "raw", "status", "--no-cm", "--app", "730")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	for _, want := range []string{"Core Services", "Online Players", "1,234,567", "Datacenters", "HIGH"} {
		if !strings.Contains(out, want) {
			t.Errorf("raw output missing %q:\n%s", want, out)
		}
	}
}

func TestStatusRejectsBadApp(t *testing.T) {
	cleanEnv(t)
	if _, err := execute(t, "status", "--app", "abc"); err == nil {
		t.Fatal("expected --app validation")
	}
}

// A profile may opt a trusted LAN host into plaintext so --allow-http is not
// needed on every invocation. Loopback is always permitted, so this uses a
// non-loopback address (TEST-NET-1) to exercise the actual check: the URL is
// validated before any connection is attempted.
func TestProfileAllowHTTPGovernsPlaintext(t *testing.T) {
	cleanEnv(t)
	dir := t.TempDir()

	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	const host = "http://192.0.2.1:1242"

	strict := write("strict.json", `{"default_profile":"lan","profiles":{"lan":{"asf_url":"`+host+`"}}}`)
	_, err := execute(t, "--timeout", "1s", "--config", strict, "asf", "status")
	if err == nil || !strings.Contains(err.Error(), "HTTPS is required") {
		t.Fatalf("plaintext must be refused without allow_http, got %v", err)
	}

	relaxed := write("lan.json", `{"default_profile":"lan","profiles":{"lan":{"asf_url":"`+host+`","allow_http":true}}}`)
	_, err = execute(t, "--timeout", "1s", "--config", relaxed, "asf", "status")
	if err == nil {
		t.Fatal("expected the request to be attempted and fail to connect")
	}
	if strings.Contains(err.Error(), "HTTPS is required") {
		t.Fatalf("allow_http should have permitted the scheme, got %v", err)
	}
}

// --- asf --bots and --output parsed ---

func asfServer(t *testing.T, handler http.HandlerFunc) (string, func()) {
	t.Helper()
	s := httptest.NewServer(handler)
	return s.URL, s.Close
}

func TestASFShortExtractsToken(t *testing.T) {
	cleanEnv(t)
	url, done := asfServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"Result":{"gabeN":{"Result":"JKWGP","Message":"Success!","Success":true}},"Message":"OK","Success":true}`))
	})
	defer done()

	out, err := execute(t, "--output", "short", "asf", "--url", url, "token", "gabeN")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.TrimSpace(out) != "JKWGP" {
		t.Errorf("short output = %q, want just the token", out)
	}
}

func TestASFShortMultipleBotsArePrefixed(t *testing.T) {
	cleanEnv(t)
	url, done := asfServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"Result":{"B":{"Result":"22222"},"A":{"Result":"11111"}},"Success":true}`))
	})
	defer done()

	out, err := execute(t, "--output", "short", "asf", "--url", url, "token", "--bots", "A,B")
	if err != nil {
		t.Fatal(err)
	}
	// short for multiple bots returns bare values
	if !strings.Contains(out, "11111") || !strings.Contains(out, "22222") {
		t.Errorf("short output = %q", out)
	}
}

func TestASFShortFallsBackToMessage(t *testing.T) {
	cleanEnv(t)
	url, done := asfServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"Result":{"erikjohnson":{"Result":null,"Message":"Bot is not connected.","Success":false}},"Success":false}`))
	})
	defer done()

	out, err := execute(t, "--output", "short", "asf", "--url", url, "token", "erikjohnson")
	// Success=false must still exit nonzero while showing the reason.
	if err == nil {
		t.Error("a failed ASF operation should exit nonzero")
	}
	if !strings.Contains(out, "Bot is not connected.") {
		t.Errorf("short output should explain the failure, got %q", out)
	}
}

// short applies to ASF envelopes; anything else still prints as JSON.
func TestShortFallsThroughForNonASFPayloads(t *testing.T) {
	cleanEnv(t)
	url, done := asfServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"steamid":"7656119800000001"}`))
	})
	defer done()

	out, err := execute(t, "--output", "short", "web", "--url", url, "call", "ITest", "Read", "--method", "GET")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "7656119800000001") {
		t.Errorf("non-ASF payload should still be printed, got %q", out)
	}
}

func TestASFBotsFlagAndDefaultSelector(t *testing.T) {
	cleanEnv(t)
	var paths []string
	url, done := asfServer(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Write([]byte(`{"Result":{},"Message":"OK","Success":true}`))
	})
	defer done()

	cases := []struct {
		args []string
		want string
	}{
		{[]string{"asf", "--url", url, "bots"}, "/Api/Bot/ASF"},
		{[]string{"asf", "--url", url, "bots", "Alpha"}, "/Api/Bot/Alpha"},
		{[]string{"asf", "--url", url, "--bots", "Alpha,Beta", "bots"}, "/Api/Bot/Alpha,Beta"},
		// A positional selector wins over the flag.
		{[]string{"asf", "--url", url, "--bots", "Alpha", "bots", "Gamma"}, "/Api/Bot/Gamma"},
		{[]string{"asf", "--url", url, "--bots", "Alpha", "token"}, "/Api/Bot/Alpha/TwoFactorAuthentication/Token"},
		{[]string{"asf", "--url", url, "token"}, "/Api/Bot/ASF/TwoFactorAuthentication/Token"},
		{[]string{"asf", "--url", url, "--bots", "Alpha", "pause"}, "/Api/Bot/Alpha/Pause"},
		{[]string{"asf", "--url", url, "resume"}, "/Api/Bot/ASF/Resume"},
	}
	for _, tc := range cases {
		paths = nil
		if _, err := execute(t, tc.args...); err != nil {
			t.Fatalf("%v: %v", tc.args, err)
		}
		if len(paths) != 1 || paths[0] != tc.want {
			t.Errorf("%v => %v, want %q", tc.args, paths, tc.want)
		}
	}
}

func TestOutputFormatValidation(t *testing.T) {
	cleanEnv(t)
	if _, err := execute(t, "--output", "nonsense", "id", "76561197960287930"); err == nil {
		t.Fatal("expected an invalid --output to be rejected")
	}
	for _, fmt := range []string{"auto", "table", "json", "compact", "raw", "short", "csv"} {
		if _, err := execute(t, "--output", fmt, "--offline", "id", "76561197960287930"); err != nil {
			t.Fatalf("%s must be accepted: %v", fmt, err)
		}
	}
}

// --- client ---

func TestClientForwardsToLauncher(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script stand-in is POSIX-only")
	}
	cleanEnv(t)
	dir := t.TempDir()
	log := filepath.Join(dir, "args.txt")
	fake := filepath.Join(dir, "fakesteam")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > "+log+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("STEAM_CLIENT_PATH", fake)

	cases := []struct {
		args []string
		want string
	}{
		{[]string{"client", "run", "730"}, "steam://run/730"},
		{[]string{"client", "install", "220"}, "steam://install/220"},
		{[]string{"client", "validate", "730"}, "steam://validate/730"},
		{[]string{"client", "open", "steam://open/console"}, "steam://open/console"},
		{[]string{"client", "shutdown"}, "-shutdown"},
		{[]string{"client", "launch", "--", "-silent"}, "-silent"},
	}
	for _, tc := range cases {
		if err := os.Remove(log); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if _, err := execute(t, tc.args...); err != nil {
			t.Fatalf("%v: %v", tc.args, err)
		}
		b, err := os.ReadFile(log)
		if err != nil {
			t.Fatalf("%v: launcher was not invoked: %v", tc.args, err)
		}
		if strings.TrimSpace(string(b)) != tc.want {
			t.Errorf("%v forwarded %q, want %q", tc.args, strings.TrimSpace(string(b)), tc.want)
		}
	}
}

func TestClientRejectsBadInput(t *testing.T) {
	cleanEnv(t)
	t.Setenv("STEAM_CLIENT_PATH", filepath.Join(t.TempDir(), "absent"))
	for _, args := range [][]string{
		{"client", "run", "not-a-number"},
		{"client", "open", "http://example.com"},
		{"client", "open", "file:///etc/passwd"},
	} {
		if _, err := execute(t, args...); err == nil {
			t.Errorf("%v should have been rejected", args)
		}
	}
}

func TestClientPathReportsResolvedBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script stand-in is POSIX-only")
	}
	cleanEnv(t)
	fake := filepath.Join(t.TempDir(), "fakesteam")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("STEAM_CLIENT_PATH", fake)

	out, err := execute(t, "client", "path")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, fake) {
		t.Errorf("client path = %q, want %q", out, fake)
	}
}

func TestRootCommandIsNamedSteamcli(t *testing.T) {
	c := New(strings.NewReader(""), io.Discard, io.Discard)
	if c.Name() != "steamcli" {
		t.Errorf("root command = %q, want steamcli", c.Name())
	}
}

func TestASFTokenAliases(t *testing.T) {
	cleanEnv(t)
	var paths []string
	url, done := asfServer(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Write([]byte(`{"Result":{"A":{"Result":"JKWGP"}},"Success":true}`))
	})
	defer done()

	const want = "/Api/Bot/A/TwoFactorAuthentication/Token"
	for _, name := range []string{"token", "2fa", "auth"} {
		paths = nil
		out, err := execute(t, "--output", "short", "asf", "--url", url, name, "A")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(paths) != 1 || paths[0] != want {
			t.Errorf("%s => %v, want %q", name, paths, want)
		}
		if strings.TrimSpace(out) != "JKWGP" {
			t.Errorf("%s => %q", name, out)
		}
	}
}

func TestClientDefaultArgs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script stand-in is POSIX-only")
	}
	cleanEnv(t)
	dir := t.TempDir()
	log := filepath.Join(dir, "args.txt")
	fake := filepath.Join(dir, "fakesteam")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nprintf '%s|' \"$@\" > "+log+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, "config.json")
	body := `{"default_profile":"d","profiles":{"d":{"steam_client_path":"` + fake +
		`","steam_client_args":["-console"]}}}`
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	run := func(args ...string) string {
		t.Helper()
		os.Remove(log)
		if _, err := execute(t, append([]string{"--config", cfg}, args...)...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		b, err := os.ReadFile(log)
		if err != nil {
			t.Fatalf("%v: launcher not invoked: %v", args, err)
		}
		return string(b)
	}

	if got := run("client", "run", "730"); got != "-console|steam://run/730|" {
		t.Errorf("configured default not applied: %q", got)
	}
	if got := run("client", "launch", "--", "-bigpicture"); got != "-console|-bigpicture|" {
		t.Errorf("launch argv = %q", got)
	}
	if got := run("client", "--steam-arg", "-silent", "run", "730"); got != "-console|-silent|steam://run/730|" {
		t.Errorf("--steam-arg should add to the defaults: %q", got)
	}
	if got := run("client", "--no-default-args", "run", "730"); got != "steam://run/730|" {
		t.Errorf("--no-default-args should suppress them: %q", got)
	}

	// The environment overrides the profile.
	t.Setenv("STEAM_CLIENT_ARGS", "-silent -noverifyfiles")
	if got := run("client", "run", "730"); got != "-silent|-noverifyfiles|steam://run/730|" {
		t.Errorf("STEAM_CLIENT_ARGS should override the profile: %q", got)
	}
}

// --- default output ---------------------------------------------------------

// The default is a rendered view where one exists, and JSON where none does.
func TestAutoRendersWhereARendererExists(t *testing.T) {
	cleanEnv(t)
	out, err := execute(t, "--offline", "id", "76561197960287930")
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(strings.TrimSpace(out), "{") {
		t.Errorf("default output should be rendered, not JSON:\n%s", out)
	}
	if !strings.Contains(strings.ToLower(out), "steamid64") || !strings.Contains(out, "STEAM_0:0:11101") {
		t.Errorf("rendered output missing fields:\n%s", out)
	}

	js, err := execute(t, "-o", "json", "--offline", "id", "76561197960287930")
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]string
	if e := json.Unmarshal([]byte(js), &parsed); e != nil {
		t.Fatalf("-o json must still produce JSON: %v", e)
	}
}

// An ASF envelope is reduced automatically; a payload that is not one is not.
func TestAutoParsesASFEnvelopesOnly(t *testing.T) {
	cleanEnv(t)
	token := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"Result":{"A":{"Result":"JKWGP"}},"Success":true}`))
	}))
	defer token.Close()

	out, err := execute(t, "asf", "--url", token.URL, "token", "A")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "JKWGP") || !strings.Contains(out, "2FA Token") {
		t.Errorf("expected 2FA card in human output, got %q", out)
	}

	shortOut, err := execute(t, "--output", "short", "asf", "--url", token.URL, "token", "A")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(shortOut) != "JKWGP" {
		t.Errorf("--output short must return bare token, got %q", shortOut)
	}

	// A bot listing carries data per bot, not an outcome; it must survive whole.
	listing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"Result":{"A":{"BotName":"A","IsConnectedAndLoggedOn":true,
		  "s_SteamID":"76561197960287930","CardsFarmer":{"Paused":false,"NowFarming":false,"GamesToFarm":[]}}},
		  "Message":"OK","Success":true}`))
	}))
	defer listing.Close()

	out, err = execute(t, "asf", "--url", listing.URL, "bots")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "A: OK") {
		t.Errorf("a bot listing must not be flattened to its envelope message:\n%s", out)
	}
	if !strings.Contains(out, "BOT") || !strings.Contains(out, "76561197960287930") {
		t.Errorf("bot listing should render as a table:\n%s", out)
	}
}

func TestASFStatusRendering(t *testing.T) {
	cleanEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"Result":{"Version":"6.0.1.2","ProcessID":1234,"MemoryUsage":102400,"ProcessStartTime":"2026-09-14T12:00:00Z","BotsCount":3,"BuildVariant":"generic"},"Success":true}`))
	}))
	defer server.Close()

	out, err := execute(t, "asf", "--url", server.URL, "status")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ASF version", "6.0.1.2", "1234", "100.0 MiB", "generic"} {
		if !strings.Contains(out, want) {
			t.Errorf("asf status output missing %q:\n%s", want, out)
		}
	}
}

func TestStatusRendersByDefaultAndAsJSON(t *testing.T) {
	cleanEnv(t)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "GetNumberOfCurrentPlayers") {
			w.Write([]byte(`{"response":{"player_count":1234567,"result":1}}`))
			return
		}
		w.Write([]byte(`{"ok":1}`))
	}))
	defer api.Close()
	t.Setenv("STEAM_WEB_URL", api.URL)
	t.Setenv("STEAM_COMMUNITY_URL", api.URL)

	out, err := execute(t, "--allow-http", "status", "--no-cm", "--no-coordinator", "--app", "730")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Core Services", "Online Players", "1,234,567"} {
		if !strings.Contains(out, want) {
			t.Errorf("default status output missing %q:\n%s", want, out)
		}
	}

	js, err := execute(t, "--allow-http", "-o", "json", "status", "--no-cm", "--no-coordinator", "--app", "730")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(strings.TrimSpace(js), "{") {
		t.Errorf("-o json should produce JSON:\n%s", js)
	}
}

func TestOutputShorthandAndValidation(t *testing.T) {
	cleanEnv(t)
	if _, err := execute(t, "-o", "table", "--offline", "id", "76561197960287930"); err != nil {
		t.Errorf("-o shorthand should work: %v", err)
	}
	if _, err := execute(t, "-o", "nonsense", "--offline", "id", "76561197960287930"); err == nil {
		t.Error("an invalid --output should be rejected")
	}
}

// --- web profile ------------------------------------------------------------

func TestWebProfileAliasesAndRendering(t *testing.T) {
	cleanEnv(t)
	body := `{"response":{"players":[{"steamid":"76561197960287930","personaname":"Gaben",
	  "realname":"Gabe","profileurl":"https://steamcommunity.com/id/x/","avatarfull":"https://a/f.jpg",
	  "personastate":1,"communityvisibilitystate":3,"profilestate":1,"timecreated":1063407589,
	  "lastlogoff":1789000000,"loccountrycode":"US","primaryclanid":"103582791429521408"}]}}`
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
	defer s.Close()

	for _, name := range []string{"player", "profile", "profiles"} {
		out, err := execute(t, "web", "--url", s.URL, name, "76561197960287930")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, want := range []string{"Gaben", "76561197960287930", "[U:1:22202]", "Online", "public", "US"} {
			if !strings.Contains(out, want) {
				t.Errorf("%s output missing %q:\n%s", name, want, out)
			}
		}
		if strings.HasPrefix(strings.TrimSpace(out), "{") {
			t.Errorf("%s should render, not print JSON:\n%s", name, out)
		}
	}

	js, err := execute(t, "-o", "json", "web", "--url", s.URL, "profile", "76561197960287930")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(js, `"personaname"`) {
		t.Errorf("-o json should pass the payload through:\n%s", js)
	}
}

// "players" is a command of its own (current player count) and must not be
// captured by an alias of "player".
func TestWebPlayersIsNotShadowedByPlayerAliases(t *testing.T) {
	cleanEnv(t)
	var gotPath string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Write([]byte(`{"response":{"player_count":1234,"result":1}}`))
	}))
	defer s.Close()

	out, err := execute(t, "web", "--url", s.URL, "players", "730")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotPath, "GetNumberOfCurrentPlayers") {
		t.Errorf("web players hit %q, want the player-count endpoint", gotPath)
	}
	if !strings.Contains(out, "1,234") {
		t.Errorf("player count missing:\n%s", out)
	}
}

func TestWebProfileMultipleRendersTable(t *testing.T) {
	cleanEnv(t)
	body := `{"response":{"players":[
	  {"steamid":"1","personaname":"Beta","personastate":0,"communityvisibilitystate":1},
	  {"steamid":"2","personaname":"Alpha","personastate":1,"communityvisibilitystate":3,"gameextrainfo":"CS2","gameid":"730"}]}}`
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
	defer s.Close()

	out, err := execute(t, "web", "--url", s.URL, "profile", "1,2")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "PERSONA") {
		t.Errorf("several profiles should render as a table:\n%s", out)
	}
	if strings.Index(out, "Alpha") > strings.Index(out, "Beta") {
		t.Errorf("profiles should be sorted by persona:\n%s", out)
	}
	if !strings.Contains(out, "In game: CS2") {
		t.Errorf("an in-game player should show the game:\n%s", out)
	}
	if !strings.Contains(out, "private") {
		t.Errorf("visibility should be named:\n%s", out)
	}
}

func TestWebProfileUnknownSteamID(t *testing.T) {
	cleanEnv(t)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"response":{"players":[]}}`))
	}))
	defer s.Close()

	out, err := execute(t, "web", "--url", s.URL, "profile", "76561197960287930")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "not exist") {
		t.Errorf("an empty result should be explained:\n%s", out)
	}
}

func TestWebBansRendering(t *testing.T) {
	cleanEnv(t)
	body := `{"players":[{"SteamId":"76561197960287930","CommunityBanned":false,"VACBanned":true,"NumberOfVACBans":1,"DaysSinceLastBan":123,"NumberOfGameBans":0,"EconomyBan":"none"}]}`
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
	defer s.Close()

	out, err := execute(t, "web", "--url", s.URL, "bans", "76561197960287930")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"76561197960287930", "VAC ban", "BANNED", "123"} {
		if !strings.Contains(out, want) {
			t.Errorf("bans output missing %q:\n%s", want, out)
		}
	}
}

func TestWebServerInfoAndResolveRendering(t *testing.T) {
	cleanEnv(t)
	infoServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"servertime":1789413416,"servertimestring":"Mon Sep 14 12:16:56 2026"}`))
	}))
	defer infoServer.Close()

	out, err := execute(t, "web", "--url", infoServer.URL, "server-info")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Server time") || !strings.Contains(out, "1789413416") {
		t.Errorf("server-info missing fields:\n%s", out)
	}

	resolveServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"response":{"steamid":"76561197960287930","success":1}}`))
	}))
	defer resolveServer.Close()

	out, err = execute(t, "web", "--url", resolveServer.URL, "resolve", "gabe")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "76561197960287930") || !strings.Contains(out, "[U:1:22202]") {
		t.Errorf("resolve missing fields:\n%s", out)
	}
}

func TestCSVOutputWithAndWithoutHeader(t *testing.T) {
	cleanEnv(t)
	body := `{"response":{"players":[
	  {"steamid":"1","personaname":"Beta","personastate":0,"communityvisibilitystate":1,"loccountrycode":"US"},
	  {"steamid":"2","personaname":"Alpha","personastate":1,"communityvisibilitystate":3,"loccountrycode":"DE"}]}}`
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
	defer s.Close()

	// Default CSV output includes headers
	outWithHeader, err := execute(t, "-o", "csv", "web", "--url", s.URL, "profile", "1,2")
	if err != nil {
		t.Fatal(err)
	}
	linesWithHeader := strings.Split(strings.TrimSpace(outWithHeader), "\n")
	if len(linesWithHeader) < 3 {
		t.Fatalf("expected at least 3 lines (1 header + 2 rows), got:\n%s", outWithHeader)
	}
	if !strings.Contains(linesWithHeader[0], "Persona") || !strings.Contains(linesWithHeader[0], "SteamID64") {
		t.Errorf("expected header in first row of CSV output, got: %q", linesWithHeader[0])
	}
	if !strings.Contains(linesWithHeader[1], "Alpha") || !strings.Contains(linesWithHeader[2], "Beta") {
		t.Errorf("expected data rows in CSV output, got:\n%s", outWithHeader)
	}

	// Suppressing headers with --with-header=false
	outNoHeader, err := execute(t, "-o", "csv", "--with-header=false", "web", "--url", s.URL, "profile", "1,2")
	if err != nil {
		t.Fatal(err)
	}
	linesNoHeader := strings.Split(strings.TrimSpace(outNoHeader), "\n")
	if len(linesNoHeader) != 2 {
		t.Fatalf("expected exactly 2 rows without header, got %d:\n%s", len(linesNoHeader), outNoHeader)
	}
	if strings.Contains(linesNoHeader[0], "PERSONA") {
		t.Errorf("header should be suppressed with --with-header=false: %q", linesNoHeader[0])
	}
	if !strings.Contains(linesNoHeader[0], "Alpha") {
		t.Errorf("first row should be Alpha without header, got: %q", linesNoHeader[0])
	}
}

func TestWebProfileDefaultsToLoggedInUser(t *testing.T) {
	cleanEnv(t)
	t.Setenv("STEAM_USER_ID", "76561198022446661")

	var gotSteamIDs string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSteamIDs = r.URL.Query().Get("steamids")
		w.Write([]byte(`{"response":{"players":[{"steamid":"76561198022446661","personaname":"blu"}]}}`))
	}))
	defer s.Close()

	out, err := execute(t, "web", "--url", s.URL, "profile")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotSteamIDs != "76561198022446661" {
		t.Errorf("expected steamids=76561198022446661, got %q", gotSteamIDs)
	}
	if !strings.Contains(out, "blu") {
		t.Errorf("expected profile output to contain 'blu', got: %s", out)
	}
}

func TestWebOwnedDefaultsToCommunitySession(t *testing.T) {
	cleanEnv(t)
	// SteamID derived from steamLoginSecure cookie
	t.Setenv("STEAM_LOGIN_SECURE", "76561198022446661%7C%7Ctoken")

	var gotSteamID string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSteamID = r.URL.Query().Get("steamid")
		w.Write([]byte(`{"response":{"game_count":1,"games":[{"appid":730,"name":"CS2"}]}}`))
	}))
	defer s.Close()

	_, err := execute(t, "web", "--url", s.URL, "owned")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotSteamID != "76561198022446661" {
		t.Errorf("expected steamid=76561198022446661, got %q", gotSteamID)
	}
}

func TestWebAchievementsDefaultsToUserWhenOneArg(t *testing.T) {
	cleanEnv(t)
	t.Setenv("STEAM_USER_ID", "76561198022446661")

	var gotSteamID, gotAppID string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSteamID = r.URL.Query().Get("steamid")
		gotAppID = r.URL.Query().Get("appid")
		w.Write([]byte(`{"playerstats":{"achievements":[]}}`))
	}))
	defer s.Close()

	_, err := execute(t, "web", "--url", s.URL, "achievements", "730")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotSteamID != "76561198022446661" || gotAppID != "730" {
		t.Errorf("expected steamid=76561198022446661 appid=730, got steamid=%q appid=%q", gotSteamID, gotAppID)
	}
}

// storeFixture serves the Steam Store search endpoint so app-name resolution
// can be tested without reaching the real store.
func storeFixture(t *testing.T, items string) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "storesearch") {
			w.WriteHeader(404)
			return
		}
		fmt.Fprintf(w, `{"total":1,"items":[%s]}`, items)
	}))
	t.Setenv("STEAM_STORE_URL", s.URL)
	return s
}

const vrchatItem = `{"id":438100,"type":"app","name":"VRChat"}`

func TestAppsCommand(t *testing.T) {
	cleanEnv(t)
	store := storeFixture(t, vrchatItem)
	defer store.Close()

	out, err := execute(t, "--allow-http", "apps", "vrchat")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(strings.ToLower(out), "vrchat") || !strings.Contains(out, "438100") {
		t.Errorf("expected apps output to contain 'VRChat' and AppID 438100, got:\n%s", out)
	}
}

func TestWebNewsResolvesAppName(t *testing.T) {
	cleanEnv(t)
	store := storeFixture(t, vrchatItem)
	defer store.Close()
	var gotAppID string
	webServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAppID = r.URL.Query().Get("appid")
		w.Write([]byte(`{"appnews":{"appid":438100,"newsitems":[{"gid":"1","title":"Update","author":"Dev","feedlabel":"Community","date":1700000000}]}}`))
	}))
	defer webServer.Close()

	var outBuf, errBuf bytes.Buffer
	c := New(strings.NewReader(""), &outBuf, &errBuf)
	c.SetArgs([]string{"--allow-http", "web", "--url", webServer.URL, "news", "vrchat"})
	if err := c.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotAppID != "438100" {
		t.Errorf("expected appid=438100, got %q", gotAppID)
	}
	if !strings.Contains(errBuf.String(), "Resolved \"vrchat\" to VRChat (AppID 438100)") {
		t.Errorf("expected resolution notice in stderr, got:\n%s", errBuf.String())
	}
	if !strings.Contains(outBuf.String(), "Update") {
		t.Errorf("expected news output to contain news title, got:\n%s", outBuf.String())
	}
}

func TestGlobalSearchCommand(t *testing.T) {
	cleanEnv(t)
	store := storeFixture(t, vrchatItem)
	defer store.Close()
	// The local-library section still reflects this machine, so the assertions
	// below deliberately cover only the store category, which is fixtured.
	out, err := execute(t, "--allow-http", "search", "vrchat")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(strings.ToLower(out), "vrchat") {
		t.Errorf("expected search output to contain 'vrchat', got:\n%s", out)
	}
	if !strings.Contains(out, "Steam Store Apps") {
		t.Errorf("expected the store category to be rendered, got:\n%s", out)
	}
}

func TestLibraryCustomOverrides(t *testing.T) {
	cleanEnv(t)
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "steamapps"), 0700)
	os.MkdirAll(filepath.Join(root, "config"), 0700)
	os.MkdirAll(filepath.Join(root, "userdata", "12345", "config"), 0700)

	// App 730 installed
	os.WriteFile(filepath.Join(root, "steamapps", "appmanifest_730.acf"), []byte(`"AppState" { "appid" "730" "name" "CS2" "installdir" "CS2" }`), 0600)
	// App 550 installed
	os.WriteFile(filepath.Join(root, "steamapps", "appmanifest_550.acf"), []byte(`"AppState" { "appid" "550" "name" "L4D2" "installdir" "L4D2" }`), 0600)

	// config.vdf with compat tool for 730
	configVDF := `"InstallConfigStore" { "Software" { "Valve" { "Steam" { "CompatToolMapping" { "730" { "name" "proton_experimental" } } } } } }`
	os.WriteFile(filepath.Join(root, "config", "config.vdf"), []byte(configVDF), 0600)

	// localconfig.vdf with launch options for 550
	localVDF := `"UserLocalConfigStore" { "Software" { "Valve" { "Steam" { "apps" { "550" { "LaunchOptions" "-novid -console" } } } } } }`
	os.WriteFile(filepath.Join(root, "userdata", "12345", "config", "localconfig.vdf"), []byte(localVDF), 0600)

	out, err := execute(t, "library", "custom", "--root", root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "CS2") || !strings.Contains(out, "proton_experimental") {
		t.Errorf("expected CS2 with compat tool, got:\n%s", out)
	}
	if !strings.Contains(out, "L4D2") || !strings.Contains(out, "-novid -console") {
		t.Errorf("expected L4D2 with launch options, got:\n%s", out)
	}
}

func TestInfoCommand(t *testing.T) {
	cleanEnv(t)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ISteamWebAPIUtil/GetServerInfo/v1/":
			w.Write([]byte(`{"servertime":1789420000,"servertimestring":"Mon Sep 14 14:00:00 2026"}`))
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer s.Close()

	out, err := execute(t, "--config", "/dev/null", "info")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Client & Environment") {
		t.Errorf("expected info output to contain 'Client & Environment', got:\n%s", out)
	}
	if !strings.Contains(out, "Platform") {
		t.Errorf("expected info output to contain 'Platform', got:\n%s", out)
	}
}

func TestLibrarySort(t *testing.T) {
	cleanEnv(t)
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "steamapps"), 0700)

	// App 730 installed (smaller size)
	os.WriteFile(filepath.Join(root, "steamapps", "appmanifest_730.acf"), []byte(`"AppState" { "appid" "730" "name" "CS2" "SizeOnDisk" "1000" "installdir" "CS2" }`), 0600)
	// App 550 installed (larger size)
	os.WriteFile(filepath.Join(root, "steamapps", "appmanifest_550.acf"), []byte(`"AppState" { "appid" "550" "name" "L4D2" "SizeOnDisk" "9000" "installdir" "L4D2" }`), 0600)

	// Sort by size (descending: 550 before 730)
	outSize, err := execute(t, "library", "--root", root, "--sort", "size")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	posL4D2 := strings.Index(outSize, "L4D2")
	posCS2 := strings.Index(outSize, "CS2")
	if posL4D2 == -1 || posCS2 == -1 || posL4D2 > posCS2 {
		t.Errorf("expected L4D2 before CS2 when sorting by size, got:\n%s", outSize)
	}

	// Sort by name (ascending: CS2 before L4D2)
	outName, err := execute(t, "library", "--root", root, "--sort", "name")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	posL4D2 = strings.Index(outName, "L4D2")
	posCS2 = strings.Index(outName, "CS2")
	if posL4D2 == -1 || posCS2 == -1 || posCS2 > posL4D2 {
		t.Errorf("expected CS2 before L4D2 when sorting by name, got:\n%s", outName)
	}
}

// --- regressions found auditing the 0.8.0 work ------------------------------

// "parsed" was renamed to "short"; the old name stays accepted so documented
// examples and anything already scripted keep working.
func TestParsedIsAcceptedAsAliasForShort(t *testing.T) {
	cleanEnv(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"Result":{"A":{"Result":"JKWGP"}},"Success":true}`))
	}))
	defer ts.Close()

	for _, format := range []string{"short", "parsed"} {
		out, err := execute(t, "--output", format, "asf", "--url", ts.URL, "token", "A")
		if err != nil {
			t.Fatalf("%s: %v", format, err)
		}
		if strings.TrimSpace(out) != "JKWGP" {
			t.Errorf("--output %s produced %q", format, out)
		}
	}
}

// STEAM_WEB_API_KEY was the documented primary before the rename to
// STEAM_API_KEY, so an environment setting only the old name must still work.
func TestLegacyWebAPIKeyEnvIsStillRead(t *testing.T) {
	cleanEnv(t)
	t.Setenv("STEAM_API_KEY", "")
	t.Setenv("STEAM_WEB_API_KEY", "legacy-key")

	out, err := execute(t, "-o", "json", "doctor")
	if err != nil {
		t.Fatal(err)
	}
	var d map[string]any
	if e := json.Unmarshal([]byte(out), &d); e != nil {
		t.Fatalf("doctor output is not JSON: %v", e)
	}
	if d["web_key_present"] != true {
		t.Errorf("STEAM_WEB_API_KEY should still resolve a key:\n%s", out)
	}
	// And the credential itself must never be echoed.
	if strings.Contains(out, "legacy-key") {
		t.Errorf("doctor leaked the key:\n%s", out)
	}
}

// info must say when a section could not be fetched, rather than omitting it.
func TestInfoReportsSectionFailures(t *testing.T) {
	cleanEnv(t)
	t.Setenv("STEAM_WEB_URL", "http://127.0.0.1:9")

	out, err := execute(t, "--allow-http", "-o", "json", "--timeout", "2s", "info")
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		Problems []string `json:"problems"`
	}
	if e := json.Unmarshal([]byte(out), &d); e != nil {
		t.Fatalf("info output is not JSON: %v\n%s", e, out)
	}
	if len(d.Problems) == 0 {
		t.Errorf("an unreachable Web API should be reported, got:\n%s", out)
	}
}

// Launch options can carry credentials and must not be printed by default.
func TestLibraryCustomRedactsLaunchOptionSecrets(t *testing.T) {
	cleanEnv(t)
	root := t.TempDir()
	steamapps := filepath.Join(root, "steamapps")
	if err := os.MkdirAll(steamapps, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(steamapps, "libraryfolders.vdf"),
		[]byte("\"libraryfolders\"\n{\n\t\"0\"\n\t{\n\t\t\"path\"\t\t\""+root+"\"\n\t}\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(steamapps, "appmanifest_1.acf"),
		[]byte("\"AppState\"\n{\n\t\"appid\"\t\t\"1\"\n\t\"name\"\t\t\"Test\"\n\t\"installdir\"\t\t\"Test\"\n\t\"StateFlags\"\t\t\"4\"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(root, "userdata", "1", "config")
	if err := os.MkdirAll(cfg, 0o755); err != nil {
		t.Fatal(err)
	}
	local := "\"UserLocalConfigStore\"\n{\n\t\"Software\"\n\t{\n\t\t\"Valve\"\n\t\t{\n\t\t\t\"Steam\"\n\t\t\t{\n\t\t\t\t\"apps\"\n\t\t\t\t{\n\t\t\t\t\t\"1\"\n\t\t\t\t\t{\n\t\t\t\t\t\t\"LaunchOptions\"\t\t\"-novid -rcon_password hunter2000\"\n\t\t\t\t\t}\n\t\t\t\t}\n\t\t\t}\n\t\t}\n\t}\n}\n"
	if err := os.WriteFile(filepath.Join(cfg, "localconfig.vdf"), []byte(local), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := execute(t, "--offline", "-o", "json", "library", "custom", "--root", root)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "hunter2000") {
		t.Errorf("a credential in launch options leaked:\n%s", out)
	}
	if !strings.Contains(out, "redacted") {
		t.Errorf("expected a redaction marker:\n%s", out)
	}
	if !strings.Contains(out, "-novid") {
		t.Errorf("ordinary options should survive:\n%s", out)
	}

	shown, err := execute(t, "--offline", "-o", "json", "library", "custom", "--root", root, "--show-secrets")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(shown, "hunter2000") {
		t.Errorf("--show-secrets should reveal the value:\n%s", shown)
	}
}

// All three commands that fall back to "the logged-in user" must agree. search
// had lost the desktop-client fallback, so it resolved a different user from
// web and info on the same machine.
func TestCurrentUserResolutionPrecedence(t *testing.T) {
	cleanEnv(t)

	// 1. An explicit STEAM_USER_ID wins.
	t.Setenv("STEAM_USER_ID", "76561197960287930")
	o := &options{}
	id, err := o.currentUserID()
	if err != nil || id != "76561197960287930" {
		t.Fatalf("explicit id = %q, %v", id, err)
	}

	// 2. Otherwise the SteamID in the Community cookie.
	t.Setenv("STEAM_USER_ID", "")
	t.Setenv("STEAM_LOGIN_SECURE", "76561198000000001%7C%7Ctoken")
	o = &options{}
	id, err = o.currentUserID()
	if err != nil || id != "76561198000000001" {
		t.Fatalf("cookie id = %q, %v", id, err)
	}

	// 3. With neither, the error names both variables and the desktop client.
	t.Setenv("STEAM_LOGIN_SECURE", "")
	t.Setenv("HOME", t.TempDir())
	o = &options{}
	if _, err = o.currentUserID(); err == nil {
		t.Fatal("expected an error when nothing identifies a user")
	}
	for _, want := range []string{"STEAM_USER_ID", "STEAM_LOGIN_SECURE", "desktop"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q: %v", want, err)
		}
	}
}

// CSV exists to be parsed, so numeric columns must not carry the grouping and
// units that make a table readable.
func TestCSVEmitsMachineReadableNumbers(t *testing.T) {
	cleanEnv(t)
	root := t.TempDir()
	steamapps := filepath.Join(root, "steamapps")
	if err := os.MkdirAll(steamapps, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(steamapps, "libraryfolders.vdf"),
		[]byte("\"libraryfolders\"\n{\n\t\"0\"\n\t{\n\t\t\"path\"\t\t\""+root+"\"\n\t}\n}\n"), 0o644)
	os.WriteFile(filepath.Join(steamapps, "appmanifest_1.acf"),
		[]byte("\"AppState\"\n{\n\t\"appid\"\t\t\"1\"\n\t\"name\"\t\t\"Test\"\n\t\"installdir\"\t\t\"T\"\n\t\"StateFlags\"\t\t\"4\"\n\t\"SizeOnDisk\"\t\t\"35020224\"\n}\n"), 0o644)

	csvOut, err := execute(t, "--offline", "-o", "csv", "library", "--root", root)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(csvOut)).ReadAll()
	if err != nil {
		t.Fatalf("output is not valid CSV: %v\n%s", err, csvOut)
	}
	if len(rows) < 2 {
		t.Fatalf("expected a header and a row:\n%s", csvOut)
	}
	if !strings.Contains(csvOut, "35020224") {
		t.Errorf("CSV should carry raw bytes, got:\n%s", csvOut)
	}
	if strings.Contains(csvOut, "MiB") {
		t.Errorf("CSV should not carry human units, got:\n%s", csvOut)
	}

	// The table form keeps the readable units.
	tableOut, err := execute(t, "--offline", "library", "--root", root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tableOut, "MiB") {
		t.Errorf("table output should be human-readable, got:\n%s", tableOut)
	}
}

func TestCSVHeaderToggle(t *testing.T) {
	cleanEnv(t)
	with, err := execute(t, "--offline", "-o", "csv", "id", "76561197960287930")
	if err != nil {
		t.Fatal(err)
	}
	without, err := execute(t, "--offline", "-o", "csv", "--with-header=false", "id", "76561197960287930")
	if err != nil {
		t.Fatal(err)
	}
	if len(strings.Split(strings.TrimSpace(without), "\n")) > len(strings.Split(strings.TrimSpace(with), "\n")) {
		t.Error("--with-header=false should not add lines")
	}
	if !strings.Contains(without, "76561197960287930") {
		t.Errorf("data should survive without a header:\n%s", without)
	}
}

// --- server ---------------------------------------------------------------

func serverFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	p := filepath.Join(root, "userdata", "1", "7", "remote", "serverbrowser_hist.vdf")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `"Filters"
{
	"favorites"
	{
		"1" { "name" "Alpha" "address" "10.0.0.1:27015" "LastPlayed" "1529218286" "appid" "440" "accountid" "0" }
		"2" { "name" "Beta" "address" "10.0.0.2:27015" "LastPlayed" "0" "appid" "0" "accountid" "0" }
	}
	"history"
	{
		"1" { "name" "Gamma" "address" "10.0.0.3:27015" "LastPlayed" "1600000000" "appid" "730" "accountid" "0" }
	}
}
`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestServerFavoritesAndHistory(t *testing.T) {
	cleanEnv(t)
	root := serverFixture(t)

	out, err := execute(t, "-o", "json", "server", "favorites", "--root", root)
	if err != nil {
		t.Fatal(err)
	}
	var favs []map[string]any
	if e := json.Unmarshal([]byte(out), &favs); e != nil {
		t.Fatalf("not JSON: %v\n%s", e, out)
	}
	if len(favs) != 2 || favs[0]["address"] != "10.0.0.1:27015" {
		t.Fatalf("favorites = %+v", favs)
	}

	out, err = execute(t, "-o", "json", "server", "history", "--root", root)
	if err != nil {
		t.Fatal(err)
	}
	var hist []map[string]any
	json.Unmarshal([]byte(out), &hist)
	if len(hist) != 1 || hist[0]["address"] != "10.0.0.3:27015" {
		t.Fatalf("history = %+v", hist)
	}
}

// Adding and removing must leave the other list untouched and reject bad input.
func TestServerAddRemoveRoundTrip(t *testing.T) {
	previous := steamRunning
	steamRunning = func() bool { return false }
	defer func() { steamRunning = previous }()
	cleanEnv(t)
	root := serverFixture(t)

	// --name avoids querying the network for a label.
	if _, err := execute(t, "server", "add", "10.0.0.9:27015", "--name", "Added", "--root", root); err != nil {
		t.Fatal(err)
	}
	out, _ := execute(t, "-o", "json", "server", "favorites", "--root", root)
	var favs []map[string]any
	json.Unmarshal([]byte(out), &favs)
	if len(favs) != 3 || favs[2]["name"] != "Added" {
		t.Fatalf("after add: %+v", favs)
	}

	// A duplicate is refused rather than silently doubling the entry.
	if _, err := execute(t, "server", "add", "10.0.0.9:27015", "--name", "x", "--root", root); err == nil {
		t.Error("adding a duplicate should fail")
	}

	// Remove by index.
	if _, err := execute(t, "server", "remove", "3", "--root", root); err != nil {
		t.Fatal(err)
	}
	out, _ = execute(t, "-o", "json", "server", "favorites", "--root", root)
	json.Unmarshal([]byte(out), &favs)
	if len(favs) != 2 {
		t.Fatalf("after remove: %+v", favs)
	}

	// History survived both writes.
	out, _ = execute(t, "-o", "json", "server", "history", "--root", root)
	var hist []map[string]any
	json.Unmarshal([]byte(out), &hist)
	if len(hist) != 1 {
		t.Errorf("history was disturbed: %+v", hist)
	}

	// Out-of-range and unknown addresses are reported, not ignored.
	if _, err := execute(t, "server", "remove", "99", "--root", root); err == nil {
		t.Error("an out-of-range index should fail")
	}
	if _, err := execute(t, "server", "remove", "10.0.0.250:27015", "--root", root); err == nil {
		t.Error("removing an address that is not a favourite should fail")
	}
}

func TestServerBrowseRequiresAFilter(t *testing.T) {
	cleanEnv(t)
	t.Setenv("STEAM_API_KEY", "k")
	_, err := execute(t, "server", "browse")
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("an unfiltered browse should be refused, got %v", err)
	}
}

func TestServerBrowseRendersMasterList(t *testing.T) {
	cleanEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Query().Get("filter"), `\appid\730`) {
			t.Errorf("filter = %q", r.URL.Query().Get("filter"))
		}
		w.Write([]byte(`{"response":{"servers":[
		  {"addr":"1.2.3.4:27015","name":"Quiet","map":"de_dust2","players":2,"max_players":10,"secure":true},
		  {"addr":"5.6.7.8:27015","name":"Busy","map":"de_mirage","players":9,"max_players":10,"secure":false}]}}`))
	}))
	defer srv.Close()
	t.Setenv("STEAM_API_KEY", "k")
	t.Setenv("STEAM_WEB_URL", srv.URL)

	out, err := execute(t, "--allow-http", "server", "browse", "730")
	if err != nil {
		t.Fatal(err)
	}
	// Busiest first, so the useful servers are at the top.
	if strings.Index(out, "Busy") > strings.Index(out, "Quiet") {
		t.Errorf("servers should be sorted by player count:\n%s", out)
	}
	if !strings.Contains(out, "9/10") {
		t.Errorf("player counts missing:\n%s", out)
	}
}

func TestServerAddressDefaultsToQueryPort(t *testing.T) {
	if got := withDefaultPort("10.0.0.1", 27015); got != "10.0.0.1:27015" {
		t.Errorf("withDefaultPort = %q", got)
	}
	if got := withDefaultPort("10.0.0.1:27020", 27015); got != "10.0.0.1:27020" {
		t.Errorf("an explicit port should be kept, got %q", got)
	}
}

func TestAccountAndIdleCLI(t *testing.T) {
	cleanEnv(t)
	// Test account list & whoami
	out, err := execute(t, "account", "list")
	if err != nil {
		t.Fatalf("account list error: %v", err)
	}
	if !strings.Contains(out, "PERSONA NAME") || !strings.Contains(out, "ACCOUNT NAME") {
		t.Errorf("account list output missing headers:\n%s", out)
	}

	out, err = execute(t, "whoami")
	if err != nil {
		t.Fatalf("whoami error: %v", err)
	}
	if !strings.Contains(out, "Persona Name") {
		t.Errorf("whoami output missing Persona Name:\n%s", out)
	}

	// Test idle help / validation
	_, err = execute(t, "idle")
	if err != nil {
		t.Fatalf("idle without args should show help, got error: %v", err)
	}

	// Test idle stop
	out, err = execute(t, "idle", "stop")
	if err != nil {
		t.Fatalf("idle stop error: %v", err)
	}
	if !strings.Contains(out, "stopped") && !strings.Contains(out, "No active") {
		t.Errorf("unexpected idle stop output:\n%s", out)
	}
}
