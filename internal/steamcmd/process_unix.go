//go:build !windows

package steamcmd

import (
	"errors"
	"golang.org/x/term"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func configureProcess(c *exec.Cmd, in io.Reader) {
	c.WaitDelay = 2 * time.Second
	// An interactive child must remain in the foreground terminal group so
	// Valve can prompt for passwords and Steam Guard. Batch processes get a
	// separate group, allowing cancellation to stop launcher AND descendants.
	if f, ok := in.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		return
	}
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error {
		e := syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
		if errors.Is(e, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return e
	}
}
