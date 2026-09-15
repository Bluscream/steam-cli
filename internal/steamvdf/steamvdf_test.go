package steamvdf

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestStrictParseAndRoundTrip(t *testing.T) {
	input := []byte("// comment\n\"root\" {\"path\" \"C:\\\\games\\\\steam\" \"args\" \"say \\\"hi\\\"\\nnext\" \"empty\" {} }\n\"other\" {\"x\" \"y\"}// EOF comment")
	m, err := ParseBytes(input)
	if err != nil {
		t.Fatal(err)
	}
	next, err := ParseBytes(Render(m))
	if err != nil || !reflect.DeepEqual(m, next) {
		t.Fatalf("round trip: %v", err)
	}
	if len(m) != 2 {
		t.Fatal("lost second root")
	}
}
func TestRejectAmbiguousDocuments(t *testing.T) {
	for _, s := range []string{`"r" {"a" "1" "a" "2"}`, `"r" {"A" "1" "a" "2"}`, `"r" {"a" "1"`, `"r" {"a"}`, `"r" {"a" "unterminated}`, `"r" {} }`, `"r" {"a" "bad\q"}`, strings.Repeat(`"r" {`, 130)} {
		if _, err := ParseBytes([]byte(s)); err == nil {
			t.Fatalf("accepted %q", s)
		}
	}
}
func TestWritePreservesBackupAndMode(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.vdf")
	original := []byte(`"r" {"keep" "original"}`)
	if err := os.WriteFile(p, original, 0600); err != nil {
		t.Fatal(err)
	}
	m, _ := Parse(p)
	Set(Obj(m, "r"), "new", "quoted \" value \\ newline\n")
	if err := Write(p, m); err != nil {
		t.Fatal(err)
	}
	if err := Write(p, m); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p + BackupSuffix)
	if string(b) != string(original) {
		t.Fatal("backup overwritten")
	}
	for _, name := range []string{p, p + BackupSuffix} {
		st, _ := os.Stat(name)
		if st.Mode().Perm()&0077 != 0 {
			t.Fatal("exposed private configuration")
		}
	}
	invalid := map[string]any{"r": map[string]any{"a": 1}}
	before, _ := os.ReadFile(p)
	if Write(p, invalid) == nil {
		t.Fatal("accepted lossy leaf")
	}
	after, _ := os.ReadFile(p)
	if string(before) != string(after) {
		t.Fatal("changed after failure")
	}
}
func TestBackupRejectsSymlink(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.vdf")
	os.WriteFile(p, []byte(`"r" {}`), 0600)
	if err := os.Symlink(p, p+BackupSuffix); err != nil {
		t.Skip(err)
	}
	if BackupOnce(p) == nil {
		t.Fatal("accepted symlink backup")
	}
}
func TestConcurrentWriterLock(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.vdf")
	unlock, e := Lock(p)
	if e != nil {
		t.Fatal(e)
	}
	defer unlock()
	if release, e := Lock(p); e == nil {
		release()
		t.Fatal("second writer acquired lock")
	}
}
