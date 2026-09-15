//go:build !windows

package idle

import "syscall"

func terminatePID(pid int) {
	_ = syscall.Kill(pid, syscall.SIGTERM)
}
