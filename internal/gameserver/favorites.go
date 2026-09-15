package gameserver

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"steamcli.local/steam/internal/steamvdf"
)

// Entry is one favorite or history record as the client stores it.
type Entry struct {
	Name       string `json:"name"`
	Address    string `json:"address"`
	LastPlayed int64  `json:"last_played,omitempty"`
	AppID      int    `json:"appid"`
	AccountID  int    `json:"accountid"`
}

// LastPlayedTime renders LastPlayed, which is a Unix timestamp.
func (e Entry) LastPlayedTime() string {
	if e.LastPlayed <= 0 {
		return ""
	}
	return time.Unix(e.LastPlayed, 0).UTC().Format("2006-01-02 15:04 UTC")
}

// The two lists the client keeps inside serverbrowser_hist.vdf.
const (
	ListFavorites = "favorites"
	ListHistory   = "history"
)

// HistoryPath locates the server browser's storage for a Steam account.
//
// Steam keeps it in its own Cloud-synced app directory (AppID 7). The file is
// therefore shared with the running client and with Steam Cloud, which is why
// writes here are made atomically and only while the client is not running.
func HistoryPath(root, accountID string) string {
	return filepath.Join(root, "userdata", accountID, "7", "remote", "serverbrowser_hist.vdf")
}

// FindHistoryFiles returns every server-browser file under the given roots,
// newest first, so a caller with no explicit account can use the most recent.
func FindHistoryFiles(roots []string) []string {
	var found []string
	for _, root := range roots {
		matches, err := filepath.Glob(filepath.Join(root, "userdata", "*", "7", "remote", "serverbrowser_hist.vdf"))
		if err != nil {
			continue
		}
		found = append(found, matches...)
	}
	sort.Slice(found, func(i, j int) bool {
		a, _ := os.Stat(found[i])
		b, _ := os.Stat(found[j])
		if a == nil || b == nil {
			return found[i] < found[j]
		}
		return a.ModTime().After(b.ModTime())
	})
	return found
}

// Load reads one list. Entries keep the file's order, which is the order the
// client displays them in.
func Load(path, list string) ([]Entry, error) {
	m, err := steamvdf.Parse(path)
	if err != nil {
		return nil, err
	}
	filters, ok := m["Filters"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s has no Filters section", path)
	}
	section, ok := filters[list].(map[string]any)
	if !ok {
		// An absent list is empty, not an error: Steam omits it until used.
		return nil, nil
	}

	keys := make([]string, 0, len(section))
	for k := range section {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, ea := strconv.Atoi(keys[i])
		b, eb := strconv.Atoi(keys[j])
		if ea == nil && eb == nil {
			return a < b
		}
		return keys[i] < keys[j]
	})

	out := make([]Entry, 0, len(keys))
	for _, k := range keys {
		e, ok := section[k].(map[string]any)
		if !ok {
			continue
		}
		addr := steamvdf.Str(e["address"])
		if addr == "" {
			continue
		}
		out = append(out, Entry{
			Name:       steamvdf.Str(e["name"]),
			Address:    addr,
			LastPlayed: steamvdf.Atoi64(e["LastPlayed"]),
			AppID:      steamvdf.Atoi(e["appid"]),
			AccountID:  steamvdf.Atoi(e["accountid"]),
		})
	}
	return out, nil
}

// Save rewrites one list, preserving every other section of the file.
//
// The file is round-tripped through the parser, so anything this package does
// not model survives. The write is atomic, and a one-time backup is kept beside
// the original.
func Save(path, list string, entries []Entry) error {
	m, err := steamvdf.Parse(path)
	if err != nil {
		return err
	}
	filters, ok := m["Filters"].(map[string]any)
	if !ok {
		filters = map[string]any{}
		m["Filters"] = filters
	}

	rebuilt := map[string]any{}
	for i, e := range entries {
		name := e.Name
		if name == "" {
			name = e.Address
		}
		rebuilt[strconv.Itoa(i+1)] = map[string]any{
			"name":       name,
			"address":    e.Address,
			"LastPlayed": strconv.FormatInt(e.LastPlayed, 10),
			"appid":      strconv.Itoa(e.AppID),
			"accountid":  strconv.Itoa(e.AccountID),
		}
	}
	filters[list] = rebuilt

	return steamvdf.Write(path, m)
}

