package gameserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const sample = `"Filters"
{
	"favorites"
	{
		"1"
		{
			"name"		"First"
			"address"		"10.0.0.1:27015"
			"LastPlayed"		"1529218286"
			"appid"		"440"
			"accountid"		"0"
		}
		"2"
		{
			"name"		"Second"
			"address"		"10.0.0.2:27015"
			"LastPlayed"		"0"
			"appid"		"0"
			"accountid"		"0"
		}
	}
	"history"
	{
		"1"
		{
			"name"		"Played"
			"address"		"10.0.0.3:27015"
			"LastPlayed"		"1600000000"
			"appid"		"730"
			"accountid"		"0"
		}
	}
}
`

func fixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "userdata", "1", "7", "remote", "serverbrowser_hist.vdf")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(sample), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadPreservesOrder(t *testing.T) {
	p := fixture(t)
	favs, err := Load(p, ListFavorites)
	if err != nil {
		t.Fatal(err)
	}
	if len(favs) != 2 || favs[0].Address != "10.0.0.1:27015" || favs[1].Name != "Second" {
		t.Fatalf("favorites = %+v", favs)
	}
	if favs[0].AppID != 440 {
		t.Errorf("AppID = %d", favs[0].AppID)
	}
	if favs[0].LastPlayedTime() == "" {
		t.Error("a non-zero LastPlayed should render")
	}
	if favs[1].LastPlayedTime() != "" {
		t.Error("a zero LastPlayed should render empty")
	}

	hist, err := Load(p, ListHistory)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 1 || hist[0].Address != "10.0.0.3:27015" {
		t.Fatalf("history = %+v", hist)
	}
}

func TestLoadMissingListIsEmptyNotAnError(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.vdf")
	os.WriteFile(p, []byte(`"Filters" { "favorites" { } }`), 0o644)
	got, err := Load(p, ListHistory)
	if err != nil {
		t.Fatalf("an absent list should not be an error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %+v, want nothing", got)
	}
}

// Rewriting one list must not disturb the other, and must keep a backup.
func TestSaveIsLosslessForOtherLists(t *testing.T) {
	p := fixture(t)
	favs, err := Load(p, ListFavorites)
	if err != nil {
		t.Fatal(err)
	}
	favs = append(favs, Entry{Name: "Added", Address: "10.0.0.9:27015", AppID: 10})
	if err := Save(p, ListFavorites, favs); err != nil {
		t.Fatal(err)
	}

	reloaded, err := Load(p, ListFavorites)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded) != 3 || reloaded[2].Address != "10.0.0.9:27015" || reloaded[2].AppID != 10 {
		t.Fatalf("favorites after save = %+v", reloaded)
	}
	// The history list is untouched.
	hist, err := Load(p, ListHistory)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 1 || hist[0].Name != "Played" {
		t.Errorf("history was disturbed: %+v", hist)
	}
	// A backup of the pre-modification file exists.
	b, err := os.ReadFile(p + ".steamcli-backup")
	if err != nil {
		t.Fatalf("no backup was kept: %v", err)
	}
	if string(b) != sample {
		t.Error("the backup should be the original file")
	}
}

// A second write must not overwrite the first backup, which is the only copy
// of the file as it was before this tool touched it.
func TestSaveKeepsTheFirstBackup(t *testing.T) {
	p := fixture(t)
	for i := 0; i < 2; i++ {
		favs, _ := Load(p, ListFavorites)
		Save(p, ListFavorites, favs[:1])
	}
	b, err := os.ReadFile(p + ".steamcli-backup")
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != sample {
		t.Error("the backup was replaced by a later revision")
	}
}

func TestSaveRoundTripsUnquotedSpecials(t *testing.T) {
	p := fixture(t)
	favs, _ := Load(p, ListFavorites)
	favs[0].Name = `Quote " and \ backslash`
	if err := Save(p, ListFavorites, favs); err != nil {
		t.Fatal(err)
	}
	got, err := Load(p, ListFavorites)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Name != `Quote " and \ backslash` {
		t.Errorf("name did not survive escaping: %q", got[0].Name)
	}
}

func TestFilterString(t *testing.T) {
	f := Filter{AppID: 730, Map: "de_dust2", NotEmpty: true, Secure: true, GameType: []string{"a", "b"}}
	got := f.String()
	for _, want := range []string{`\appid\730`, `\map\de_dust2`, `\empty\1`, `\secure\1`, `\gametype\a,b`} {
		if !strings.Contains(got, want) {
			t.Errorf("filter %q missing %q", got, want)
		}
	}
	var empty Filter
	if empty.String() != "" {
		t.Error("an empty filter should produce an empty string")
	}
}

func TestFindHistoryFilesNewestFirst(t *testing.T) {
	root := t.TempDir()
	var paths []string
	for _, id := range []string{"1", "2"} {
		p := filepath.Join(root, "userdata", id, "7", "remote", "serverbrowser_hist.vdf")
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(sample), 0o644)
		paths = append(paths, p)
	}
	// Make the second account clearly newer.
	os.Chtimes(paths[0], time.Now().Add(-time.Hour), time.Now().Add(-time.Hour))

	got := FindHistoryFiles([]string{root})
	if len(got) != 2 {
		t.Fatalf("got %d files, want 2", len(got))
	}
	if got[0] != paths[1] {
		t.Errorf("newest file should come first, got %v", got)
	}
}

// An unreachable address must return an error rather than panicking, since
// replies come from arbitrary hosts.
func TestQueryUnreachableIsAnError(t *testing.T) {
	if _, err := Query("127.0.0.1:1", 200*time.Millisecond); err == nil {
		t.Fatal("expected an error from a closed port")
	}
	if _, err := QueryPlayers("127.0.0.1:1", 200*time.Millisecond); err == nil {
		t.Fatal("expected an error from a closed port")
	}
}

func TestQueryManyReportsPerAddress(t *testing.T) {
	res := QueryMany([]string{"127.0.0.1:1", "127.0.0.1:2"}, 200*time.Millisecond, 2)
	if len(res) != 2 {
		t.Fatalf("got %d results, want 2", len(res))
	}
	for i, r := range res {
		if r.Error == "" {
			t.Errorf("result %d should carry an error", i)
		}
		if r.Info != nil {
			t.Errorf("result %d should have no info", i)
		}
	}
	// Order follows the input, so a caller can pair results with its own list.
	if res[0].Address != "127.0.0.1:1" || res[1].Address != "127.0.0.1:2" {
		t.Errorf("results are out of order: %+v", res)
	}
}
