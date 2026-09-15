// Package sdk generates a native Steamworks adapter from a user-supplied SDK.
// Valve headers and runtime libraries are never bundled into the Go binary.
package sdk

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/gofrs/flock"
)

//go:embed native/host.cpp native/json.hpp native/LICENSE.json
var sources embed.FS

const maxFile = 32 << 20

type Parameter struct {
	Name string `json:"paramname"`
	Type string `json:"paramtype"`
}
type Accessor struct {
	Kind string `json:"kind"`
	Name string `json:"name_flat"`
}
type Method struct {
	Name      string      `json:"methodname"`
	Symbol    string      `json:"methodname_flat"`
	Return    string      `json:"returntype"`
	Params    []Parameter `json:"params"`
	Interface string      `json:"interface"`
	Accessors []Accessor  `json:"accessors,omitempty"`
}
type Definition struct {
	Class     string     `json:"classname"`
	Struct    string     `json:"struct"`
	Methods   []Method   `json:"methods"`
	Accessors []Accessor `json:"accessors"`
}
type Schema struct {
	Interfaces []Definition `json:"interfaces"`
	Structs    []Definition `json:"structs"`
	Callbacks  []Definition `json:"callback_structs"`
}

func ReadSchema(dir string) (Schema, []byte, error) {
	p := filepath.Join(dir, "public", "steam", "steam_api.json")
	b, e := readBounded(p)
	if e != nil {
		return Schema{}, nil, fmt.Errorf("read Steamworks SDK metadata: %w", e)
	}
	var s Schema
	if e = json.Unmarshal(b, &s); e != nil {
		return s, nil, e
	}
	if len(s.Interfaces) == 0 {
		return s, nil, errors.New("SDK metadata contains no interfaces")
	}
	return s, b, nil
}
func readBounded(p string) ([]byte, error) {
	f, e := os.Open(p)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, maxFile+1))
	if len(b) > maxFile {
		return nil, errors.New("SDK file exceeds 32 MiB")
	}
	return b, e
}
func (s Schema) Methods() []Method {
	var out []Method
	for _, d := range append(append([]Definition{}, s.Interfaces...), s.Structs...) {
		name := d.Class
		if name == "" {
			name = d.Struct
		}
		for _, m := range d.Methods {
			m.Interface = name
			m.Accessors = d.Accessors
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Symbol < out[j].Symbol })
	return out
}
func (s Schema) Resolve(iface, name string) (Method, error) {
	var matches []Method
	for _, m := range s.Methods() {
		if strings.EqualFold(iface, m.Interface) && (name == m.Symbol || strings.EqualFold(name, m.Name)) {
			matches = append(matches, m)
		}
	}
	if len(matches) != 1 {
		return Method{}, fmt.Errorf("expected one SDK method for %s %s, found %d; use the exact flat symbol for overloads", iface, name, len(matches))
	}
	return matches[0], nil
}

var identifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func Generate(s Schema) ([]byte, error) {
	host, _ := sources.ReadFile("native/host.cpp")
	var b bytes.Buffer
	b.Write(host)
	b.WriteString("\nvoid register_methods(){\n")
	seen := map[string]bool{}
	for _, m := range s.Methods() {
		if !identifier.MatchString(m.Symbol) || seen[m.Symbol] {
			return nil, fmt.Errorf("invalid or duplicate SDK symbol %q", m.Symbol)
		}
		seen[m.Symbol] = true
		accessor, serverAccessor := "", ""
		for _, a := range m.Accessors {
			if !identifier.MatchString(a.Name) {
				return nil, errors.New("invalid SDK accessor")
			}
			if a.Kind == "gameserver" {
				serverAccessor = a.Name
			} else {
				accessor = a.Name
			}
		}
		if m.Interface == "ISteamClient" {
			accessor = "@client"
		}
		fmt.Fprintf(&b, "methods[%q]={%q,%q,[](Library&l,Arena&a,const json&v){return invoke(l.get<decltype(&%s)>(%q),v,a);}};\n", m.Symbol, accessor, serverAccessor, m.Symbol, m.Symbol)
	}
	for _, d := range append(append([]Definition{}, s.Structs...), s.Callbacks...) {
		if !identifier.MatchString(d.Struct) {
			return nil, fmt.Errorf("invalid SDK type %q", d.Struct)
		}
		fmt.Fprintf(&b, "layout<%s>(%q);\n", d.Struct, d.Struct)
	}
	for _, d := range s.Callbacks {
		fmt.Fprintf(&b, "callback<%s>(%q);\n", d.Struct, d.Struct)
	}
	b.WriteString("}\n")
	return b.Bytes(), nil
}

type Build struct {
	Helper   string `json:"helper"`
	Library  string `json:"library"`
	SDKDir   string `json:"sdk_dir"`
	Digest   string `json:"digest"`
	Methods  int    `json:"methods"`
	Platform string `json:"platform"`
}

func Load(dataDir string) (Build, error) {
	var b Build
	raw, e := readBounded(filepath.Join(dataDir, "sdk", "current.json"))
	if e != nil {
		return b, errors.New("native helper is not built; run steamcli sdk build --sdk-dir /path/to/sdk")
	}
	e = json.Unmarshal(raw, &b)
	if e == nil && b.Platform != runtime.GOOS+"/"+runtime.GOARCH {
		e = errors.New("native helper was built for another platform; run sdk build")
	}
	return b, e
}
func RuntimePath(dir string) (string, error) {
	var rel string
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "linux/amd64":
		rel = "linux64/libsteam_api.so"
	case "linux/arm64":
		rel = "linuxarm64/libsteam_api.so"
	case "darwin/amd64", "darwin/arm64":
		rel = "osx/libsteam_api.dylib"
	case "windows/amd64":
		rel = "win64/steam_api64.dll"
	default:
		return "", errors.New("this SDK runtime platform is unsupported; supply --library for a compatible runtime")
	}
	p := filepath.Join(dir, "redistributable_bin", rel)
	if _, e := os.Stat(p); e != nil {
		return "", e
	}
	return p, nil
}
func Compile(ctx context.Context, dir, dataDir, compiler, library string, progress io.Writer) (Build, error) {
	var result Build
	dir, e := filepath.Abs(dir)
	if e != nil {
		return result, e
	}
	s, _, e := ReadSchema(dir)
	if e != nil {
		return result, e
	}
	generated, e := Generate(s)
	if e != nil {
		return result, e
	}
	if library == "" {
		library, e = RuntimePath(dir)
		if e != nil {
			return result, e
		}
	}
	library, e = filepath.Abs(library)
	if e != nil {
		return result, e
	}
	if _, e = os.Stat(library); e != nil {
		return result, e
	}
	if compiler == "" {
		compiler = os.Getenv("CXX")
	}
	if compiler == "" {
		if runtime.GOOS == "windows" {
			compiler = "clang++"
		} else {
			compiler = "c++"
		}
	}
	cpp, e := exec.LookPath(compiler)
	if e != nil {
		return result, fmt.Errorf("a C++17 compiler is needed once to build the native helper: %w", e)
	}
	header, _ := sources.ReadFile("native/json.hpp")
	hash := sha256.New()
	hash.Write(generated)
	hash.Write(header)
	hash.Write([]byte(runtime.GOOS + "/" + runtime.GOARCH + cpp))
	e = filepath.WalkDir(filepath.Join(dir, "public"), func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(p, ".h") {
			b, e := readBounded(p)
			if e != nil {
				return e
			}
			rel, _ := filepath.Rel(dir, p)
			hash.Write([]byte(rel))
			hash.Write(b)
		}
		return nil
	})
	if e != nil {
		return result, e
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	target := filepath.Join(dataDir, "sdk", digest[:24])
	if e = os.MkdirAll(target, 0700); e != nil {
		return result, e
	}
	lock := flock.New(filepath.Join(target, "build.lock"))
	ok, e := lock.TryLockContext(ctx, 100*time.Millisecond)
	if e != nil {
		return result, e
	}
	if !ok {
		return result, errors.New("could not acquire SDK build lock")
	}
	defer lock.Unlock()
	name := "steamcli-sdk"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	helper := filepath.Join(target, name)
	if _, e = os.Stat(helper); e != nil {
		stage, e := os.MkdirTemp(target, "build-")
		if e != nil {
			return result, e
		}
		defer os.RemoveAll(stage)
		for name, b := range map[string][]byte{"host.cpp": generated, "json.hpp": header} {
			if e = os.WriteFile(filepath.Join(stage, name), b, 0600); e != nil {
				return result, e
			}
		}
		args := []string{"-std=c++17", "-O1", "-I", filepath.Join(dir, "public"), filepath.Join(stage, "host.cpp"), "-o", filepath.Join(stage, name)}
		if runtime.GOOS == "linux" {
			args = append(args, "-ldl", "-pthread")
		}
		command := exec.CommandContext(ctx, cpp, args...)
		command.Stdout = progress
		command.Stderr = progress
		if e = command.Run(); e != nil {
			return result, fmt.Errorf("compile native SDK helper: %w", e)
		}
		if e = os.Rename(filepath.Join(stage, name), helper); e != nil {
			return result, e
		}
	}
	result = Build{Helper: helper, Library: library, SDKDir: dir, Digest: digest, Methods: len(s.Methods()), Platform: runtime.GOOS + "/" + runtime.GOARCH}
	b, _ := json.MarshalIndent(result, "", "  ")
	if e = atomicWrite(filepath.Join(dataDir, "sdk", "current.json"), b); e != nil {
		return result, e
	}
	return result, nil
}
func atomicWrite(p string, b []byte) error {
	f, e := os.CreateTemp(filepath.Dir(p), ".sdk-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if _, e = f.Write(b); e != nil {
		f.Close()
		return e
	}
	if e = f.Sync(); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	return os.Rename(f.Name(), p)
}

// Run keeps all native work on the helper's main thread. Stdout carries JSONL;
// SDK diagnostic output is redirected to stderr by the helper itself.
func Run(ctx context.Context, b Build, appID string, input io.Reader, out, errOut io.Writer) error {
	command := exec.CommandContext(ctx, b.Helper, b.Library)
	command.Stdin = input
	command.Stdout = out
	command.Stderr = errOut
	command.WaitDelay = 2 * time.Second
	var env []string
	for _, s := range os.Environ() {
		name, _, _ := strings.Cut(s, "=")
		switch strings.ToUpper(name) {
		case "STEAMAPPID", "STEAMGAMEID", "STEAM_API_KEY", "STEAM_ACCESS_TOKEN", "ASF_IPC_PASSWORD", "STEAM_LOGIN_SECURE":
			continue
		}
		env = append(env, s)
	}
	if appID != "" {
		env = append(env, "SteamAppId="+appID, "SteamGameId="+appID)
	}
	command.Env = env
	if e := command.Run(); e != nil {
		return fmt.Errorf("native SDK helper failed (see its JSON result or stderr): %w", e)
	}
	return nil
}
