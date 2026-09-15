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

func TestParseShapes(t *testing.T) {
	cases := []struct {
		name string
		body string
		want []ParsedLine
		ok   bool
	}{{
		name: "two-factor token nested under the bot",
		body: `{"Result":{"gabeN":{"Result":"JKWGP","Message":"Success!","Success":true}},"Message":"OK","Success":true}`,
		want: []ParsedLine{{"gabeN", "JKWGP"}},
		ok:   true,
	}, {
		name: "several bots come back sorted",
		body: `{"Result":{"Zed":{"Result":"22222"},"Abe":{"Result":"11111"}},"Success":true}`,
		want: []ParsedLine{{"Abe", "11111"}, {"Zed", "22222"}},
		ok:   true,
	}, {
		name: "executed command is a bare string",
		body: `{"Result":"<Bot> Bot is not farming anything.","Message":"OK","Success":true}`,
		want: []ParsedLine{{"", "<Bot> Bot is not farming anything."}},
		ok:   true,
	}, {
		name: "no token falls back to the bot's message",
		body: `{"Result":{"erikjohnson":{"Result":null,"Message":"Bot is not connected.","Success":false}},"Success":false}`,
		want: []ParsedLine{{"erikjohnson", "Bot is not connected."}},
		ok:   true,
	}, {
		name: "no per-bot detail falls back to the envelope message",
		body: `{"Result":{},"Message":"Automatic farming is resumed already!","Success":false}`,
		want: []ParsedLine{{"", "Automatic farming is resumed already!"}},
		ok:   true,
	}, {
		name: "bot object without a scalar result uses its message",
		body: `{"Result":{"A":{"BotName":"A","Nested":{"x":1},"Message":"ok"}},"Success":true}`,
		want: []ParsedLine{{"A", "ok"}},
		ok:   true,
	}, {
		name: "numbers and booleans render as text",
		body: `{"Result":{"A":{"Result":42},"B":{"Result":true}},"Success":true}`,
		want: []ParsedLine{{"A", "42"}, {"B", "true"}},
		ok:   true,
	}, {
		name: "not an ASF envelope",
		body: `{"something":"else"}`,
		ok:   false,
	}, {
		name: "not JSON at all",
		body: `<html>`,
		ok:   false,
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := Parse([]byte(tc.body))
			if ok != tc.ok {
				t.Fatalf("recognized = %v, want %v", ok, tc.ok)
			}
			if !tc.ok {
				return
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("line %d = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// A value must never be inferred from an unrelated field. An entry carrying
// neither a scalar Result nor a Message has no outcome to report, so the
// response is reported as unparsed rather than rendered as a blank line.
func TestParseDoesNotInventValues(t *testing.T) {
	if _, ok := Parse([]byte(`{"Result":{"A":{"Success":false}},"Message":"","Success":false}`)); ok {
		t.Error("an entry with no result and no message should not be claimed as parsed")
	}
}

// Endpoints that return a rich object per bot must not be reduced to one line:
// doing so would discard the data the caller asked for.
func TestParseDeclinesDataObjects(t *testing.T) {
	botListing := `{"Result":{"Alpha":{"BotName":"Alpha","IsConnectedAndLoggedOn":true,
	  "CardsFarmer":{"Paused":false},"SteamID":"76561197960287930"}},"Message":"OK","Success":true}`
	if _, ok := Parse([]byte(botListing)); ok {
		t.Error("a per-bot data object should be reported as unparsed so it prints in full")
	}

	asfStatus := `{"Result":{"BuildVariant":"linux-x64","GlobalConfig":{"IPC":true},"Version":"6.3.10.1"},
	  "Message":"OK","Success":true}`
	if _, ok := Parse([]byte(asfStatus)); ok {
		t.Error("the ASF status object should be reported as unparsed")
	}
}

func TestBotNameForSteamID(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/proxy/Api/Bot/ASF" {
			w.Write([]byte(`{"Success":true,"Result":{"MyBot":{"BotName":"MyBot","s_SteamID":"76561198000000001","IsConnectedAndLoggedOn":true,"CardsFarmer":{"GamesToFarm":[]}},"OtherBot":{"BotName":"OtherBot","s_SteamID":"76561198000000002","IsConnectedAndLoggedOn":true,"CardsFarmer":{"GamesToFarm":[]}}}}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer s.Close()

	c := Client{HTTP: httpx.New(time.Second, false, false), BaseURL: s.URL + "/proxy"}
	bot, err := c.BotNameForSteamID(context.Background(), "76561198000000001")
	if err != nil || bot != "MyBot" {
		t.Fatalf("expected MyBot, got %s, err: %v", bot, err)
	}

	bot2, err := c.BotNameForSteamID(context.Background(), "76561198000000002")
	if err != nil || bot2 != "OtherBot" {
		t.Fatalf("expected OtherBot, got %s, err: %v", bot2, err)
	}

	_, err = c.BotNameForSteamID(context.Background(), "99999999999999999")
	if err == nil {
		t.Fatal("expected error for non-matching steam id")
	}
}

