package workshop

import (
	"context"
	"net/http"
	"net/http/httptest"
	"steamcli.local/steam/internal/httpx"
	"steamcli.local/steam/internal/webapi"
	"testing"
	"time"
)

func TestGetCollectionDetails(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/ISteamRemoteStorage/GetCollectionDetails/v1/" {
			w.Write([]byte(`{
				"response": {
					"result": 1,
					"resultcount": 1,
					"collectiondetails": [{
						"publishedfileid": "12345",
						"result": 1,
						"children": [
							{"publishedfileid": "111", "sortorder": 1, "filetype": 0},
							{"publishedfileid": "222", "sortorder": 2, "filetype": 0}
						]
					}]
				}
			}`))
			return
		}
		if r.URL.Path == "/IPublishedFileService/GetDetails/v1/" {
			w.Write([]byte(`{
				"response": {
					"publishedfiledetails": [{
						"publishedfileid": "12345",
						"result": 1,
						"title": "My Test Collection",
						"num_children": 2
					}]
				}
			}`))
			return
		}
		w.WriteHeader(404)
	}))
	defer ts.Close()

	webClient := &webapi.Client{
		HTTP:    httpx.New(time.Second, false, true),
		BaseURL: ts.URL,
		Key:     "test-key",
	}

	client := &Client{Web: webClient}
	coll, err := client.GetCollectionDetails(context.Background(), "12345")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if coll.PublishedFileID != "12345" {
		t.Errorf("expected 12345, got %s", coll.PublishedFileID)
	}
	if len(coll.Children) != 2 {
		t.Fatalf("expected 2 children, got %d", len(coll.Children))
	}
	if coll.Children[0].PublishedFileID != "111" || coll.Children[1].PublishedFileID != "222" {
		t.Errorf("unexpected children: %+v", coll.Children)
	}
	if coll.Details == nil || coll.Details.Title != "My Test Collection" {
		t.Errorf("unexpected title in details: %+v", coll.Details)
	}
}

func TestBatchSubscribe(t *testing.T) {
	subscribed := make([]string, 0)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/IPublishedFileService/Subscribe/v1/" {
			r.ParseForm()
			id := r.Form.Get("publishedfileid")
			subscribed = append(subscribed, id)
			w.Write([]byte(`{"response":{}}`))
			return
		}
		w.WriteHeader(404)
	}))
	defer ts.Close()

	webClient := &webapi.Client{
		HTTP:    httpx.New(time.Second, false, true),
		BaseURL: ts.URL,
		Key:     "test-key",
	}

	client := &Client{Web: webClient}
	results := client.Subscribe(context.Background(), 4000, []string{"111", "222"})
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	for _, res := range results {
		if !res.Success {
			t.Errorf("expected success for %s", res.PublishedFileID)
		}
	}
	if len(subscribed) != 2 || subscribed[0] != "111" || subscribed[1] != "222" {
		t.Errorf("unexpected subscribed list: %v", subscribed)
	}
}
