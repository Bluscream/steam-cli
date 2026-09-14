package webapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"steamcli.local/steam/internal/httpx"
	"strings"
	"testing"
	"time"
)

const catalogJSON = `{"apilist":{"interfaces":[{"name":"ITest","methods":[{"name":"Read","version":1,"httpmethod":"GET","parameters":[]},{"name":"Read","version":2,"httpmethod":"GET","parameters":[]},{"name":"Write","version":1,"httpmethod":"POST","parameters":[]}]}]}}`

func TestWireEncoding(t *testing.T) {
	for _, verb := range []string{"GET", "POST"} {
		t.Run(verb, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != verb || r.URL.Path != "/proxy/ITest/Write/v3/" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				if verb == "POST" && (r.URL.RawQuery != "" || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded") {
					t.Error("POST query or content type")
				}
				r.ParseForm()
				if r.Form.Get("key") != "secret" || r.Form.Get("text") != "a+b & 日本" || len(r.Form["x"]) != 2 {
					t.Errorf("bad form: keys=%d", len(r.Form))
				}
				w.Write([]byte(`{"id":76561198000000001}`))
			}))
			defer s.Close()
			c := Client{HTTP: httpx.New(time.Second, false, false), BaseURL: s.URL + "/proxy", Key: "secret"}
			p := url.Values{"text": {"a+b & 日本"}, "x": {"a", "b"}}
			b, e := c.Call(context.Background(), "ITest", "Write", 3, verb, p)
			if e != nil {
				t.Fatal(e)
			}
			if !strings.Contains(string(b), "76561198000000001") || p.Has("key") {
				t.Fatal("ID changed or caller parameters mutated")
			}
		})
	}
}
func TestCatalogCacheAndResolution(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(catalogJSON)) }))
	defer s.Close()
	c := Client{HTTP: httpx.New(time.Second, false, false), BaseURL: s.URL, Key: "cache-secret", CacheDir: t.TempDir()}
	v, e := c.Catalog(context.Background(), false)
	if e != nil {
		t.Fatal(e)
	}
	m, e := v.Resolve("ITest", "Read", 0)
	if e != nil || m.Version != 2 {
		t.Fatalf("resolve %v %v", m, e)
	}
	if _, e = v.Resolve("ITest", "Missing", 0); e == nil {
		t.Fatal("missing method accepted")
	}
	b, e := os.ReadFile(c.cachePath())
	if e != nil || strings.Contains(string(b), c.Key) || strings.Contains(c.cachePath(), c.Key) {
		t.Fatal("cache leaked key")
	}
	c.HTTP.Offline = true
	if _, e = c.Catalog(context.Background(), true); e != nil {
		t.Fatal(e)
	}
	c.Key = "other"
	if _, e = c.Catalog(context.Background(), false); e == nil {
		t.Fatal("cache not scoped to key")
	}
}
func TestRejectPathInjection(t *testing.T) {
	c := Client{HTTP: httpx.New(time.Second, false, false), BaseURL: "https://api.steampowered.com"}
	for _, iface := range []string{"../ITest", "ITest?key=x", "ITest/Other"} {
		if _, e := c.Call(context.Background(), iface, "Read", 1, "GET", nil); e == nil {
			t.Fatal("path accepted")
		}
	}
}

func TestExtendedCatalog(t *testing.T) {
	cat, err := ExtendedCatalog()
	if err != nil {
		t.Fatalf("ExtendedCatalog failed: %v", err)
	}
	if len(cat.APIList.Interfaces) == 0 {
		t.Fatal("ExtendedCatalog returned 0 interfaces")
	}
}

