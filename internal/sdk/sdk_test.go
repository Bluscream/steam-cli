package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestResolveOverloadsAndGeneration(t *testing.T) {
	s := Schema{Interfaces: []Definition{{Class: "ITest", Accessors: []Accessor{{Kind: "user", Name: "SteamAPI_Test_v001"}}, Methods: []Method{{Name: "Read", Symbol: "SteamAPI_ITest_Read", Params: []Parameter{{Name: "id", Type: "uint64"}}}, {Name: "Read", Symbol: "SteamAPI_ITest_Read2"}}}}}
	if _, e := s.Resolve("ITest", "Read"); e == nil {
		t.Fatal("ambiguous overload accepted")
	}
	m, e := s.Resolve("itest", "SteamAPI_ITest_Read")
	if e != nil || len(m.Params) != 1 {
		t.Fatal(m, e)
	}
	b, e := Generate(s)
	if e != nil || !bytes.Contains(b, []byte("decltype(&SteamAPI_ITest_Read)")) {
		t.Fatal(e)
	}
	s.Interfaces[0].Methods[0].Symbol = "bad;code"
	if _, e = Generate(s); e == nil {
		t.Fatal("invalid source identifier accepted")
	}
}
func TestLoadRejectsForeignPlatform(t *testing.T) {
	d := t.TempDir()
	os.MkdirAll(filepath.Join(d, "sdk"), 0700)
	os.WriteFile(filepath.Join(d, "sdk/current.json"), []byte(`{"platform":"foreign/cpu"}`), 0600)
	if _, e := Load(d); e == nil {
		t.Fatal("foreign helper accepted")
	}
}

// Opt-in ABI integration: compile all methods from a local SDK, but load a fake
// shared library, so calls and callbacks never contact a live Steam account.
func TestNativeABI(t *testing.T) {
	dir := os.Getenv("STEAMCLI_TEST_SDK")
	if dir == "" {
		t.Skip("set STEAMCLI_TEST_SDK to a local SDK directory")
	}
	if runtime.GOOS != "linux" {
		t.Skip("fake shared library build currently exercises Linux ABI")
	}
	dir, _ = filepath.Abs(dir)
	tmp := t.TempDir()
	source := filepath.Join(tmp, "fake.cpp")
	lib := filepath.Join(tmp, "libfake.so")
	code := `
#include "steam/steam_api_flat.h"
#include "steam/steam_gameserver.h"
#include <cstdio>
#include <cstdlib>
#include <cstring>
static int event_state=0;
extern "C" {
ESteamAPIInitResult SteamAPI_InitFlat(SteamErrMsg*err){if(std::getenv("STEAMCLI_FAKE_FAIL")){std::strcpy(*err,"fixture failure");return k_ESteamAPIInitResult_FailedGeneric;}std::puts("diagnostic must not corrupt JSON");return k_ESteamAPIInitResult_OK;}
void SteamAPI_Shutdown(){}
void SteamAPI_ManualDispatch_Init(){}
HSteamPipe SteamAPI_GetHSteamPipe(){return 1;}
void SteamAPI_ManualDispatch_RunFrame(HSteamPipe){}
bool SteamAPI_ManualDispatch_GetNextCallback(HSteamPipe,CallbackMsg_t*m){static SteamAPICallCompleted_t c{};if(event_state)return false;c.m_hAsyncCall=18446744073709551614ULL;c.m_iCallback=NumberOfCurrentPlayers_t::k_iCallback;c.m_cubParam=sizeof(NumberOfCurrentPlayers_t);m->m_iCallback=SteamAPICallCompleted_t::k_iCallback;m->m_pubParam=(uint8*)&c;m->m_cubParam=sizeof(c);return true;}
void SteamAPI_ManualDispatch_FreeLastCallback(HSteamPipe){event_state++;}
bool SteamAPI_ManualDispatch_GetAPICallResult(HSteamPipe,SteamAPICall_t,void*p,int n,int,bool*failed){NumberOfCurrentPlayers_t c{};c.m_bSuccess=1;c.m_cPlayers=123;std::memcpy(p,&c,n);*failed=false;return true;}
ISteamUtils* SteamAPI_SteamUtils_v010(){return reinterpret_cast<ISteamUtils*>(1);}
AppId_t SteamAPI_ISteamUtils_GetAppID(ISteamUtils*p){return p==reinterpret_cast<ISteamUtils*>(5)?481:480;}
ESteamAPIInitResult SteamInternal_GameServer_Init_V2(uint32,uint16,uint16,EServerMode,const char*,const char*,SteamErrMsg*){return k_ESteamAPIInitResult_OK;}
ISteamUtils* SteamAPI_SteamGameServerUtils_v010(){return reinterpret_cast<ISteamUtils*>(5);}
HSteamPipe SteamGameServer_GetHSteamPipe(){return 2;}
void SteamGameServer_Shutdown(){}
const char* SteamAPI_ISteamUtils_GetIPCountry(ISteamUtils*){return "ZZ";}
ISteamUser* SteamAPI_SteamUser_v023(){return reinterpret_cast<ISteamUser*>(2);}
uint64_steamid SteamAPI_ISteamUser_GetSteamID(ISteamUser*){return 76561198000000001ULL;}
ISteamApps* SteamAPI_SteamApps_v009(){return reinterpret_cast<ISteamApps*>(3);}
bool SteamAPI_ISteamApps_GetCurrentBetaName(ISteamApps*,char*b,int n){if(n<5)return false;std::memcpy(b,"test",5);return true;}
ISteamInput* SteamAPI_SteamInput_v006(){return reinterpret_cast<ISteamInput*>(4);}
InputAnalogActionData_t SteamAPI_ISteamInput_GetAnalogActionData(ISteamInput*,InputHandle_t,InputAnalogActionHandle_t){InputAnalogActionData_t r{};r.eMode=k_EInputSourceMode_JoystickMove;r.x=0.5f;r.y=-0.25f;r.bActive=true;return r;}
}
`
	if e := os.WriteFile(source, []byte(code), 0600); e != nil {
		t.Fatal(e)
	}
	cmd := exec.Command("c++", "-std=c++17", "-shared", "-fPIC", "-I", filepath.Join(dir, "public"), source, "-o", lib)
	if b, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("fake runtime: %v\n%s", e, b)
	}
	cache := os.Getenv("STEAMCLI_TEST_SDK_CACHE")
	if cache == "" {
		cache = t.TempDir()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	var log bytes.Buffer
	build, e := Compile(ctx, dir, cache, "", lib, &log)
	if e != nil {
		t.Fatalf("helper: %v\n%s", e, log.String())
	}
	requests := []map[string]any{
		{"op": "init"},
		{"op": "call", "method": "SteamAPI_ISteamUtils_GetAppID", "args": []any{}},
		{"op": "call", "method": "SteamAPI_ISteamUtils_GetIPCountry", "args": []any{}},
		{"op": "call", "method": "SteamAPI_ISteamUser_GetSteamID", "args": []any{}},
		{"op": "buffer", "size": 16},
		{"op": "call", "method": "SteamAPI_ISteamApps_GetCurrentBetaName", "args": []any{map[string]string{"buffer": "b4"}, 16}},
		{"op": "read", "buffer": "b4"},
		{"op": "call", "method": "SteamAPI_ISteamInput_GetAnalogActionData", "args": []any{"1", "2"}},
		{"op": "poll"},
		{"op": "layout", "type": "InputAnalogActionData_t"},
	}
	// Three interface handles precede the first buffer, making its ID b4.
	var input, out, stderr bytes.Buffer
	enc := json.NewEncoder(&input)
	for i, r := range requests {
		r["id"] = i
		enc.Encode(r)
	}
	if e = Run(ctx, build, "480", &input, &out, &stderr); e != nil {
		t.Fatalf("run: %v\n%s\n%s", e, out.String(), stderr.String())
	}
	var replies []struct {
		OK     bool
		Result json.RawMessage
		Error  string
	}
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var reply struct {
			OK     bool
			Result json.RawMessage
			Error  string
		}
		if e = json.Unmarshal([]byte(line), &reply); e != nil {
			t.Fatal(e, out.String())
		}
		if !reply.OK {
			t.Fatal(reply.Error)
		}
		replies = append(replies, reply)
	}
	if len(replies) != len(requests) {
		t.Fatal("missing replies")
	}
	for idx, want := range map[int]string{1: `480`, 2: `"ZZ"`, 3: `"76561198000000001"`, 6: `7465737400`, 8: `18446744073709551614`} {
		if !bytes.Contains(replies[idx].Result, []byte(want)) {
			t.Fatalf("reply %d: %s", idx, replies[idx].Result)
		}
	}
	if !strings.Contains(stderr.String(), "diagnostic must not corrupt JSON") {
		t.Fatal("SDK stdout was not redirected")
	}
	// Invalid arguments must fail before a native call, with nonzero status.
	input.Reset()
	out.Reset()
	input.WriteString("{\"op\":\"init\"}\n{\"op\":\"call\",\"method\":\"SteamAPI_ISteamInput_GetAnalogActionData\",\"args\":[\"-1\",2]}\n")
	if e = Run(ctx, build, "480", &input, &out, &stderr); e == nil || !strings.Contains(out.String(), "unsigned") {
		t.Fatal("invalid uint64 accepted", out.String(), e)
	}
	input.Reset()
	out.Reset()
	input.WriteString("{\"op\":\"init\",\"mode\":\"gameserver\"}\n{\"op\":\"call\",\"method\":\"SteamAPI_ISteamUtils_GetAppID\",\"args\":[]}\n")
	if e = Run(ctx, build, "480", &input, &out, &stderr); e != nil || !strings.Contains(out.String(), "481") {
		t.Fatal("game-server accessor selection", out.String(), e)
	}
	idleDir := t.TempDir()
	pid, err := StartIdle(ctx, build, idleDir, 480)
	if err != nil || pid <= 0 {
		t.Fatal("idle init handshake", pid, err)
	}
	defer StopIdle(idleDir)
	stoppedPID, stopped, err := StopIdle(idleDir)
	if err != nil || !stopped || stoppedPID != pid {
		t.Fatal("idle control shutdown", err)
	}
	t.Setenv("STEAMCLI_FAKE_FAIL", "1")
	if _, err = StartIdle(ctx, build, idleDir, 480); err == nil || !strings.Contains(err.Error(), "fixture failure") {
		t.Fatal("idle falsely reported startup", err)
	}
	t.Logf("compiled %d methods; tested real native calls, pointer outputs, struct returns, callbacks, exact uint64 and diagnostics", build.Methods)
}

func TestIdleStateCannotDeleteOutsideControlDirectory(t *testing.T) {
	d := t.TempDir()
	dir := filepath.Join(d, "sdk")
	os.MkdirAll(dir, 0700)
	victim := filepath.Join(t.TempDir(), "idle-control-other")
	os.WriteFile(victim, []byte("keep"), 0600)
	raw, _ := json.Marshal(idleState{PID: 1, Control: victim})
	os.WriteFile(filepath.Join(dir, "idle.json"), raw, 0600)
	if _, _, e := StopIdle(d); e == nil {
		t.Fatal("accepted outside control file")
	}
	if _, e := os.Stat(victim); e != nil {
		t.Fatal("deleted unrelated file")
	}
}
