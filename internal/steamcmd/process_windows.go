package steamcmd

import (
	"io"
	"os/exec"
	"time"
)

func configureProcess(c *exec.Cmd, in io.Reader) { c.WaitDelay = 2 * time.Second }
