//go:build linux || darwin || dragonfly || freebsd || illumos || netbsd || openbsd

package forge

import (
	"os"
	"syscall"
)

// lockFile takes an exclusive lock on f, waiting until it is free. Closing f
// releases it.
func lockFile(f *os.File) error {
	for {
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != syscall.EINTR {
			return err
		}
	}
}
