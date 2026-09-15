package status

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"steamcli.local/steam/internal/httpx"
)

func testClient() *httpx.Client { return httpx.New(5*time.Second, false, true) }

// steamAPI serves the Web API shapes the monitor depends on.
func steamAPI(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "GetNumberOfCurrentPlayers"):
			appid := r.URL.Query().Get("appid")
			if appid == "570" {
				json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"result": 42}})
				return
			}
			json.NewEncoder(w).Encode(map[string]any{
				"response": map[string]any{"player_count": 1234567, "result": 1}})
		case strings.Contains(r.URL.Path, "GetCMList"):
			json.NewEncoder(w).Encode(map[string]any{
				"response": map[string]any{"serverlist": []string{"127.0.0.1:1", "127.0.0.1:2"}}})
		case strings.Contains(r.URL.Path, "GetGameServersStatus"):
			if r.URL.Query().Get("key") == "" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{
				"services":    map[string]string{"SessionsLogon": "normal", "IEconItems": "offline"},
				"datacenters": map[string]any{"EU": map[string]any{"capacity": "high", "load": "low"}},
			}})
		case strings.Contains(r.URL.Path, "GetServerInfo"):
			json.NewEncoder(w).Encode(map[string]any{"servertime": 1})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestCheckOfflineFails(t *testing.T) {
	m := &Monitor{HTTP: httpx.New(time.Second, true, false)}
	if _, err := m.Check(context.Background(), false, false); err == nil {
		t.Fatal("expected an error in offline mode")
	}
}

func TestCheckRequiresClient(t *testing.T) {
	m := &Monitor{}
	if _, err := m.Check(context.Background(), false, false); err == nil {
		t.Fatal("expected an error without an HTTP client")
	}
}

func TestFetchPlayerCount(t *testing.T) {
	ts := steamAPI(t)
	defer ts.Close()

	m := &Monitor{HTTP: testClient(), WebAPIURL: ts.URL}
	n, err := m.fetchPlayerCount(context.Background(), 730)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 1234567 {
		t.Errorf("player count = %d, want 1234567", n)
	}
}

func TestFetchPlayerCountUnavailable(t *testing.T) {
	ts := steamAPI(t)
	defer ts.Close()

	m := &Monitor{HTTP: testClient(), WebAPIURL: ts.URL}
	// AppID 570 is wired to answer with a non-OK result code.
	if _, err := m.fetchPlayerCount(context.Background(), 570); err == nil {
		t.Fatal("expected an error when Steam reports result != 1")
	}
}

func TestProbeHTTPStatuses(t *testing.T) {
	var code atomic.Int32
	code.Store(http.StatusOK)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(int(code.Load()))
	}))
	defer ts.Close()

	m := &Monitor{HTTP: testClient()}

	if got := m.probeHTTP(context.Background(), "t", ts.URL+"/"); got.Status != "normal" || got.HTTPCode != 200 {
		t.Errorf("200 => %+v, want normal/200", got)
	}

	// A 5xx means the host is up but the service is not.
	code.Store(http.StatusServiceUnavailable)
	if got := m.probeHTTP(context.Background(), "t", ts.URL+"/"); got.Status != "down" || got.HTTPCode != 503 {
		t.Errorf("503 => %+v, want down/503", got)
	}

	// A 4xx proves reachability, but cannot establish service health.
	code.Store(http.StatusNotFound)
	if got := m.probeHTTP(context.Background(), "t", ts.URL+"/"); got.Status != "error" || got.HTTPCode != 404 {
		t.Errorf("404 => %+v, want error/404", got)
	}
}

func TestProbeHTTPUnreachable(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := ts.URL
	ts.Close() // nothing is listening now

	m := &Monitor{HTTP: testClient()}
	got := m.probeHTTP(context.Background(), "t", url+"/")
	if got.Status != "down" || got.Error == "" {
		t.Errorf("closed server => %+v, want down with an error", got)
	}
}

func TestProbeSocket(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()

	if got := probeSocket(context.Background(), ln.Addr().String()); got.Status != "online" {
		t.Errorf("listening socket => %+v, want online", got)
	}

	// Port 1 on loopback refuses connections.
	if got := probeSocket(context.Background(), "127.0.0.1:1"); got.Status != "unreachable" || got.Error == "" {
		t.Errorf("closed port => %+v, want unreachable with an error", got)
	}
}

func TestProbeCMs(t *testing.T) {
	ts := steamAPI(t)
	defer ts.Close()

	m := &Monitor{HTTP: testClient(), WebAPIURL: ts.URL}
	cms, err := m.probeCMs(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cms) != 2 {
		t.Fatalf("got %d CMs, want 2", len(cms))
	}
	for _, cm := range cms {
		// Both fixtures point at refused ports; the probe must still report them.
		if cm.Status != "unreachable" || cm.Server == "" {
			t.Errorf("unexpected CM result: %+v", cm)
		}
	}
}

func TestProbeCMsRespectsLimit(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		list := make([]string, 20)
		for i := range list {
			list[i] = fmt.Sprintf("127.0.0.1:%d", i+1)
		}
		json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"serverlist": list}})
	}))
	defer ts.Close()

	m := &Monitor{HTTP: testClient(), WebAPIURL: ts.URL, CMLimit: 3}
	cms, err := m.probeCMs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(cms) != 3 {
		t.Errorf("got %d CMs, want 3 (CMLimit)", len(cms))
	}
}

func TestFetchGameServersStatus(t *testing.T) {
	ts := steamAPI(t)
	defer ts.Close()

	m := &Monitor{HTTP: testClient(), WebAPIURL: ts.URL, WebKey: "k"}
	c, err := m.fetchGameServersStatus(context.Background(), 730)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Services["SessionsLogon"] != "normal" {
		t.Errorf("services = %v", c.Services)
	}
	if len(c.Datacenters) != 1 {
		t.Errorf("datacenters = %v, want one region", c.Datacenters)
	}
}

func TestCheckAggregates(t *testing.T) {
	ts := steamAPI(t)
	defer ts.Close()

	m := &Monitor{
		HTTP:         testClient(),
		WebAPIURL:    ts.URL,
		StoreURL:     ts.URL,
		CommunityURL: ts.URL,
		HelpURL:      ts.URL,
		WebKey:       "k",
		Apps:         []TrackedApp{{"CS2", 730}, {"Dota 2", 570}},
	}

	report, err := m.Check(context.Background(), true, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(report.Endpoints) != 4 {
		t.Errorf("got %d endpoints, want 4", len(report.Endpoints))
	}
	if len(report.PlayerCounts) != 2 {
		t.Fatalf("got %d player counts, want 2", len(report.PlayerCounts))
	}
	// One app succeeds and one fails; both must be represented.
	if report.PlayerCounts[0].Count != 1234567 || report.PlayerCounts[0].Error != "" {
		t.Errorf("CS2 count = %+v", report.PlayerCounts[0])
	}
	if report.PlayerCounts[1].Error == "" {
		t.Errorf("Dota 2 should carry an error, got %+v", report.PlayerCounts[1])
	}
	if len(report.Coordinators) != 1 || report.Coordinators[0].Services["IEconItems"] != "offline" {
		t.Errorf("coordinators = %+v", report.Coordinators)
	}
	if len(report.ConnectionManagers) != 2 {
		t.Errorf("got %d CMs, want 2", len(report.ConnectionManagers))
	}
	if report.Timestamp.IsZero() {
		t.Error("report timestamp not set")
	}
}

func TestCheckWarnsWithoutKey(t *testing.T) {
	ts := steamAPI(t)
	defer ts.Close()

	m := &Monitor{HTTP: testClient(), WebAPIURL: ts.URL, StoreURL: ts.URL,
		CommunityURL: ts.URL, HelpURL: ts.URL, Apps: []TrackedApp{{"CS2", 730}}}

	report, err := m.Check(context.Background(), false, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Coordinators) != 0 {
		t.Error("coordinator status should be skipped without a key")
	}
	if len(report.Warnings) == 0 {
		t.Error("skipping the coordinator must be reported as a warning")
	}
}

func TestReportSerializesCleanly(t *testing.T) {
	ts := steamAPI(t)
	defer ts.Close()

	m := &Monitor{HTTP: testClient(), WebAPIURL: ts.URL, StoreURL: ts.URL,
		CommunityURL: ts.URL, HelpURL: ts.URL, Apps: []TrackedApp{{"CS2", 730}}}
	report, err := m.Check(context.Background(), false, false)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"latency":`) {
		t.Error("report should not expose raw nanosecond durations")
	}
}

func TestCMFailureIsReported(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{}`)) }))
	defer ts.Close()
	m := Monitor{HTTP: testClient(), WebAPIURL: ts.URL, StoreURL: ts.URL, CommunityURL: ts.URL, HelpURL: ts.URL, Apps: []TrackedApp{{"test", 42}}}
	r, e := m.Check(context.Background(), true, false)
	if e != nil {
		t.Fatal(e)
	}
	if len(r.Warnings) != 1 || !strings.Contains(r.Warnings[0], "connection manager") {
		t.Fatal(r.Warnings)
	}
}
