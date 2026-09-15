package cli

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// search fans out across goroutines that each resolve the configuration. When
// a profile sets allow_http, resolving it used to write to the shared options
// struct from every one of them while another goroutine read the same field.
// Run with -race; this guards the caching that removed it.
func TestSearchConcurrentSettingsIsRaceFree(t *testing.T) {
	cleanEnv(t)
	t.Setenv("STEAM_API_KEY", "k")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "storesearch") {
			w.Write([]byte(`{"total":1,"items":[{"id":438100,"type":"app","name":"VRChat"}]}`))
			return
		}
		w.Write([]byte(`{"response":{"players":[],"publishedfiledetails":[],"games":[]}}`))
	}))
	defer srv.Close()
	// A profile with allow_http set is what makes settings() write to the
	// shared options struct from each search goroutine.
	dir := t.TempDir()
	cfg := dir + "/config.json"
	body := `{"default_profile":"d","profiles":{"d":{"allow_http":true,"web_url":"` + srv.URL +
		`","community_url":"` + srv.URL + `","store_url":"` + srv.URL + `"}}}`
	os.WriteFile(cfg, []byte(body), 0o600)
	t.Setenv("STEAM_STORE_URL", srv.URL)
	t.Setenv("STEAM_WEB_URL", srv.URL)
	t.Setenv("STEAM_COMMUNITY_URL", srv.URL)
	execute(t, "--config", cfg, "search", "vrchat")
}
