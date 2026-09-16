package sdk

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gofrs/flock"
)

type idleState struct {
	PID     int    `json:"pid"`
	Control string `json:"control"`
}

// StartIdle starts an independent native helper and requires an initialization
// acknowledgement before reporting success. A private control file owns its
// lifetime; StopIdle never sends signals to an unverified, possibly reused PID.
func StartIdle(ctx context.Context, b Build, dataDir string, appID uint32) (int, error) {
	if appID == 0 {
		return 0, errors.New("AppID must be positive")
	}
	dir := filepath.Join(dataDir, "sdk")
	if e := os.MkdirAll(dir, 0700); e != nil {
		return 0, e
	}
	lock := flock.New(filepath.Join(dir, "idle.lock"))
	ok, e := lock.TryLockContext(ctx, 100*time.Millisecond)
	if e != nil {
		return 0, e
	}
	if !ok {
		return 0, errors.New("could not acquire idle lock")
	}
	defer lock.Unlock()
	if _, _, e = stopIdle(dir); e != nil {
		return 0, e
	}
	control, e := os.CreateTemp(dir, "idle-control-")
	if e != nil {
		return 0, e
	}
	controlPath := control.Name()
	keep := false
	defer func() {
		if !keep {
			os.Remove(controlPath)
		}
	}()
	nonce := make([]byte, 24)
	if _, e = rand.Read(nonce); e != nil {
		control.Close()
		return 0, e
	}
	token := hex.EncodeToString(nonce)
	if _, e = control.WriteString(token + "\n"); e != nil {
		control.Close()
		return 0, e
	}
	if e = control.Close(); e != nil {
		return 0, e
	}
	command := exec.Command(b.Helper, b.Library, "--idle", controlPath, token)
	configureDetached(command)
	command.Env = environment(strconv.FormatUint(uint64(appID), 10))
	null, e := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if e != nil {
		return 0, e
	}
	defer null.Close()
	command.Stderr = null
	output, e := command.StdoutPipe()
	if e != nil {
		return 0, e
	}
	defer output.Close()
	if e = command.Start(); e != nil {
		return 0, e
	}
	success := false
	defer func() {
		if !success {
			command.Process.Kill()
			command.Wait()
		}
	}()
	type response struct {
		line string
		err  error
	}
	ready := make(chan response, 1)
	go func() {
		scanner := bufio.NewScanner(output)
		scanner.Buffer(make([]byte, 4096), 1<<20)
		if scanner.Scan() {
			ready <- response{line: scanner.Text()}
		} else {
			err := scanner.Err()
			if err == nil {
				err = errors.New("native helper exited before initialization")
			}
			ready <- response{err: err}
		}
	}()
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-timer.C:
		return 0, errors.New("native Steam initialization timed out")
	case reply := <-ready:
		if reply.err != nil {
			return 0, reply.err
		}
		var r struct {
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		}
		if e = json.Unmarshal([]byte(reply.line), &r); e != nil {
			return 0, e
		}
		if !r.OK {
			return 0, fmt.Errorf("native initialization: %s", r.Error)
		}
	}
	state := idleState{PID: command.Process.Pid, Control: controlPath}
	raw, _ := json.Marshal(state)
	if e = atomicWrite(filepath.Join(dir, "idle.json"), raw); e != nil {
		return 0, e
	}
	keep = true
	success = true
	command.Process.Release()
	return state.PID, nil
}
func StopIdle(dataDir string) (int, bool, error) {
	dir := filepath.Join(dataDir, "sdk")
	if _, e := os.Stat(dir); errors.Is(e, os.ErrNotExist) {
		return 0, false, nil
	}
	lock := flock.New(filepath.Join(dir, "idle.lock"))
	ok, e := lock.TryLock()
	if e != nil {
		return 0, false, e
	}
	if !ok {
		return 0, false, errors.New("native idle state is busy")
	}
	defer lock.Unlock()
	return stopIdle(dir)
}
func stopIdle(dir string) (int, bool, error) {
	p := filepath.Join(dir, "idle.json")
	raw, e := os.ReadFile(p)
	if errors.Is(e, os.ErrNotExist) {
		return 0, false, nil
	}
	if e != nil {
		return 0, false, e
	}
	var state idleState
	if e = json.Unmarshal(raw, &state); e != nil {
		return 0, false, e
	}
	parent, _ := filepath.Abs(filepath.Dir(state.Control))
	expected, _ := filepath.Abs(dir)
	if parent != expected || !strings.HasPrefix(filepath.Base(state.Control), "idle-control-") {
		return 0, false, errors.New("invalid native idle control path")
	}
	if e = os.Remove(state.Control); e != nil && !errors.Is(e, os.ErrNotExist) {
		return 0, false, e
	}
	if e = os.Remove(p); e != nil {
		return 0, false, e
	}
	return state.PID, true, nil
}
