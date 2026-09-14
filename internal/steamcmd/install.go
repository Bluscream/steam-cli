package steamcmd

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/gofrs/flock"
	"steamcli.local/steam/internal/httpx"
)

const CDN = "https://steamcdn-a.akamaihd.net/client/installer/"
const maxExpanded int64 = 256 << 20

type Manager struct {
	HTTP                  *httpx.Client
	DataDir, Path, SHA256 string
	NoDownload            bool
	Progress              io.Writer
}

func ArchiveName(goos, arch string) (string, error) {
	if arch != "amd64" && arch != "386" && !(goos == "darwin" && arch == "arm64") {
		return "", fmt.Errorf("Valve SteamCMD has no native %s/%s bootstrap; use --steamcmd-path with a configured compatibility wrapper", goos, arch)
	}
	switch goos {
	case "windows":
		return "steamcmd.zip", nil
	case "linux":
		return "steamcmd_linux.tar.gz", nil
	case "darwin":
		return "steamcmd_osx.tar.gz", nil
	}
	return "", errors.New("SteamCMD is supported on Windows, Linux, and macOS")
}
func BinaryRelative(goos string) string {
	if goos == "windows" {
		return "steamcmd.exe"
	}
	if goos == "darwin" {
		return filepath.Join("MacOS", "steamcmd.sh")
	}
	return "steamcmd.sh"
}
func (m *Manager) lock(ctx context.Context) (*flock.Flock, error) {
	if e := os.MkdirAll(m.DataDir, 0700); e != nil {
		return nil, e
	}
	l := flock.New(filepath.Join(m.DataDir, "steamcmd.lock"))
	ok, e := l.TryLockContext(ctx, 200*time.Millisecond)
	if e != nil {
		return nil, e
	}
	if !ok {
		return nil, errors.New("SteamCMD is busy")
	}
	return l, nil
}
func (m *Manager) Find() (string, error) {
	if m.Path != "" {
		p, e := filepath.Abs(m.Path)
		if e != nil {
			return "", e
		}
		s, e := os.Stat(p)
		if e != nil {
			return "", e
		}
		if !s.Mode().IsRegular() {
			return "", errors.New("SteamCMD path is not a regular file")
		}
		return p, nil
	}
	managed := filepath.Join(m.DataDir, "steamcmd", BinaryRelative(runtime.GOOS))
	if s, e := os.Stat(managed); e == nil && s.Mode().IsRegular() {
		return managed, nil
	}
	if p, e := exec.LookPath("steamcmd"); e == nil {
		return p, nil
	}
	return "", os.ErrNotExist
}
func (m *Manager) Ensure(ctx context.Context) (string, error) {
	l, e := m.lock(ctx)
	if e != nil {
		return "", e
	}
	defer l.Unlock()
	return m.ensure(ctx)
}
func (m *Manager) ensure(ctx context.Context) (string, error) {
	if p, e := m.Find(); e == nil {
		return p, nil
	} else if m.Path != "" {
		return "", e
	}
	if m.NoDownload || m.HTTP.Offline {
		return "", errors.New("SteamCMD not installed; run steam cmd install online or set --steamcmd-path")
	}
	name, e := ArchiveName(runtime.GOOS, runtime.GOARCH)
	if e != nil {
		return "", e
	}
	if m.Progress != nil {
		fmt.Fprintf(m.Progress, "Downloading Valve %s…\n", name)
	}
	data, e := m.HTTP.Do(ctx, "GET", CDN+name, nil, nil, nil)
	if e != nil {
		return "", e
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	if m.SHA256 != "" && !strings.EqualFold(m.SHA256, hash) {
		return "", errors.New("SteamCMD bootstrap SHA-256 mismatch")
	}
	stage, e := os.MkdirTemp(m.DataDir, ".steamcmd-install-*")
	if e != nil {
		return "", e
	}
	defer os.RemoveAll(stage)
	dest := stage
	if runtime.GOOS == "darwin" {
		dest = filepath.Join(stage, "MacOS")
		if e = os.Mkdir(dest, 0700); e != nil {
			return "", e
		}
	}
	if e = Extract(data, name, dest); e != nil {
		return "", e
	}
	bin := filepath.Join(stage, BinaryRelative(runtime.GOOS))
	s, e := os.Stat(bin)
	if e != nil || !s.Mode().IsRegular() {
		return "", errors.New("Valve archive did not contain expected SteamCMD executable")
	}
	meta, _ := json.MarshalIndent(map[string]string{"source": CDN + name, "sha256": hash, "downloaded_at": time.Now().UTC().Format(time.RFC3339)}, "", "  ")
	if e = os.WriteFile(filepath.Join(stage, "bootstrap.json"), meta, 0600); e != nil {
		return "", e
	}
	root := filepath.Join(m.DataDir, "steamcmd")
	if e = os.Rename(stage, root); e != nil {
		return "", fmt.Errorf("install SteamCMD (existing incomplete directory must be moved aside): %w", e)
	}
	if m.Progress != nil {
		fmt.Fprintln(m.Progress, "Bootstrap installed. Valve will self-update on first execution.")
	}
	return filepath.Join(root, BinaryRelative(runtime.GOOS)), nil
}

// Extract rejects traversal, hardlinks, devices, duplicates, and escaping links.
// Framework symlinks are installed last, after all file writes have completed.
// The caller must provide a newly created private staging directory.
func Extract(data []byte, name, dest string) error {
	var total int64
	type link struct{ name, target string }
	var links []link
	safePath := func(name string) (string, error) {
		name = strings.ReplaceAll(name, "\\", "/")
		if strings.ContainsAny(name, ":\x00") {
			return "", errors.New("invalid archive path")
		}
		for _, p := range strings.Split(name, "/") {
			if p == ".." {
				return "", errors.New("archive path traversal")
			}
		}
		if !filepath.IsLocal(filepath.FromSlash(name)) {
			return "", errors.New("non-local archive path")
		}
		return filepath.Join(dest, filepath.FromSlash(name)), nil
	}
	write := func(name string, mode os.FileMode, size int64, r io.Reader) error {
		path, e := safePath(name)
		if e != nil {
			return e
		}
		if mode.IsDir() {
			return os.MkdirAll(path, 0700)
		}
		if !mode.IsRegular() {
			return errors.New("archive contains a special file")
		}
		if size < 0 || size > maxExpanded-total {
			return errors.New("archive expansion exceeds 256 MiB")
		}
		total += size
		if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
			return e
		}
		perm := os.FileMode(0600)
		if mode&0111 != 0 {
			perm = 0700
		}
		f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
		if e != nil {
			return e
		}
		n, e := io.Copy(f, io.LimitReader(r, size+1))
		ce := f.Close()
		if e != nil {
			return e
		}
		if ce != nil {
			return ce
		}
		if n != size {
			return errors.New("archive entry size mismatch")
		}
		return nil
	}
	if strings.HasSuffix(name, ".zip") {
		z, e := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if e != nil {
			return e
		}
		if len(z.File) > 10000 {
			return errors.New("archive has too many entries")
		}
		for _, f := range z.File {
			if f.UncompressedSize64 > uint64(maxExpanded) {
				return errors.New("archive entry too large")
			}
			r, e := f.Open()
			if e != nil {
				return e
			}
			e = write(f.Name, f.Mode(), int64(f.UncompressedSize64), r)
			r.Close()
			if e != nil {
				return e
			}
		}
		return nil
	}
	g, e := gzip.NewReader(bytes.NewReader(data))
	if e != nil {
		return e
	}
	defer g.Close()
	t := tar.NewReader(g)
	for count := 0; ; count++ {
		h, e := t.Next()
		if e == io.EOF {
			n, e := io.Copy(io.Discard, io.LimitReader(g, maxExpanded-total+1))
			if e != nil {
				return e
			}
			if n > maxExpanded-total {
				return errors.New("archive trailer exceeds expansion limit")
			}
			break
		}
		if e != nil {
			return e
		}
		if count >= 10000 {
			return errors.New("archive has too many entries")
		}
		if h.Typeflag == tar.TypeSymlink {
			if _, e = safePath(h.Name); e != nil {
				return e
			}
			// Valve's framework links are relative, with no upward traversal.
			if _, e = safePath(h.Linkname); e != nil {
				return errors.New("unsafe archive symlink target")
			}
			links = append(links, link{h.Name, h.Linkname})
			continue
		}
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeDir {
			return errors.New("archive contains a hardlink or special entry")
		}
		if e = write(h.Name, h.FileInfo().Mode(), h.Size, t); e != nil {
			return e
		}
	}
	for _, l := range links {
		path, e := safePath(l.name)
		if e != nil {
			return e
		}
		if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
			return e
		}
		// Never create a link through an already-created link ancestor.
		for parent := filepath.Dir(path); parent != filepath.Clean(dest); parent = filepath.Dir(parent) {
			st, e := os.Lstat(parent)
			if e != nil {
				return e
			}
			if st.Mode()&os.ModeSymlink != 0 {
				return errors.New("symlink parent in archive")
			}
		}
		if e = os.Symlink(filepath.FromSlash(l.target), path); e != nil {
			return e
		}
	}
	for _, l := range links {
		path, _ := safePath(l.name)
		resolved, e := filepath.EvalSymlinks(path)
		if e != nil {
			return errors.New("dangling or cyclic archive symlink")
		}
		relative, e := filepath.Rel(dest, resolved)
		if e != nil || !filepath.IsLocal(relative) {
			return errors.New("archive symlink escapes destination")
		}
	}
	return nil
}
