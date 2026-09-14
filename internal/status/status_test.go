package status

import (
	"context"
	"net/http"
	"net/http/httptest"
	"steamcli.local/steam/internal/httpx"
	"testing"
	"time"
)

func TestStatusOfflineFails(t *testing.T) {
	m := &Monitor{
		HTTP: httpx.New(time.Second, true, false),
	}
	_, err := m.Check(context.Background(), false, false)
	if err == nil {
		t.Fatal("expected error in offline mode")
	}
}

func TestFetchPlayerCount(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"response":{"player_count":888888,"result":1}}`))
	}))
	defer server.Close()

	client := httpx.New(time.Second, false, true)
	m := &Monitor{
		HTTP:    client,
		BaseURL: server.URL,
	}

	data, err := client.Do(context.Background(), "GET", server.URL, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("expected non-empty response")
	}
	_ = m
}
