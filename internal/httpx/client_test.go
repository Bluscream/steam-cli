package httpx

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestTransportDoesNotLeakKey(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	s.Close()
	c := New(time.Second, false, false)
	_, e := c.Do(context.Background(), "GET", s.URL, url.Values{"key": {"highly-secret"}}, nil, nil)
	if e == nil || strings.Contains(e.Error(), "highly-secret") || strings.Contains(e.Error(), s.URL) {
		t.Fatalf("unsafe error: %v", e)
	}
}
func TestRedirectNeverForwardsCredentials(t *testing.T) {
	var hits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer target.Close()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer s.Close()
	_, e := New(time.Second, false, false).Do(context.Background(), "GET", s.URL, nil, nil, http.Header{"Authentication": {"secret"}})
	var status *StatusError
	if !errors.As(e, &status) || status.Code != 302 || hits.Load() != 0 {
		t.Fatalf("redirect followed: %v, %d", e, hits.Load())
	}
}
func TestOfflineNoRequest(t *testing.T) {
	var hits atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer s.Close()
	_, e := New(time.Second, true, false).Do(context.Background(), "GET", s.URL, nil, nil, nil)
	if e == nil || hits.Load() != 0 {
		t.Fatal("offline request sent")
	}
}
func TestRetryGETOnly(t *testing.T) {
	for _, verb := range []string{"GET", "POST"} {
		t.Run(verb, func(t *testing.T) {
			n := 0
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n++
				if n == 1 {
					w.Header().Set("Retry-After", "0")
					w.WriteHeader(503)
					return
				}
				w.Write([]byte(`{}`))
			}))
			defer s.Close()
			_, e := New(time.Second, false, false).Do(context.Background(), verb, s.URL, nil, nil, nil)
			if verb == "GET" && (e != nil || n != 2) {
				t.Fatalf("GET retry: %v, %d", e, n)
			}
			if verb == "POST" && (e == nil || n != 1) {
				t.Fatalf("POST replayed: %v, %d", e, n)
			}
		})
	}
}
func TestURLPolicy(t *testing.T) {
	for _, u := range []string{"http://example.com", "https://user:pass@example.com", "https://example.com?key=secret", "file:///tmp/a"} {
		if _, e := ValidateURL(u, false); e == nil {
			t.Errorf("accepted %s", u)
		}
	}
	for _, p := range []string{"../Api", "%2e%2e/Api", "//evil.com/../Api", "Api/%5c..%5cx", "Api/a?key=x"} {
		if _, e := Endpoint("https://example.com/proxy", p, false); e == nil {
			t.Errorf("accepted %s", p)
		}
	}
	u, e := Endpoint("https://example.com/proxy/", "Api/Bot/A%20B", false)
	if e != nil || u != "https://example.com/proxy/Api/Bot/A%20B" {
		t.Fatalf("prefix: %s %v", u, e)
	}
}
func TestHTTPErrorHidesResponseBody(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403); w.Write([]byte("echoed-password")) }))
	defer s.Close()
	_, e := New(time.Second, false, false).Do(context.Background(), "GET", s.URL, nil, nil, nil)
	if e == nil || strings.Contains(e.Error(), "echoed-password") {
		t.Fatal(e)
	}
}
func TestResponseLimit(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chunk := make([]byte, 1<<20)
		for i := 0; i < 33; i++ {
			if _, e := w.Write(chunk); e != nil {
				return
			}
		}
	}))
	defer s.Close()
	_, e := New(5*time.Second, false, false).Do(context.Background(), "GET", s.URL, nil, nil, nil)
	if e == nil || !strings.Contains(e.Error(), "32 MiB") {
		t.Fatal(e)
	}
}
