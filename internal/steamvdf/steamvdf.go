// Package steamvdf reads and rewrites the Valve KeyValues files the desktop
// client keeps on disk.
//
// Every writer here round-trips the whole file through the parser, so sections
// this program does not model survive untouched, and every write is atomic with
// a one-time backup beside the original. That matters because these files are
// shared with a running Steam client and, in the case of userdata, with Steam
// Cloud.
package steamvdf

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// MaxSize bounds a KeyValues file. The largest thing Steam keeps in this
// format is localconfig.vdf, which is a few hundred kilobytes.
const MaxSize = 16 << 20

// BackupSuffix marks the copy taken before this program first modified a file.
const BackupSuffix = ".steamcli-backup"

// Parse reads a KeyValues file into a tree of map[string]any and string leaves.
func Parse(path string) (map[string]any, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > MaxSize {
		return nil, fmt.Errorf("%s exceeds %d bytes", path, MaxSize)
	}
	b, err := io.ReadAll(io.LimitReader(f, MaxSize+1))
	if err != nil {
		return nil, err
	}
	return ParseBytes(b)
}

// Str returns a leaf as a string, or "" if it is absent or a subtree.
func Str(v any) string { s, _ := v.(string); return s }

// Atoi returns a leaf as an int, or 0 if it is absent or not a number.
func Atoi(v any) int { n, _ := strconv.Atoi(Str(v)); return n }

// Atoi64 returns a leaf as an int64, or 0 if it is absent or not a number.
func Atoi64(v any) int64 { n, _ := strconv.ParseInt(Str(v), 10, 64); return n }

// Obj walks a path of nested sections, returning nil if any step is missing.
func Obj(m map[string]any, path ...string) map[string]any {
	for _, k := range path {
		if m == nil {
			return nil
		}
		next, _ := m[k].(map[string]any)
		m = next
	}
	return m
}

// Section walks a path of nested sections, creating any that do not exist.
// Use it on a tree you are about to write; use Obj to read.
func Section(m map[string]any, path ...string) map[string]any {
	for _, k := range path {
		next, ok := m[k].(map[string]any)
		if !ok {
			next = map[string]any{}
			m[k] = next
		}
		m = next
	}
	return m
}

// CaseKey finds the existing spelling of a key, ignoring case.
//
// Steam is inconsistent about capitalisation across files and versions
// ("apps" and "Apps", "betakey" and "BetaKey"), and writing a second spelling
// alongside the first would leave the file with two competing values.
func CaseKey(m map[string]any, key string) (string, bool) {
	if _, ok := m[key]; ok {
		return key, true
	}
	for k := range m {
		if strings.EqualFold(k, key) {
			return k, true
		}
	}
	return key, false
}

// Get reads a leaf by case-insensitive key.
func Get(m map[string]any, key string) any {
	if m == nil {
		return nil
	}
	k, ok := CaseKey(m, key)
	if !ok {
		return nil
	}
	return m[k]
}

// Set writes a leaf, reusing the existing spelling of the key if there is one.
func Set(m map[string]any, key, value string) {
	k, _ := CaseKey(m, key)
	m[k] = value
}

// Delete removes a leaf by case-insensitive key.
func Delete(m map[string]any, key string) {
	if k, ok := CaseKey(m, key); ok {
		delete(m, k)
	}
}

// Render serialises a parsed tree back to KeyValues text.
func Render(m map[string]any) []byte {
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
	// Numeric keys are list indices and must stay in numeric order; the rest
	// are sorted so the output is stable between runs.
	sort.Slice(keys, func(i, j int) bool {
		a, ea := strconv.Atoi(keys[i])
		c, ec := strconv.Atoi(keys[j])
		if ea == nil && ec == nil && a != c {
			return a < c
		}
		if (ea == nil) != (ec == nil) {
			return ea == nil
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

var quoter = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`, "\t", `\t`)

func quote(s string) string { return `"` + quoter.Replace(s) + `"` }

// Write renders a tree over an existing file, keeping a one-time backup and
// replacing the file atomically.
func Write(path string, m map[string]any) error {
	if st, err := os.Lstat(path); err == nil && !st.Mode().IsRegular() {
		return fmt.Errorf("refusing to replace non-regular file: %s", path)
	}
	data := Render(m)
	// Re-reading what we are about to write is cheap next to losing a config
	// file to a serialiser bug on an input shape we have not seen.
	if parsed, err := ParseBytes(data); err != nil || !reflect.DeepEqual(parsed, m) {
		return fmt.Errorf("refusing to write %s: the result does not round-trip (%v)", path, err)
	}
	if err := BackupOnce(path); err != nil {
		return err
	}
	return WriteAtomic(path, data)
}

// BackupOnce copies path beside itself the first time, and does nothing after.
// The backup is the file as it was before this program ever touched it, which
// is the copy worth keeping.
func BackupOnce(path string) error {
	bak := path + BackupSuffix
	if st, err := os.Lstat(bak); err == nil {
		if !st.Mode().IsRegular() {
			return fmt.Errorf("backup is not a regular file: %s", bak)
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	f, err := os.OpenFile(bak, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		f.Close()
		if !ok {
			os.Remove(bak)
		}
	}()
	if _, err = f.Write(b); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	ok = true
	return nil
}

// WriteAtomic replaces path with data via a temporary file in the same
// directory, so a crash mid-write cannot leave a truncated config behind.
func WriteAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".steamcli-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	mode := os.FileMode(0o600)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	if err := os.Chmod(tmp, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
