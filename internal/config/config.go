// Package config holds non-secret settings. Secrets are resolved only at use time.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type Profile struct {
	AccessTokenEnv           string `json:"access_token_env,omitempty"`
	AccessTokenFile          string `json:"access_token_file,omitempty"`
	WebURL                   string `json:"web_url,omitempty"`
	WebKeyEnv                string `json:"web_key_env,omitempty"`
	WebKeyFile               string `json:"web_key_file,omitempty"`
	CommunityURL             string `json:"community_url,omitempty"`
	CommunityLoginSecureEnv  string `json:"community_login_secure_env,omitempty"`
	CommunityLoginSecureFile string `json:"community_login_secure_file,omitempty"`
	SteamUserIDEnv           string `json:"steam_user_id_env,omitempty"`
	SteamUserIDFile          string `json:"steam_user_id_file,omitempty"`
	ASFURL                   string `json:"asf_url,omitempty"`
	ASFPasswordEnv           string `json:"asf_password_env,omitempty"`
	ASFPasswordFile          string `json:"asf_password_file,omitempty"`
	SteamCMDPath             string `json:"steamcmd_path,omitempty"`
	SteamClientPath          string `json:"steam_client_path,omitempty"`
	// SteamClientArgs are prepended to every desktop-client launch made
	// through "steamcli client", for options you always want, such as
	// -console. STEAM_CLIENT_ARGS overrides it, split on whitespace.
	SteamClientArgs []string `json:"steam_client_args,omitempty"`
	// AllowHTTP permits plaintext HTTP outside loopback for this profile's
	// hosts. Intended for a trusted LAN service such as an ASF instance; the
	// --allow-http flag turns it on for a single run instead.
	AllowHTTP bool `json:"allow_http,omitempty"`
}
type File struct {
	DefaultProfile string             `json:"default_profile"`
	Profiles       map[string]Profile `json:"profiles"`
}
type Settings struct {
	Profile
	ConfigPath, DataDir, CacheDir, ProfileName string
}

func Paths() (config, data, cache string, err error) {
	c, e := os.UserConfigDir()
	if e != nil {
		return "", "", "", e
	}
	k, e := os.UserCacheDir()
	if e != nil {
		return "", "", "", e
	}
	// UserConfigDir handles Windows/macOS conventions. XDG_DATA_HOME on Unix
	// keeps persistent SteamCMD files separate from discardable schema caches.
	d := filepath.Join(c, "steam-cli", "data")
	if runtime.GOOS != "windows" && runtime.GOOS != "darwin" {
		home, e := os.UserHomeDir()
		if e != nil {
			return "", "", "", e
		}
		d = filepath.Join(home, ".local", "share", "steam-cli")
		if x := os.Getenv("XDG_DATA_HOME"); x != "" {
			d = filepath.Join(x, "steam-cli")
		}
	}
	return filepath.Join(c, "steam-cli", "config.json"), d, filepath.Join(k, "steam-cli"), nil
}
func Load(path, name string) (Settings, error) {
	cp, d, k, e := Paths()
	if e != nil {
		return Settings{}, e
	}
	explicit := path != ""
	if path == "" {
		path = cp
	}
	f := File{DefaultProfile: "default", Profiles: map[string]Profile{"default": {}}}
	b, e := os.ReadFile(path)
	if e == nil {
		if e = json.Unmarshal(b, &f); e != nil {
			return Settings{}, errors.New("invalid configuration JSON")
		}
	} else if !errors.Is(e, os.ErrNotExist) || explicit {
		return Settings{}, fmt.Errorf("read configuration: %w", e)
	}
	if name == "" {
		name = f.DefaultProfile
	}
	if name == "" {
		name = "default"
	}
	p, ok := f.Profiles[name]
	if !ok {
		return Settings{}, fmt.Errorf("profile %q does not exist", name)
	}
	if p.WebURL == "" {
		p.WebURL = "https://api.steampowered.com"
	}
	if p.ASFURL == "" {
		p.ASFURL = "http://127.0.0.1:1242"
	}
	if p.CommunityURL == "" {
		p.CommunityURL = "https://steamcommunity.com"
	}
	if p.AccessTokenEnv == "" {
		p.AccessTokenEnv = "STEAM_ACCESS_TOKEN"
	}
	if p.CommunityLoginSecureEnv == "" {
		p.CommunityLoginSecureEnv = "STEAM_LOGIN_SECURE"
	}
	if p.SteamUserIDEnv == "" {
		p.SteamUserIDEnv = "STEAM_USER_ID"
	}
	if p.WebKeyEnv == "" {
		p.WebKeyEnv = "STEAM_API_KEY"
	}
	if p.ASFPasswordEnv == "" {
		p.ASFPasswordEnv = "ASF_IPC_PASSWORD"
	}
	for _, v := range []struct {
		env string
		dst *string
	}{{"STEAM_WEB_URL", &p.WebURL}, {"STEAM_COMMUNITY_URL", &p.CommunityURL}, {"STEAM_ASF_URL", &p.ASFURL}, {"STEAMCMD_PATH", &p.SteamCMDPath}, {"STEAM_CLIENT_PATH", &p.SteamClientPath}, {"STEAM_CLI_DATA_DIR", &d}, {"STEAM_CLI_CACHE_DIR", &k}} {
		if s := os.Getenv(v.env); s != "" {
			*v.dst = s
		}
	}
	if v, ok := os.LookupEnv("STEAM_CLIENT_ARGS"); ok {
		p.SteamClientArgs = strings.Fields(v)
	}
	for _, raw := range []string{p.WebURL, p.ASFURL, p.CommunityURL} {
		u, e := url.Parse(raw)
		if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
			return Settings{}, errors.New("configured URLs must be HTTP(S) URLs without credentials, query, or fragment")
		}
	}
	d, e = filepath.Abs(d)
	if e != nil {
		return Settings{}, e
	}
	k, e = filepath.Abs(k)
	if e != nil {
		return Settings{}, e
	}
	return Settings{p, path, d, k, name}, nil
}
func Secret(env, file string, aliases ...string) (string, error) {
	for _, k := range append([]string{env}, aliases...) {
		if k != "" {
			if v, ok := os.LookupEnv(k); ok && v != "" {
				return v, nil
			}
		}
	}
	if file == "" {
		return "", nil
	}
	f, e := os.Open(file)
	if e != nil {
		return "", fmt.Errorf("read secret file: %w", e)
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, 65537))
	if e != nil {
		return "", fmt.Errorf("read secret file: %w", e)
	}
	if len(b) > 65536 {
		return "", errors.New("secret file exceeds 64 KiB")
	}
	return strings.TrimRight(string(b), "\r\n"), nil
}
func (s Settings) WebKey() (string, error) {
	return Secret(s.WebKeyEnv, s.WebKeyFile)
}
func (s Settings) ASFPassword() (string, error) { return Secret(s.ASFPasswordEnv, s.ASFPasswordFile) }
func Init(path string) error {
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(File{DefaultProfile: "default", Profiles: map[string]Profile{"default": {WebURL: "https://api.steampowered.com", CommunityURL: "https://steamcommunity.com", ASFURL: "http://127.0.0.1:1242", WebKeyEnv: "STEAM_API_KEY", AccessTokenEnv: "STEAM_ACCESS_TOKEN", CommunityLoginSecureEnv: "STEAM_LOGIN_SECURE", SteamUserIDEnv: "STEAM_USER_ID", ASFPasswordEnv: "ASF_IPC_PASSWORD"}}})
}

func (s Settings) AccessToken() (string, error) {
	return Secret(s.AccessTokenEnv, s.AccessTokenFile)
}

// CommunityLoginSecure resolves the steamLoginSecure browser cookie used for
// Community endpoints that the Web API does not expose.
func (s Settings) CommunityLoginSecure() (string, error) {
	return Secret(s.CommunityLoginSecureEnv, s.CommunityLoginSecureFile)
}

// SteamUserID resolves an explicit SteamID / Steam user ID from environment or secret file.
func (s Settings) SteamUserID() (string, error) {
	return Secret(s.SteamUserIDEnv, s.SteamUserIDFile, "STEAM_STEAMID", "STEAMID64", "STEAMID")
}
