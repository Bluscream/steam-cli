package steamcmd

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"steamcli.local/steam/internal/httpx"
)

func tarArchive(t *testing.T, name string, kind byte, body string) []byte {
	t.Helper()
	var b bytes.Buffer
	g := gzip.NewWriter(&b)
	w := tar.NewWriter(g)
	h := &tar.Header{Name: name, Typeflag: kind, Mode: 0755, Size: int64(len(body))}
	if kind != tar.TypeReg {
		h.Size = 0
	}
	if e := w.WriteHeader(h); e != nil {
		t.Fatal(e)
	}
	if h.Size > 0 {
		w.Write([]byte(body))
	}
	w.Close()
	g.Close()
	return b.Bytes()
}
func TestArchiveExtraction(t *testing.T) {
	dir := t.TempDir()
	if e := Extract(tarArchive(t, "linux32/steamcmd", tar.TypeReg, "executable"), "x.tar.gz", dir); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(filepath.Join(dir, "linux32", "steamcmd"))
	if e != nil || string(b) != "executable" {
		t.Fatal(e)
	}
	for _, name := range []string{"../escape", "/absolute", "x/../../escape", `..\escape`, `C:\escape`} {
		if e := Extract(tarArchive(t, name, tar.TypeReg, "bad"), "x.tar.gz", t.TempDir()); e == nil {
			t.Errorf("accepted %s", name)
		}
	}
	for _, kind := range []byte{tar.TypeSymlink, tar.TypeLink, tar.TypeChar} {
		if e := Extract(tarArchive(t, "link", kind, ""), "x.tar.gz", t.TempDir()); e == nil {
			t.Errorf("accepted link/device %d", kind)
		}
	}
}
func TestZipTraversalAndDuplicates(t *testing.T) {
	for _, names := range [][]string{{"../escape"}, {"steamcmd.exe", "steamcmd.exe"}} {
		var b bytes.Buffer
		z := zip.NewWriter(&b)
		for _, n := range names {
			w, _ := z.Create(n)
			w.Write([]byte("bad"))
		}
		z.Close()
		if e := Extract(b.Bytes(), "steamcmd.zip", t.TempDir()); e == nil {
			t.Fatal("unsafe zip accepted")
		}
	}
}
func TestDownloadOrderingAndValidation(t *testing.T) {
	d := Download{AppID: "730", Dir: "/some folder/game", User: "anonymous", Platform: "windows", Branch: "test", Validate: true}
	a, s, e := d.Args()
	if e != nil {
		t.Fatal(e)
	}
	joined := strings.Join(a, "|")
	if strings.Index(joined, "+force_install_dir") > strings.Index(joined, "+login") || !strings.Contains(joined, "+app_update|730|-beta|test|validate|+quit") || s != "Success! App '730' fully installed." {
		t.Fatal(joined, s)
	}
	d.User = "+quit"
	if _, _, e = d.Args(); e == nil {
		t.Fatal("command injection accepted")
	}
	d.User = "alice"
	d.Dir = "/tmp/\nquit"
	if _, _, e = d.Args(); e == nil {
		t.Fatal("newline accepted")
	}
}
func TestMarkerAcrossWrites(t *testing.T) {
	var b bytes.Buffer
	w := outputWatch{dst: &b, success: "Success! App '730' fully installed."}
	w.Write([]byte("noise\nSuccess! App '730' fully"))
	w.Write([]byte(" installed.\n"))
	if !w.found {
		t.Fatal("split success lost")
	}
	w.Write(bytes.Repeat([]byte("x"), 10000))
	if len(w.tail) > 4096 {
		t.Fatal("unbounded memory")
	}
}
func TestFakeProcessAndExitCode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix subprocess fixture")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "steamcmd.sh")
	os.WriteFile(p, []byte("#!/bin/sh\nprintf 'argument:%s\\n' \"$1\"\nexit 0\n"), 0700)
	m := Manager{HTTP: httpx.New(time.Second, false, false), DataDir: dir, Path: p}
	var b bytes.Buffer
	if e := m.Run(context.Background(), []string{"space ; $(echo injection)"}, nil, &b, io.Discard, ""); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(b.String(), "space ; $(echo injection)") {
		t.Fatal("arguments altered")
	}
	if e := m.Run(context.Background(), nil, nil, io.Discard, io.Discard, "Success!"); e == nil {
		t.Fatal("exit 0 falsely accepted")
	}
	os.WriteFile(p, []byte("#!/bin/sh\nexit 7\n"), 0700)
	if e := m.Run(context.Background(), nil, nil, io.Discard, io.Discard, ""); e == nil || e.(*ExitError).Code != 7 {
		t.Fatal(e)
	}
}
func TestOfflineExecutionAndMissingInstall(t *testing.T) {
	m := Manager{HTTP: httpx.New(time.Second, true, false), DataDir: t.TempDir()}
	if _, e := m.Ensure(context.Background()); e == nil {
		t.Fatal("offline installed")
	}
	if e := m.Run(context.Background(), nil, nil, io.Discard, io.Discard, ""); e == nil {
		t.Fatal("offline execution allowed")
	}
}
func TestPlatformMatrix(t *testing.T) {
	for _, os := range []string{"linux", "windows", "darwin"} {
		if _, e := ArchiveName(os, "amd64"); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := ArchiveName("linux", "arm64"); e == nil {
		t.Fatal("claimed native ARM bootstrap")
	}
	if BinaryRelative("darwin") != filepath.Join("MacOS", "steamcmd.sh") {
		t.Fatal("macOS directory layout")
	}
}

func TestBatchCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix process group behavior")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "launcher.sh")
	os.WriteFile(p, []byte("#!/bin/sh\nsleep 60 &\nwait\n"), 0700)
	m := Manager{HTTP: httpx.New(time.Second, false, false), DataDir: dir, Path: p}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	e := m.Run(ctx, nil, nil, io.Discard, io.Discard, "")
	if e == nil || time.Since(start) > 3*time.Second {
		t.Fatal("cancellation failed", e)
	}
}

func TestVerifyAppRequiresCorrectCompletedManifest(t *testing.T) {
	dir := t.TempDir()
	if e := VerifyApp(dir, "730"); e == nil {
		t.Fatal("missing manifest accepted")
	}
	os.MkdirAll(filepath.Join(dir, "steamapps"), 0700)
	p := filepath.Join(dir, "steamapps", "appmanifest_730.acf")
	for _, body := range []string{`"AppState" { "appid" "999" "StateFlags" "4" }`, `"AppState" { "appid" "730" "StateFlags" "2" }`} {
		os.WriteFile(p, []byte(body), 0600)
		if e := VerifyApp(dir, "730"); e == nil {
			t.Fatal("wrong/incomplete manifest accepted")
		}
	}
	os.WriteFile(p, []byte(`"AppState" { "appid" "730" "StateFlags" "4" }`), 0600)
	if e := VerifyApp(dir, "730"); e != nil {
		t.Fatal(e)
	}
}

func TestFrameworkSymlinksDeferred(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("macOS tar symlinks are only extracted on Unix")
	}
	var b bytes.Buffer
	g := gzip.NewWriter(&b)
	w := tar.NewWriter(g)
	for _, h := range []*tar.Header{{Name: "Framework/Current", Typeflag: tar.TypeSymlink, Linkname: "Versions/A"}, {Name: "Framework/Resources", Typeflag: tar.TypeSymlink, Linkname: "Current/Resources"}, {Name: "Framework/Versions/A/Resources", Typeflag: tar.TypeReg, Mode: 0600, Size: 2}} {
		if e := w.WriteHeader(h); e != nil {
			t.Fatal(e)
		}
		if h.Size > 0 {
			w.Write([]byte("ok"))
		}
	}
	w.Close()
	g.Close()
	dir := t.TempDir()
	if e := Extract(b.Bytes(), "x.tar.gz", dir); e != nil {
		t.Fatal(e)
	}
	got, e := os.ReadFile(filepath.Join(dir, "Framework", "Resources"))
	if e != nil || string(got) != "ok" {
		t.Fatal(e)
	}
}
func TestEscapingSymlinkRejected(t *testing.T) {
	var b bytes.Buffer
	g := gzip.NewWriter(&b)
	w := tar.NewWriter(g)
	w.WriteHeader(&tar.Header{Name: "escape", Typeflag: tar.TypeSymlink, Linkname: "../outside"})
	w.Close()
	g.Close()
	if e := Extract(b.Bytes(), "x.tar.gz", t.TempDir()); e == nil {
		t.Fatal("escaping link accepted")
	}
}
func TestArchiveFileCannotWriteThroughSymlink(t *testing.T) {
	var b bytes.Buffer
	g := gzip.NewWriter(&b)
	w := tar.NewWriter(g)
	w.WriteHeader(&tar.Header{Name: "alias", Typeflag: tar.TypeSymlink, Linkname: "real"})
	w.WriteHeader(&tar.Header{Name: "real", Typeflag: tar.TypeDir, Mode: 0700})
	w.WriteHeader(&tar.Header{Name: "alias/file", Typeflag: tar.TypeReg, Mode: 0600, Size: 3})
	w.Write([]byte("bad"))
	w.Close()
	g.Close()
	dir := t.TempDir()
	if e := Extract(b.Bytes(), "x.tar.gz", dir); e == nil {
		t.Fatal("file/symlink conflict accepted")
	}
	if _, e := os.Stat(filepath.Join(dir, "real", "file")); !os.IsNotExist(e) {
		t.Fatal("write followed symlink")
	}
}
