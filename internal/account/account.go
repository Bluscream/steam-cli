package account

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"steamcli.local/steam/internal/library"
	"steamcli.local/steam/internal/steamvdf"
)

type User struct {
	SteamID64        string `json:"steamid64"`
	AccountName      string `json:"account_name"`
	PersonaName      string `json:"persona_name"`
	Timestamp        int64  `json:"timestamp"`
	AutoLogin        bool   `json:"auto_login"`
	MostRecent       bool   `json:"most_recent"`
	RememberPassword bool   `json:"remember_password"`
	WantsOffline     bool   `json:"wants_offline"`
	ASFBot           string `json:"asf_bot,omitempty"`
}

// List returns all accounts known to loginusers.vdf across the provided Steam roots.
func List(roots []string) ([]User, error) {
	if len(roots) == 0 {
		roots = library.Defaults()
	}
	var out []User
	seen := map[string]bool{}

	for _, root := range roots {
		path := filepath.Join(root, "config", "loginusers.vdf")
		m, err := steamvdf.Parse(path)
		if err != nil {
			continue
		}
		users, ok := m["users"].(map[string]any)
		if !ok {
			users, _ = m["Users"].(map[string]any)
		}
		for id, u := range users {
			if seen[id] {
				continue
			}
			data, ok := u.(map[string]any)
			if !ok {
				continue
			}
			seen[id] = true
			var ts int64
			if tStr := steamvdf.Str(data["Timestamp"]); tStr != "" {
				ts, _ = strconv.ParseInt(tStr, 10, 64)
			}
			persona := steamvdf.Str(data["PersonaName"])
			if persona == "" {
				persona = steamvdf.Str(data["personaname"])
			}
			acc := steamvdf.Str(data["AccountName"])
			if acc == "" {
				acc = steamvdf.Str(data["accountname"])
			}
			out = append(out, User{
				SteamID64:        id,
				AccountName:      acc,
				PersonaName:      persona,
				Timestamp:        ts,
				AutoLogin:        steamvdf.Str(data["AutoLogin"]) == "1",
				MostRecent:       steamvdf.Str(data["MostRecent"]) == "1",
				RememberPassword: steamvdf.Str(data["RememberPassword"]) == "1",
				WantsOffline:     steamvdf.Str(data["WantsOfflineMode"]) == "1",
			})
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].AutoLogin != out[j].AutoLogin {
			return out[i].AutoLogin
		}
		if out[i].MostRecent != out[j].MostRecent {
			return out[i].MostRecent
		}
		return out[i].Timestamp > out[j].Timestamp
	})
	return out, nil
}

// Active returns the currently active/autologin user.
func Active(roots []string) (User, error) {
	users, err := List(roots)
	if err != nil {
		return User{}, err
	}
	if len(users) == 0 {
		return User{}, errors.New("no accounts found in Steam loginusers.vdf")
	}
	for _, u := range users {
		if u.AutoLogin || u.MostRecent {
			return u, nil
		}
	}
	return users[0], nil
}

// Switch changes the active Steam account to the user matching target (SteamID64 or AccountName).
func Switch(roots []string, target string) (User, error) {
	if len(roots) == 0 {
		roots = library.Defaults()
	}
	users, err := List(roots)
	if err != nil {
		return User{}, err
	}
	var matched *User
	targetLow := strings.ToLower(strings.TrimSpace(target))
	for i := range users {
		if users[i].SteamID64 == target || strings.ToLower(users[i].AccountName) == targetLow || strings.ToLower(users[i].PersonaName) == targetLow {
			matched = &users[i]
			break
		}
	}
	if matched == nil {
		return User{}, fmt.Errorf("no known Steam account matching %q", target)
	}

	for _, root := range roots {
		loginPath := filepath.Join(root, "config", "loginusers.vdf")
		m, err := steamvdf.Parse(loginPath)
		if err != nil {
			continue
		}
		usersNode, ok := m["users"].(map[string]any)
		if !ok {
			usersNode, _ = m["Users"].(map[string]any)
		}
		if usersNode != nil {
			for id, u := range usersNode {
				data, ok := u.(map[string]any)
				if !ok {
					continue
				}
				if id == matched.SteamID64 {
					steamvdf.Set(data, "AutoLogin", "1")
					steamvdf.Set(data, "MostRecent", "1")
				} else {
					steamvdf.Set(data, "AutoLogin", "0")
					steamvdf.Set(data, "MostRecent", "0")
				}
			}
			_ = steamvdf.Write(loginPath, m)
		}

		// Update registry.vdf on Unix systems
		if runtime.GOOS != "windows" {
			regPath := registryPath(root)
			if regPath != "" {
				if reg, err := steamvdf.Parse(regPath); err == nil {
					steamNode := steamvdf.Section(reg, "Registry", "HKCU", "Software", "Valve", "Steam")
					steamvdf.Set(steamNode, "AutoLoginUser", matched.AccountName)
					steamvdf.Set(steamNode, "RememberPassword", "1")
					_ = steamvdf.Write(regPath, reg)
				}
			}
		}
	}

	matched.AutoLogin = true
	matched.MostRecent = true
	return *matched, nil
}

// Forget removes an account from loginusers.vdf across all roots.
func Forget(roots []string, target string) error {
	if len(roots) == 0 {
		roots = library.Defaults()
	}
	targetLow := strings.ToLower(strings.TrimSpace(target))
	found := false

	for _, root := range roots {
		loginPath := filepath.Join(root, "config", "loginusers.vdf")
		m, err := steamvdf.Parse(loginPath)
		if err != nil {
			continue
		}
		usersNode, ok := m["users"].(map[string]any)
		if !ok {
			usersNode, _ = m["Users"].(map[string]any)
		}
		if usersNode != nil {
			for id, u := range usersNode {
				data, ok := u.(map[string]any)
				if !ok {
					continue
				}
				acc := steamvdf.Str(data["AccountName"])
				persona := steamvdf.Str(data["PersonaName"])
				if id == target || strings.ToLower(acc) == targetLow || strings.ToLower(persona) == targetLow {
					delete(usersNode, id)
					found = true
				}
			}
			if found {
				_ = steamvdf.Write(loginPath, m)
			}
		}
	}
	if !found {
		return fmt.Errorf("account %q not found", target)
	}
	return nil
}

func registryPath(steamRoot string) string {
	if steamRoot != "" {
		steamRoot = filepath.Clean(steamRoot)
		share := filepath.Dir(steamRoot)
		local := filepath.Dir(share)
		if filepath.Base(share) == "share" && filepath.Base(local) == ".local" {
			p := filepath.Join(filepath.Dir(local), ".steam", "registry.vdf")
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	h, err := os.UserHomeDir()
	if err != nil || h == "" {
		return ""
	}
	return filepath.Join(h, ".steam", "registry.vdf")
}
