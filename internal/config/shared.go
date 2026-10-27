package config

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/antevens/devc/internal/xdg"
)

// defaultShared is written to the shared config path when it does not exist.
//
//go:embed devcontainer.json
var defaultShared []byte

// ErrLocalChanges is returned by Refresh for a workspace config that has
// been edited since it was copied by Init.
var ErrLocalChanges = errors.New("has local changes, not replacing")

// Shared returns the path of the shared devcontainer config, from
// $DEVC_CONFIG or ~/.config/devcontainer/devcontainer.json. The latter is
// created when it does not exist, and created reports whether it was.
func Shared(home string) (path string, created bool, err error) {
	if path := os.Getenv("DEVC_CONFIG"); path != "" {
		if _, err := os.Stat(path); err != nil {
			return "", false, fmt.Errorf("DEVC_CONFIG: %w", err)
		}
		return path, false, nil
	}
	path = filepath.Join(xdg.ConfigHome(home), "devcontainer", "devcontainer.json")
	if _, err := os.Stat(path); err == nil {
		return path, false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", false, err
	}
	if err := os.WriteFile(path, defaultShared, 0o644); err != nil {
		return "", false, err
	}
	return path, true, nil
}

// Workspace manages the copy of the shared config in a workspace, made for
// per-repository customisation.
type Workspace struct {
	// Dir is the workspace folder.
	Dir string
	// Shared is the path of the shared config.
	Shared string
	// StateDir records the hash of each copy, to tell whether it has been
	// edited.
	StateDir string
}

// Path returns the path of the workspace's devcontainer.json.
func (w Workspace) Path() string {
	return filepath.Join(w.Dir, ".devcontainer", "devcontainer.json")
}

func (w Workspace) hashFile() string {
	return filepath.Join(w.StateDir, hash([]byte(w.Dir))+".sha256")
}

// Init copies the shared config into the workspace.
func (w Workspace) Init() error {
	if _, err := os.Stat(w.Path()); err == nil {
		return fmt.Errorf("%s already exists", w.Path())
	}
	return w.install()
}

// Refresh replaces a workspace config copied by Init with the current shared
// config, unless it has been edited since. It reports whether the config was
// replaced, and returns ErrLocalChanges for an edited config.
func (w Workspace) Refresh() (bool, error) {
	current, err := os.ReadFile(w.Path())
	if err != nil {
		return false, nil
	}
	recorded, err := os.ReadFile(w.hashFile())
	if err != nil {
		return false, nil
	}
	if hash(current) != strings.TrimSpace(string(recorded)) {
		return false, fmt.Errorf("%s %w", w.Path(), ErrLocalChanges)
	}
	shared, err := os.ReadFile(w.Shared)
	if err != nil {
		return false, err
	}
	if bytes.Equal(shared, current) {
		return false, nil
	}
	return true, w.install()
}

// install copies the shared config into the workspace and records its hash.
func (w Workspace) install() error {
	data, err := os.ReadFile(w.Shared)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(w.Path()), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(w.Path(), data, 0o644); err != nil {
		return err
	}
	if err := os.MkdirAll(w.StateDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(w.hashFile(), []byte(hash(data)+"\n"), 0o644)
}

func hash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
