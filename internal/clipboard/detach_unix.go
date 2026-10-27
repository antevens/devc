//go:build unix

package clipboard

import (
	"os/exec"
	"syscall"
)

// detach starts cmd in a session of its own.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
