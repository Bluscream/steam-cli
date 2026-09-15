package gameserver

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/andygrunwald/vdf"
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

func parseFile(path string) (map[string]any, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > 16<<20 {
		return nil, errors.New("server browser file exceeds 16 MiB")
	}
	return vdf.NewParser(io.LimitReader(f, 16<<20)).Parse()
}

func str(v any) string { s, _ := v.(string); return s }

func atoi(v any) int { n, _ := strconv.Atoi(str(v)); return n }

func atoi64(v any) int64 { n, _ := strconv.ParseInt(str(v), 10, 64); return n }

// Load reads one list. Entries keep the file's order, which is the order the
// client displays them in.
func Load(path, list string) ([]Entry, error) {
	m, err := parseFile(path)
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
		addr := str(e["address"])
		if addr == "" {
			continue
		}
		out = append(out, Entry{
			Name:       str(e["name"]),
			Address:    addr,
			LastPlayed: atoi64(e["LastPlayed"]),
			AppID:      atoi(e["appid"]),
			AccountID:  atoi(e["accountid"]),
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
	m, err := parseFile(path)
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

	if err := backupOnce(path); err != nil {
		return err
	}
	return writeAtomic(path, render(m))
}

func backupOnce(path string) error {
	bak := path + ".steamcli-backup"
	if _, err := os.Stat(bak); err == nil {
		return nil // keep the first, pre-modification copy
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return os.WriteFile(bak, b, 0o644)
}

func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".serverbrowser-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// render serialises the parsed tree back to Valve KeyValues text.
func render(m map[string]any) []byte {
	var b strings.Builder
	writeObject(&b, m, 0)
	return []byte(b.String())
}

func writeObject(b *strings.Builder, m map[string]any, depth int) {
	indent := strings.Repeat("\t", depth)
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Numeric keys are the list indices and must stay in numeric order; the
	// rest are sorted so the output is stable between runs.
	sort.Slice(keys, func(i, j int) bool {
		a, ea := strconv.Atoi(keys[i])
		c, ec := strconv.Atoi(keys[j])
		if ea == nil && ec == nil {
			return a < c
		}
		return keys[i] < keys[j]
	})
	for _, k := range keys {
		switch v := m[k].(type) {
		case map[string]any:
			fmt.Fprintf(b, "%s%s\n%s{\n", indent, quote(k), indent)
			writeObject(b, v, depth+1)
			fmt.Fprintf(b, "%s}\n", indent)
		default:
			fmt.Fprintf(b, "%s%s\t\t%s\n", indent, quote(k), quote(fmt.Sprint(v)))
		}
	}
}

var quoter = strings.NewReplacer(`\`, `\\`, `"`, `\"`)

func quote(s string) string { return `"` + quoter.Replace(s) + `"` }
