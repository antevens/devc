//go:build !(linux || darwin || dragonfly || freebsd || illumos || netbsd || openbsd)

package forge

import "os"

// lockFile does nothing on systems without flock.
func lockFile(*os.File) error {
	return nil
}
