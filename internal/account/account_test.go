package account

import (
	"os"
	"path/filepath"
	"testing"
)

const testLoginUsers = `"users"
{
	"76561198022446661"
	{
		"AccountName"		"user_alpha"
		"PersonaName"		"Alpha"
		"RememberPassword"	"1"
		"WantsOfflineMode"	"0"
		"AutoLogin"		"1"
		"Timestamp"		"1700000000"
	}
	"76561199078918384"
	{
		"AccountName"		"user_beta"
		"PersonaName"		"Beta"
		"RememberPassword"	"1"
		"WantsOfflineMode"	"0"
		"AutoLogin"		"0"
		"Timestamp"		"1690000000"
	}
}
`

func TestListAndSwitch(t *testing.T) {
	tmp := t.TempDir()
	cfgDir := filepath.Join(tmp, "config")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	vdfPath := filepath.Join(cfgDir, "loginusers.vdf")
	if err := os.WriteFile(vdfPath, []byte(testLoginUsers), 0o644); err != nil {
		t.Fatal(err)
	}

	// 1. List
	users, err := List([]string{tmp})
	if err != nil {
		t.Fatalf("List error: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("expected 2 users, got %d", len(users))
	}
	if users[0].AccountName != "user_alpha" || !users[0].AutoLogin {
		t.Fatalf("expected user_alpha to be active autologin, got %+v", users[0])
	}

	// 2. Active
	active, err := Active([]string{tmp})
	if err != nil {
		t.Fatalf("Active error: %v", err)
	}
	if active.AccountName != "user_alpha" {
		t.Fatalf("expected user_alpha, got %s", active.AccountName)
	}

	// 3. Switch to Beta
	switched, err := Switch([]string{tmp}, "user_beta")
	if err != nil {
		t.Fatalf("Switch error: %v", err)
	}
	if switched.AccountName != "user_beta" || !switched.AutoLogin {
		t.Fatalf("expected switched to be user_beta, got %+v", switched)
	}

	// Verify persistence in file
	usersAfter, err := List([]string{tmp})
	if err != nil {
		t.Fatalf("List error: %v", err)
	}
	if !usersAfter[0].AutoLogin || usersAfter[0].AccountName != "user_beta" {
		t.Fatalf("expected user_beta to now be autologin, got %+v", usersAfter[0])
	}

	// 4. Forget
	if err := Forget([]string{tmp}, "user_alpha"); err != nil {
		t.Fatalf("Forget error: %v", err)
	}
	usersRemaining, _ := List([]string{tmp})
	if len(usersRemaining) != 1 || usersRemaining[0].AccountName != "user_beta" {
		t.Fatalf("expected 1 user (user_beta), got %d: %+v", len(usersRemaining), usersRemaining)
	}
}
