package community

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"steamcli.local/steam/internal/httpx"
)

const fakeCookie = "76561197960287930%7C%7CeyJhbGciOiJF.signature"

func newClient(base string) *Client {
	return &Client{
		HTTP:        httpx.New(5*time.Second, false, true),
		BaseURL:     base,
		LoginSecure: fakeCookie,
	}
}

func TestSteamIDFromCookie(t *testing.T) {
	c := newClient("http://example.invalid")
	id, err := c.SteamID()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "76561197960287930" {
		t.Errorf("SteamID = %q", id)
	}
}

func TestSteamIDRejectsMalformed(t *testing.T) {
	c := &Client{LoginSecure: "not-a-steamid||token"}
	if _, err := c.SteamID(); err == nil {
		t.Fatal("expected an error for a malformed cookie")
	}
}

func TestSteamIDWithoutSession(t *testing.T) {
	c := &Client{}
	_, err := c.SteamID()
	if !errors.Is(err, ErrNoSession) {
		t.Fatalf("err = %v, want ErrNoSession", err)
	}
}

func TestAddChildSendsSessionAndForm(t *testing.T) {
	var gotForm url.Values
	var gotCookie, gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		gotForm = r.PostForm
		gotCookie = r.Header.Get("Cookie")
		gotPath = r.URL.Path
		w.Write([]byte(`{"success":1}`))
	}))
	defer ts.Close()

	c := newClient(ts.URL)
	if err := c.AddChild(context.Background(), "111", "222"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotPath != "/sharedfiles/addchild/" {
		t.Errorf("path = %q", gotPath)
	}
	if gotForm.Get("parentid") != "111" || gotForm.Get("childid") != "222" {
		t.Errorf("form = %v", gotForm)
	}
	// The CSRF token must be double-submitted: cookie and form must agree.
	if gotForm.Get("sessionid") == "" {
		t.Error("sessionid missing from the form")
	}
	if !strings.Contains(gotCookie, "sessionid="+gotForm.Get("sessionid")) {
		t.Errorf("cookie %q does not carry the form sessionid", gotCookie)
	}
	if !strings.Contains(gotCookie, "steamLoginSecure="+fakeCookie) {
		t.Errorf("cookie %q missing steamLoginSecure", gotCookie)
	}
}

func TestRemoveChild(t *testing.T) {
	var gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Write([]byte(`1`))
	}))
	defer ts.Close()

	if err := newClient(ts.URL).RemoveChild(context.Background(), "1", "2"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotPath != "/sharedfiles/removechild/" {
		t.Errorf("path = %q", gotPath)
	}
}

func TestDeleteFile(t *testing.T) {
	var gotForm url.Values
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		gotForm = r.PostForm
		w.Write([]byte(`{"success":1}`))
	}))
	defer ts.Close()

	if err := newClient(ts.URL).DeleteFile(context.Background(), 4000, "999"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotForm.Get("id") != "999" || gotForm.Get("appid") != "4000" {
		t.Errorf("form = %v", gotForm)
	}
}

func TestMutationWithoutSession(t *testing.T) {
	c := &Client{HTTP: httpx.New(time.Second, false, true), BaseURL: "http://example.invalid"}
	if err := c.AddChild(context.Background(), "1", "2"); !errors.Is(err, ErrNoSession) {
		t.Fatalf("err = %v, want ErrNoSession", err)
	}
}

func TestExpiredSessionIsReported(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Steam bounces unauthenticated writes to the login page.
		http.Redirect(w, r, "https://steamcommunity.com/login/home/", http.StatusFound)
	}))
	defer ts.Close()

	err := newClient(ts.URL).AddChild(context.Background(), "1", "2")
	if !errors.Is(err, ErrNoSession) {
		t.Fatalf("err = %v, want it to wrap ErrNoSession", err)
	}
}

func TestInterpretOutcomes(t *testing.T) {
	cases := []struct {
		name string
		body string
		ok   bool
	}{
		{"empty body", "", true},
		{"bare one", "1", true},
		{"bare eresult", "15", false},
		{"success json", `{"success":1}`, true},
		{"failure json", `{"success":8}`, false},
		{"failure with message", `{"success":8,"message":"nope"}`, false},
		{"html login page", "<!DOCTYPE html><html>", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := interpret([]byte(tc.body))
			if tc.ok && err != nil {
				t.Errorf("expected success, got %v", err)
			}
			if !tc.ok && err == nil {
				t.Error("expected a failure")
			}
		})
	}
}

func TestInterpretSurfacesMessage(t *testing.T) {
	err := interpret([]byte(`{"success":8,"message":"item does not exist"}`))
	if err == nil || !strings.Contains(err.Error(), "item does not exist") {
		t.Fatalf("err = %v, want Steam's message preserved", err)
	}
}

// workshopPage renders the item-tile markup the listing scraper anchors on.
func workshopPage(ids ...string) string {
	var sb strings.Builder
	sb.WriteString(`<html><body><div class="workshopBrowseItems">`)
	for _, id := range ids {
		sb.WriteString(`<div class="workshopItem" id="sharedfile_` + id + `">`)
		sb.WriteString(`<a href="https://steamcommunity.com/sharedfiles/filedetails/?id=` + id + `">x</a></div>`)
	}
	sb.WriteString(`</div></body></html>`)
	return sb.String()
}

func TestListWorkshopFiles(t *testing.T) {
	var gotQuery url.Values
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(workshopPage("101", "202", "303")))
	}))
	defer ts.Close()

	ids, err := newClient(ts.URL).ListWorkshopFiles(context.Background(), 4000, FilterFavorites, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ids) != 3 || ids[0] != "101" || ids[2] != "303" {
		t.Errorf("ids = %v", ids)
	}
	if gotQuery.Get("browsefilter") != FilterFavorites {
		t.Errorf("browsefilter = %q", gotQuery.Get("browsefilter"))
	}
	if gotQuery.Get("appid") != "4000" {
		t.Errorf("appid = %q", gotQuery.Get("appid"))
	}
}

func TestListWorkshopFilesPaginates(t *testing.T) {
	// A full page must be followed; a short page ends the walk.
	page1 := make([]string, 30)
	for i := range page1 {
		page1[i] = itoa(1000 + i)
	}
	page2 := []string{"2001", "2002"}

	var pages int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages++
		if r.URL.Query().Get("p") == "1" {
			w.Write([]byte(workshopPage(page1...)))
			return
		}
		w.Write([]byte(workshopPage(page2...)))
	}))
	defer ts.Close()

	ids, err := newClient(ts.URL).ListWorkshopFiles(context.Background(), 4000, FilterSubscriptions, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 32 {
		t.Errorf("got %d ids, want 32 across two pages", len(ids))
	}
	if pages != 2 {
		t.Errorf("fetched %d pages, want 2", pages)
	}
}

func TestListWorkshopFilesStopsOnRepeats(t *testing.T) {
	// A server that always returns the same full page must not loop forever.
	page := make([]string, 30)
	for i := range page {
		page[i] = itoa(500 + i)
	}
	var pages int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages++
		w.Write([]byte(workshopPage(page...)))
	}))
	defer ts.Close()

	ids, err := newClient(ts.URL).ListWorkshopFiles(context.Background(), 4000, FilterSubscriptions, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 30 {
		t.Errorf("got %d ids, want 30 unique", len(ids))
	}
	if pages > 2 {
		t.Errorf("fetched %d pages; the walk should stop once nothing new appears", pages)
	}
}

func TestListWorkshopFilesRejectsUnknownFilter(t *testing.T) {
	if _, err := newClient("http://example.invalid").ListWorkshopFiles(
		context.Background(), 4000, "everything", 1); err == nil {
		t.Fatal("expected an error for an unsupported filter")
	}
}

func TestListWorkshopFilesNeedsSession(t *testing.T) {
	c := &Client{HTTP: httpx.New(time.Second, false, true)}
	_, err := c.ListWorkshopFiles(context.Background(), 4000, FilterFavorites, 1)
	if !errors.Is(err, ErrNoSession) {
		t.Fatalf("err = %v, want ErrNoSession", err)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
