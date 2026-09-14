package cli

import (
	"bytes"
	"context"
	"encoding/json"
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
		w.Write([]byte(`{"Result":{"Version":"6.0.1.2","ProcessID":1234,"MemoryUsage":104857600,"ProcessStartTime":"2026-09-14T12:00:00Z","BotsCount":3,"BuildVariant":"generic"},"Success":true}`))
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


