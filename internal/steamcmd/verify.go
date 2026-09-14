package steamcmd

import (
	"errors"
	"github.com/andygrunwald/vdf"
	"io"
	"os"
	"path/filepath"
	"strconv"
)

// VerifyApp requires SteamCMD's manifest in the requested installation directory.
// The caller separately requires the process's success marker for this run.
func VerifyApp(dir, appID string) error {
	f, e := os.Open(filepath.Join(dir, "steamapps", "appmanifest_"+appID+".acf"))
	if e != nil {
		return errors.New("SteamCMD reported success but the requested directory has no app manifest")
	}
	defer f.Close()
	m, e := vdf.NewParser(io.LimitReader(f, 4<<20)).Parse()
	if e != nil {
		return errors.New("SteamCMD app manifest is invalid")
	}
	a, ok := m["AppState"].(map[string]interface{})
	if !ok || a["appid"] != appID {
		return errors.New("SteamCMD app manifest does not match requested app")
	}
	state, _ := a["StateFlags"].(string)
	flags, e := strconv.ParseUint(state, 10, 64)
	if e != nil || flags&4 == 0 {
		return errors.New("SteamCMD app manifest does not mark the app fully installed")
	}
	return nil
}
