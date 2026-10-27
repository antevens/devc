// Package xdg resolves the XDG base directories devc keeps its files in.
package xdg

import (
	"os"
	"path/filepath"
)

// ConfigHome returns $XDG_CONFIG_HOME, or ~/.config if it is not set.
func ConfigHome(home string) string {
	return dir("XDG_CONFIG_HOME", home, ".config")
}

// ConfigDir returns the directory holding the devc config.
func ConfigDir(home string) string {
	return filepath.Join(ConfigHome(home), "devc")
}

// StateDir returns the directory devc keeps its state in.
func StateDir(home string) string {
	return filepath.Join(dir("XDG_STATE_HOME", home, ".local", "state"), "devc")
}

// CacheDir returns the directory devc caches downloads in.
func CacheDir(home string) string {
	return filepath.Join(dir("XDG_CACHE_HOME", home, ".cache"), "devc")
}

func dir(env, home string, fallback ...string) string {
	if d := os.Getenv(env); d != "" {
		return d
	}
	return filepath.Join(append([]string{home}, fallback...)...)
}
