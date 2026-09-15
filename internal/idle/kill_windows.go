//go:build windows

package idle

import "os"

func terminatePID(pid int) {
	p, err := os.FindProcess(pid)
	if err == nil && p != nil {
		_ = p.Kill()
	}
}
