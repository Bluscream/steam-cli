package asf

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"steamcli.local/steam/internal/httpx"
	"testing"
	"time"
)

func TestCommandAuthenticationAndBody(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/proxy/Api/Command" || r.URL.RawQuery != "" || r.Header.Get("Authentication") != "password" {
			t.Error("bad IPC request")
		}
		var d map[string]string
		if e := json.NewDecoder(r.Body).Decode(&d); e != nil || d["Command"] != "status ASF" {
			t.Error("bad command body")
		}
		w.Write([]byte(`{"Success":true,"Result":"ok"}`))
	}))
	defer s.Close()
	c := Client{HTTP: httpx.New(time.Second, false, false), BaseURL: s.URL + "/proxy", Password: "password"}
	if _, e := c.Command(context.Background(), "status ASF"); e != nil {
		t.Fatal(e)
	}
}
func TestApplicationFailure(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"Success":false,"Message":"sensitive detail"}`))
	}))
	defer s.Close()
	c := Client{HTTP: httpx.New(time.Second, false, false), BaseURL: s.URL}
	b, e := c.Call(context.Background(), "GET", "Api/ASF", nil, nil)
	if e == nil || len(b) == 0 {
		t.Fatal("application failure lost")
	}
}
func TestBotSelector(t *testing.T) {
	for _, s := range []string{"", "../ASF", "ASF/Stop", "a?password=x", "a%2fb", "a\nb"} {
		if _, e := BotPath(s, "Start"); e == nil {
			t.Errorf("accepted %q", s)
		}
	}
	if p, e := BotPath("A B,C", "Start"); e != nil || p != "Api/Bot/A%20B%2CC/Start" { // Go leaves commas unescaped in path segments.
		if e != nil || p != "Api/Bot/A%20B,C/Start" {
			t.Fatalf("selector %s %v", p, e)
		}
	}
}
