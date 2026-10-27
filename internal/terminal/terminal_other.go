//go:build !linux

package terminal

import (
	"errors"
	"io"
	"os"
	"os/exec"
)

// Run returns errors.ErrUnsupported: pseudo-terminals are only opened on
// Linux.
func Run(*exec.Cmd, *os.File, io.Writer) error {
	return errors.ErrUnsupported
}
