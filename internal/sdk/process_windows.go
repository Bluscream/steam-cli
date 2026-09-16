package sdk

import (
	"os/exec"
	"syscall"
)

func configureProcess(cmd *exec.Cmd) {}
func configureDetached(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000008 | 0x00000200, HideWindow: true}
}
