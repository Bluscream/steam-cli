package workshop

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"steamcli.local/steam/internal/community"
	"steamcli.local/steam/internal/httpx"
	"steamcli.local/steam/internal/webapi"
)

func newWeb(base string) *webapi.Client {
	return &webapi.Client{HTTP: httpx.New(5*time.Second, false, true), BaseURL: base, Key: "test-key"}
}

func newCommunity(base string) *community.Client {
	return &community.Client{
		HTTP:        httpx.New(5*time.Second, false, true),
		BaseURL:     base,
		LoginSecure: "76561197960287930%7C%7Ctoken",
	}
}

func TestGetCollectionDetails(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/ISteamRemoteStorage/GetCollectionDetails/v1/":
			w.Write([]byte(`{"response":{"result":1,"resultcount":1,"collectiondetails":[
				{"publishedfileid":"12345","result":1,"children":[
					{"publishedfileid":"111","sortorder":1,"filetype":0},
					{"publishedfileid":"222","sortorder":2,"filetype":0}]}]}}`))
		case "/IPublishedFileService/GetDetails/v1/":
			w.Write([]byte(`{"response":{"publishedfiledetails":[
				{"publishedfileid":"12345","result":1,"title":"My Test Collection","num_children":2}]}}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer ts.Close()

	c := &Client{Web: newWeb(ts.URL)}
	coll, err := c.GetCollectionDetails(context.Background(), "12345")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if coll.PublishedFileID != "12345" || len(coll.Children) != 2 {
		t.Fatalf("collection = %+v", coll)
	}
	if coll.Children[0].PublishedFileID != "111" || coll.Children[1].PublishedFileID != "222" {
		t.Errorf("children = %+v", coll.Children)
	}
	if coll.Details == nil || coll.Details.Title != "My Test Collection" {
		t.Errorf("details = %+v", coll.Details)
	}
}

func TestGetCollectionDetailsMissing(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"response":{"result":1,"resultcount":0,"collectiondetails":[]}}`))
	}))
	defer ts.Close()

	c := &Client{Web: newWeb(ts.URL)}
	if _, err := c.GetCollectionDetails(context.Background(), "404"); err == nil {
		t.Fatal("expected an error for an unknown collection")
	}
}

func TestGetCollectionDetailsRejectsEmptyID(t *testing.T) {
	c := &Client{Web: newWeb("http://example.invalid")}
	if _, err := c.GetCollectionDetails(context.Background(), ""); err == nil {
		t.Fatal("expected an error for an empty collection ID")
	}
}

func TestGetDetailsChunks(t *testing.T) {
	var requests int
	var maxPerRequest int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		q := r.URL.Query()
		n := 0
		for k := range q {
			if strings.HasPrefix(k, "publishedfileids[") {
				n++
			}
		}
		if n > maxPerRequest {
			maxPerRequest = n
		}
		var out []map[string]any
		for i := 0; i < n; i++ {
			out = append(out, map[string]any{"publishedfileid": q.Get("publishedfileids[" + itoa(i) + "]"), "result": 1})
		}
		json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"publishedfiledetails": out}})
	}))
	defer ts.Close()

	ids := make([]string, 120)
	for i := range ids {
		ids[i] = itoa(1000 + i)
	}

	c := &Client{Web: newWeb(ts.URL)}
	got, err := c.GetDetails(context.Background(), ids)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 120 {
		t.Errorf("got %d details, want 120", len(got))
	}
	if requests != 3 {
		t.Errorf("made %d requests, want 3 chunks of 50", requests)
	}
	if maxPerRequest > 50 {
		t.Errorf("a request carried %d ids, want at most 50", maxPerRequest)
	}
}

func TestSubscribeReportsEResultFailure(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		// Steam answers HTTP 200 and reports the real outcome in a header.
		if r.PostForm.Get("publishedfileid") == "222" {
			w.Header().Set("x-eresult", "15")
		} else {
			w.Header().Set("x-eresult", "1")
		}
		w.Write([]byte(`{"response":{}}`))
	}))
	defer ts.Close()

	c := &Client{Web: newWeb(ts.URL)}
	results := c.Subscribe(context.Background(), 4000, []string{"111", "222"})
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if !results[0].Success {
		t.Errorf("111 should have succeeded: %+v", results[0])
	}
	if results[1].Success {
		t.Error("222 returned EResult 15 and must not be reported as success")
	}
	if !strings.Contains(results[1].Error, "15") {
		t.Errorf("error should name the EResult: %q", results[1].Error)
	}
}

func TestSubscribeSkipsBlankIDs(t *testing.T) {
	var seen []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		seen = append(seen, r.PostForm.Get("publishedfileid"))
		w.Header().Set("x-eresult", "1")
		w.Write([]byte(`{"response":{}}`))
	}))
	defer ts.Close()

	c := &Client{Web: newWeb(ts.URL)}
	c.Subscribe(context.Background(), 4000, []string{"111", "  ", "", "222"})
	if len(seen) != 2 {
		t.Errorf("sent %v, want only the two real IDs", seen)
	}
}

func TestUnsubscribeUsesItsOwnMethod(t *testing.T) {
	var path string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Header().Set("x-eresult", "1")
		w.Write([]byte(`{"response":{}}`))
	}))
	defer ts.Close()

	c := &Client{Web: newWeb(ts.URL)}
	c.Unsubscribe(context.Background(), 4000, []string{"111"})
	if path != "/IPublishedFileService/Unsubscribe/v1/" {
		t.Errorf("path = %q", path)
	}
}

func TestCreateCollectionAddsChildren(t *testing.T) {
	var added []string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-eresult", "1")
		w.Write([]byte(`{"response":{"publishedfileid":"999"}}`))
	}))
	defer api.Close()
	comm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		added = append(added, r.PostForm.Get("childid"))
		w.Write([]byte(`{"success":1}`))
	}))
	defer comm.Close()

	c := &Client{Web: newWeb(api.URL), Community: newCommunity(comm.URL)}
	id, results, err := c.CreateCollection(context.Background(), 4000, "T", "d", 0, []string{"1", "2", "3"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "999" {
		t.Errorf("id = %q", id)
	}
	if len(added) != 3 {
		t.Errorf("added %v, want all three children", added)
	}
	for _, r := range results {
		if !r.Success {
			t.Errorf("child %s failed: %s", r.PublishedFileID, r.Error)
		}
	}
}

// The previous implementation reported success while adding nothing. A failing
// membership call must now surface per item.
func TestCreateCollectionReportsChildFailure(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-eresult", "1")
		w.Write([]byte(`{"response":{"publishedfileid":"999"}}`))
	}))
	defer api.Close()
	comm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"success":8,"message":"no such item"}`))
	}))
	defer comm.Close()

	c := &Client{Web: newWeb(api.URL), Community: newCommunity(comm.URL)}
	_, results, err := c.CreateCollection(context.Background(), 4000, "T", "", 0, []string{"1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 || results[0].Success {
		t.Fatalf("results = %+v, want the child reported as failed", results)
	}
	if !strings.Contains(results[0].Error, "no such item") {
		t.Errorf("error should carry Steam's message: %q", results[0].Error)
	}
}

func TestCreateCollectionRefusesItemsWithoutSession(t *testing.T) {
	c := &Client{Web: newWeb("http://example.invalid"), Community: &community.Client{}}
	_, _, err := c.CreateCollection(context.Background(), 4000, "T", "", 0, []string{"1"})
	if !errors.Is(err, community.ErrNoSession) {
		t.Fatalf("err = %v, want ErrNoSession", err)
	}
}

func TestCreateCollectionRejectsMissingID(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-eresult", "1")
		w.Write([]byte(`{"response":{}}`))
	}))
	defer ts.Close()

	c := &Client{Web: newWeb(ts.URL)}
	if _, _, err := c.CreateCollection(context.Background(), 4000, "T", "", 0, nil); err == nil {
		t.Fatal("expected an error when Steam returns no publishedfileid")
	}
}

func TestCreateCollectionRequiresTitle(t *testing.T) {
	c := &Client{Web: newWeb("http://example.invalid")}
	if _, _, err := c.CreateCollection(context.Background(), 4000, "", "", 0, nil); err == nil {
		t.Fatal("expected an error for an empty title")
	}
}

func TestEditCollectionRequiresAChange(t *testing.T) {
	c := &Client{Web: newWeb("http://example.invalid")}
	if err := c.EditCollection(context.Background(), 4000, "1", "", "", -1); err == nil {
		t.Fatal("expected an error when nothing would change")
	}
}

func TestEditCollectionPropagatesEResult(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-eresult", "15")
		w.Write([]byte(`{"response":{}}`))
	}))
	defer ts.Close()

	c := &Client{Web: newWeb(ts.URL)}
	err := c.EditCollection(context.Background(), 4000, "1", "new title", "", -1)
	if err == nil {
		t.Fatal("expected access-denied to surface")
	}
	var re *webapi.ResultError
	if !errors.As(err, &re) || re.Result != webapi.EResultAccessDenied {
		t.Errorf("err = %v, want a ResultError carrying EResult 15", err)
	}
}

func TestDeleteCollectionPrefersSession(t *testing.T) {
	var communityHit bool
	comm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		communityHit = true
		w.Write([]byte(`{"success":1}`))
	}))
	defer comm.Close()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the Web API must not be used when a session is available")
	}))
	defer api.Close()

	c := &Client{Web: newWeb(api.URL), Community: newCommunity(comm.URL)}
	if err := c.DeleteCollection(context.Background(), 4000, "1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !communityHit {
		t.Error("the Community session should have been used")
	}
}

func TestDeleteCollectionExplainsPublisherOnly(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-eresult", "15")
		w.Write([]byte(`{"response":{}}`))
	}))
	defer api.Close()

	c := &Client{Web: newWeb(api.URL), Community: &community.Client{}}
	err := c.DeleteCollection(context.Background(), 4000, "1")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "publisher-only") {
		t.Errorf("error should explain why: %q", err)
	}
}

func TestAddAndRemoveItems(t *testing.T) {
	var paths []string
	comm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Write([]byte(`{"success":1}`))
	}))
	defer comm.Close()

	c := &Client{Web: newWeb("http://example.invalid"), Community: newCommunity(comm.URL)}
	if res := c.AddItems(context.Background(), "500", []string{"1", "2"}); len(res) != 2 {
		t.Fatalf("add results = %+v", res)
	}
	if res := c.RemoveItems(context.Background(), "500", []string{"3"}); len(res) != 1 || !res[0].Success {
		t.Fatalf("remove results = %+v", res)
	}
	want := []string{"/sharedfiles/addchild/", "/sharedfiles/addchild/", "/sharedfiles/removechild/"}
	for i, p := range want {
		if paths[i] != p {
			t.Errorf("path %d = %q, want %q", i, paths[i], p)
		}
	}
}

func TestAddItemsWithoutSession(t *testing.T) {
	c := &Client{Web: newWeb("http://example.invalid")}
	res := c.AddItems(context.Background(), "1", []string{"2"})
	if len(res) != 1 || res[0].Success {
		t.Fatalf("results = %+v, want a failure", res)
	}
}

func TestHasSession(t *testing.T) {
	if (&Client{}).HasSession() {
		t.Error("no community client means no session")
	}
	if (&Client{Community: &community.Client{}}).HasSession() {
		t.Error("an empty cookie means no session")
	}
	if !(&Client{Community: newCommunity("http://x.invalid")}).HasSession() {
		t.Error("a cookie means a session")
	}
}

func TestQueryUsesCursorPagination(t *testing.T) {
	var cursors []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cursor := r.URL.Query().Get("cursor")
		cursors = append(cursors, cursor)
		switch cursor {
		case "*":
			w.Write([]byte(`{"response":{"total":3,"next_cursor":"c2","publishedfiledetails":[
				{"publishedfileid":"1","title":"one"},{"publishedfileid":"2","title":"two"}]}}`))
		default:
			w.Write([]byte(`{"response":{"total":3,"next_cursor":"c3","publishedfiledetails":[
				{"publishedfileid":"3","title":"three"}]}}`))
		}
	}))
	defer ts.Close()

	c := &Client{Web: newWeb(ts.URL)}
	items, total, err := c.Query(context.Background(), QueryOptions{AppID: 4000, FileType: -1, AllPages: true, NumPerPage: 2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 3 || len(items) != 3 {
		t.Errorf("got %d items (total %d), want 3", len(items), total)
	}
	if len(cursors) < 2 || cursors[0] != "*" || cursors[1] != "c2" {
		t.Errorf("cursors = %v, want the walk to start at * and follow next_cursor", cursors)
	}
}

func TestQueryDeduplicates(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A server that repeats itself must not produce duplicates or loop.
		w.Write([]byte(`{"response":{"total":1,"next_cursor":"same","publishedfiledetails":[
			{"publishedfileid":"1","title":"one"}]}}`))
	}))
	defer ts.Close()

	c := &Client{Web: newWeb(ts.URL)}
	items, _, err := c.Query(context.Background(), QueryOptions{AppID: 4000, FileType: -1, AllPages: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Errorf("got %d items, want 1 unique", len(items))
	}
}

func TestQueryCollectionsFiltersToType2(t *testing.T) {
	var fileType string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fileType = r.URL.Query().Get("filetype")
		w.Write([]byte(`{"response":{"total":0,"publishedfiledetails":[]}}`))
	}))
	defer ts.Close()

	c := &Client{Web: newWeb(ts.URL)}
	if _, _, err := c.QueryCollections(context.Background(), 4000, 1, 10, "x"); err != nil {
		t.Fatal(err)
	}
	if fileType != "2" {
		t.Errorf("filetype = %q, want 2 (collections)", fileType)
	}
}

const acfFixture = `"AppWorkshop"
{
	"appid"		"107410"
	"SizeOnDisk"		"152207211569"
	"TimeLastUpdated"		"1789384987"
	"LastBuildID"		"25102380"
	"WorkshopItemsInstalled"
	{
		"169806722"
		{
			"size"		"6402796"
			"timeupdated"		"1457883528"
			"manifest"		"-1"
			"ugchandle"		"315622114442411106"
		}
		"176185661"
		{
			"size"		"6838895"
			"timeupdated"		"1651799144"
			"ugchandle"		"1836916390031720397"
		}
	}
	"WorkshopItemDetails"
	{
		"169806722"
		{
			"manifest"		"7811700143279984"
			"timeupdated"		"1457883528"
		}
		"999888777"
		{
			"manifest"		"123"
		}
	}
}
`

func writeACF(t *testing.T, root, appID, content string) {
	t.Helper()
	dir := filepath.Join(root, "steamapps", "workshop")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "appworkshop_"+appID+".acf"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScanInstalledParsesVDF(t *testing.T) {
	root := t.TempDir()
	writeACF(t, root, "107410", acfFixture)

	apps, err := ScanInstalled([]string{root}, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(apps) != 1 {
		t.Fatalf("got %d apps, want 1", len(apps))
	}
	got := apps[0]
	if got.AppID != "107410" {
		t.Errorf("appid = %q", got.AppID)
	}
	// Item IDs are the section keys. Sizes, timestamps, manifests, ugchandles
	// and the app's own ID must not be mistaken for items.
	want := []string{"169806722", "176185661", "999888777"}
	if got.Total != len(want) {
		t.Fatalf("items = %v, want %v", got.Items, want)
	}
	for i, id := range want {
		if got.Items[i] != id {
			t.Errorf("item %d = %q, want %q", i, got.Items[i], id)
		}
	}
}

func TestScanInstalledFiltersByApp(t *testing.T) {
	root := t.TempDir()
	writeACF(t, root, "107410", acfFixture)
	writeACF(t, root, "4000", strings.Replace(acfFixture, "107410", "4000", 1))

	apps, err := ScanInstalled([]string{root}, 4000)
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 1 || apps[0].AppID != "4000" {
		t.Errorf("apps = %+v, want only AppID 4000", apps)
	}
}

func TestScanInstalledSkipsEmptyAndMalformed(t *testing.T) {
	root := t.TempDir()
	writeACF(t, root, "10180", `"AppWorkshop"
{
	"appid"		"10180"
	"WorkshopItemsInstalled"
	{
	}
	"WorkshopItemDetails"
	{
	}
}
`)
	writeACF(t, root, "1", "this is not VDF at all {{{")

	apps, err := ScanInstalled([]string{root}, 0)
	if err != nil {
		t.Fatalf("a malformed manifest must not fail the scan: %v", err)
	}
	if len(apps) != 0 {
		t.Errorf("apps = %+v, want none (no installed items)", apps)
	}
}

func TestListUserItemsNeedsSession(t *testing.T) {
	c := &Client{Web: newWeb("http://example.invalid")}
	if _, err := c.ListUserItems(context.Background(), 4000, community.FilterFavorites); !errors.Is(err, community.ErrNoSession) {
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

// Empty results must serialize as a list, not null, so consumers can iterate.
func TestEmptyResultsSerializeAsLists(t *testing.T) {
	apps, err := ScanInstalled([]string{t.TempDir()}, 0)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(apps)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "[]" {
		t.Errorf("ScanInstalled with no results = %s, want []", b)
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"response":{"total":0,"publishedfiledetails":[]}}`))
	}))
	defer ts.Close()
	items, _, err := (&Client{Web: newWeb(ts.URL)}).Query(context.Background(), QueryOptions{AppID: 4000, FileType: -1})
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := json.Marshal(items); string(b) != "[]" {
		t.Errorf("empty Query = %s, want []", b)
	}
}
