package steamcmd

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"steamcli.local/steam/internal/httpx"
	"strings"
	"testing"
	"time"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func fixtureArchive(t *testing.T) []byte {
	if runtime.GOOS == "windows" {
		var b bytes.Buffer
		z := zip.NewWriter(&b)
		w, _ := z.Create("steamcmd.exe")
		w.Write([]byte("fixture"))
		z.Close()
		return b.Bytes()
	}
	return tarArchive(t, "steamcmd.sh", tar.TypeReg, "#!/bin/sh\nexit 0\n")
}
func TestBootstrapInstallAndIdempotency(t *testing.T) {
	dir := t.TempDir()
	data := fixtureArchive(t)
	calls := 0
	h := httpx.New(time.Second, false, false)
	h.HTTP.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if !strings.HasPrefix(r.URL.String(), CDN) {
			t.Error("not Valve CDN")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(data))}, nil
	})
	m := Manager{HTTP: h, DataDir: dir, SHA256: fmt.Sprintf("%x", sha256.Sum256(data))}
	p, e := m.Ensure(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(p); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(filepath.Join(dir, "steamcmd", "bootstrap.json"))
	if e != nil || !bytes.Contains(b, []byte(m.SHA256)) {
		t.Fatal("missing provenance", e)
	}
	if _, e = m.Ensure(context.Background()); e != nil || calls != 1 {
		t.Fatal("redownloaded", e, calls)
	}
}
func TestChecksumFailureLeavesNoInstallation(t *testing.T) {
	h := httpx.New(time.Second, false, false)
	h.HTTP.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(fixtureArchive(t)))}, nil
	})
	m := Manager{HTTP: h, DataDir: t.TempDir(), SHA256: strings.Repeat("0", 64)}
	if _, e := m.Ensure(context.Background()); e == nil {
		t.Fatal("bad hash accepted")
	}
	if _, e := os.Stat(filepath.Join(m.DataDir, "steamcmd")); !os.IsNotExist(e) {
		t.Fatal("partial installation survived")
	}
}

// Optional fixture audit of the actual Valve archives, without executing them.
func TestOfficialBootstrapArchives(t *testing.T) {
	dir := os.Getenv("STEAM_CLI_TEST_ARCHIVES")
	if dir == "" {
		t.Skip("set STEAM_CLI_TEST_ARCHIVES to downloaded Valve bootstrap archives")
	}
	for _, name := range []string{"steamcmd.zip", "steamcmd_osx.tar.gz"} {
		t.Run(name, func(t *testing.T) {
			if name == "steamcmd_osx.tar.gz" && runtime.GOOS == "windows" {
				t.Skip("macOS symlinks require Unix extraction")
			}
			data, e := os.ReadFile(filepath.Join(dir, name))
			if e != nil {
				t.Fatal(e)
			}
			dest := t.TempDir()
			if e = Extract(data, name, dest); e != nil {
				t.Fatal(e)
			}
			bin := "steamcmd.exe"
			if strings.HasSuffix(name, ".tar.gz") {
				bin = "steamcmd.sh"
				if _, e = os.Stat(filepath.Join(dest, "Frameworks", "Breakpad.framework", "Resources")); e != nil {
					t.Fatal("framework symlink target:", e)
				}
			}
			if _, e = os.Stat(filepath.Join(dest, bin)); e != nil {
				t.Fatal(e)
			}
		})
	}
}
