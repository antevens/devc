package main

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// defaultConfig is written to the shared config path when it does not exist.
//
//go:embed devcontainer.json
var defaultConfig []byte

// ensureDefaultConfig returns the path of the shared config used by
// repositories without their own, creating it if needed.
func ensureDefaultConfig(home string) (string, error) {
	path := os.Getenv("DEVC_CONFIG")
	if path == "" {
		path = filepath.Join(home, ".config", "devcontainer", "devcontainer.json")
	} else if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("DEVC_CONFIG: %w", err)
	}
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, defaultConfig, 0o644); err != nil {
		return "", err
	}
	fmt.Fprintf(os.Stderr, "devc: created the shared config %s\n", path)
	return path, nil
}

// repoConfig returns the path of the workspace config created by init.
func (p *project) repoConfig() string {
	return filepath.Join(p.workspace, ".devcontainer", "devcontainer.json")
}

// stateFile returns the file recording the hash of the workspace config
// created by init.
func (p *project) stateFile() string {
	sum := sha256.Sum256([]byte(p.workspace))
	return filepath.Join(stateDir(p.home), hex.EncodeToString(sum[:])+".sha256")
}

// stateDir returns the directory devc keeps its state in.
func stateDir(home string) string {
	dir := os.Getenv("XDG_STATE_HOME")
	if dir == "" {
		dir = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(dir, "devc")
}

// initConfig copies the shared config into the workspace.
func (p *project) initConfig() error {
	if _, err := os.Stat(p.repoConfig()); err == nil {
		return fmt.Errorf("%s already exists", p.repoConfig())
	}
	if err := p.installConfig(); err != nil {
		return err
	}
	fmt.Printf("Created %s\n", p.repoConfig())
	return nil
}

// installConfig copies the shared config into the workspace and records its
// hash.
func (p *project) installConfig() error {
	data, err := os.ReadFile(p.defaultConfig)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p.repoConfig()), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(p.repoConfig(), data, 0o644); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p.stateFile()), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p.stateFile(), []byte(hash(data)+"\n"), 0o644)
}

// refreshConfig replaces a workspace config created by init with the current
// shared config, unless it has been modified since.
func (p *project) refreshConfig() error {
	current, err := os.ReadFile(p.repoConfig())
	if err != nil {
		return nil
	}
	recorded, err := os.ReadFile(p.stateFile())
	if err != nil {
		return nil
	}
	if hash(current) != strings.TrimSpace(string(recorded)) {
		fmt.Fprintf(os.Stderr, "devc: %s has local changes, not replacing\n", p.repoConfig())
		return nil
	}
	shared, err := os.ReadFile(p.defaultConfig)
	if err != nil {
		return err
	}
	if bytes.Equal(shared, current) {
		return nil
	}
	if err := p.installConfig(); err != nil {
		return err
	}
	fmt.Printf("Updated %s from %s\n", p.repoConfig(), p.defaultConfig)
	return nil
}

func hash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
