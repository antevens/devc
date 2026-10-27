//go:build !unix

package clipboard

import "os/exec"

// detach leaves cmd unchanged on systems without sessions.
func detach(*exec.Cmd) {}
