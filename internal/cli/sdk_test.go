package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSDKMetadataAndExplicitAppID(t *testing.T) {
	cleanEnv(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "public", "steam")
	os.MkdirAll(p, 0700)
	os.WriteFile(filepath.Join(p, "steam_api.json"), []byte(`{"interfaces":[{"classname":"ITest","methods":[{"methodname":"GetValue","methodname_flat":"SteamAPI_ITest_GetValue","returntype":"uint64","params":[]}]}]}`), 0600)
	out, e := execute(t, "--offline", "sdk", "--sdk-dir", dir, "methods", "getvalue")
	if e != nil || !strings.Contains(out, "SteamAPI_ITest_GetValue") {
		t.Fatal(out, e)
	}
	if _, e = execute(t, "sdk", "--sdk-dir", dir, "call", "ITest", "GetValue"); e == nil || !strings.Contains(e.Error(), "--appid") {
		t.Fatal(e)
	}
	if _, e = execute(t, "--offline", "sdk", "--appid", "480", "call", "ITest", "GetValue"); e == nil || !strings.Contains(e.Error(), "--offline") {
		t.Fatal(e)
	}
}
