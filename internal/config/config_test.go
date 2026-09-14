package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestProfilesAndSecretPrecedence(t *testing.T) {
	t.Setenv("STEAM_WEB_API_KEY", "primary")
	t.Setenv("STEAM_API_KEY", "alias")
	t.Setenv("CUSTOM_STEAM_KEY", "custom")
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	if e := os.WriteFile(p, []byte(`{"default_profile":"work","profiles":{"work":{"web_key_env":"CUSTOM_STEAM_KEY"},"home":{}}}`), 0600); e != nil {
		t.Fatal(e)
	}
	s, e := Load(p, "")
	if e != nil {
		t.Fatal(e)
	}
	if v, _ := s.WebKey(); v != "custom" {
		t.Fatal("custom profile ignored")
	}
	s, e = Load(p, "home")
	if e != nil {
		t.Fatal(e)
	}
	if v, _ := s.WebKey(); v != "primary" {
		t.Fatal("primary not preferred")
	}
	if _, e = Load(p, "missing"); e == nil {
		t.Fatal("missing profile accepted")
	}
	if _, e = Load(filepath.Join(dir, "missing"), ""); e == nil {
		t.Fatal("explicit missing config ignored")
	}
}
func TestInitNeverOverwrites(t *testing.T) {
	p := filepath.Join(t.TempDir(), "nested", "config.json")
	if e := Init(p); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(p)
	if strings.Contains(string(b), "api_key\"") {
		t.Fatal("secret field")
	}
	if e := Init(p); e == nil {
		t.Fatal("overwrote config")
	}
	if runtime.GOOS != "windows" {
		s, _ := os.Stat(p)
		if s.Mode().Perm() != 0600 {
			t.Fatal("permissions", s.Mode())
		}
	}
}
func TestSecretFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "secret")
	os.WriteFile(p, []byte("from-file\n"), 0600)
	v, e := Secret("NONEXISTENT_STEAMCLI_TEST_KEY", p)
	if e != nil || v != "from-file" {
		t.Fatal(v, e)
	}
}

func TestSecretPreservesSpacesAndBoundsFile(t *testing.T) {
	t.Setenv("SPACED_SECRET", " leading and trailing ")
	v, e := Secret("SPACED_SECRET", "")
	if e != nil || v != " leading and trailing " {
		t.Fatal("password whitespace changed")
	}
	p := filepath.Join(t.TempDir(), "secret")
	os.WriteFile(p, []byte(" spaces \r\n"), 0600)
	v, e = Secret("", p)
	if e != nil || v != " spaces " {
		t.Fatal("file password whitespace changed")
	}
	os.WriteFile(p, []byte(strings.Repeat("x", 65537)), 0600)
	if _, e = Secret("", p); e == nil {
		t.Fatal("oversized secret accepted")
	}
}

func TestProfileAllowHTTP(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"default_profile":"lan","profiles":{
		"lan":{"asf_url":"http://192.168.0.2:1242","allow_http":true},
		"strict":{"asf_url":"http://192.168.0.2:1242"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	lan, err := Load(path, "lan")
	if err != nil {
		t.Fatal(err)
	}
	if !lan.AllowHTTP {
		t.Error("allow_http should be honored from the profile")
	}
	strict, err := Load(path, "strict")
	if err != nil {
		t.Fatal(err)
	}
	if strict.AllowHTTP {
		t.Error("allow_http must default to false")
	}
}
