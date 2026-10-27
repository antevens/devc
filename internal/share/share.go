// Package share copies the user's files from the host home directory into a
// container and keeps them in sync while a session runs. Copies go one way:
// changes made in the container are not written back.
package share

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/antevens/devc/internal/archive"
	"github.com/antevens/devc/internal/container"
)

// Tree is a directory in the home directory that is copied into the
// container and re-copied when it changes.
type Tree struct {
	Name string
	// Skip lists top-level entries that are not copied.
	Skip map[string]bool
}

// Trees are copied with symlinks dereferenced.
var Trees = []Tree{
	{
		// Per-machine session state is not copied.
		Name: ".claude",
		Skip: map[string]bool{
			"backups": true, "cache": true, "debug": true, "file-history": true,
			"history.jsonl": true, "ide": true, "logs": true, "paste-cache": true,
			"plans": true, "projects": true, "session-env": true, "sessions": true,
			"shell-snapshots": true, "statsig": true, "tasks": true, "telemetry": true,
			"todos": true,
		},
	},
	{Name: ".ai"},
}

// files are copied when a session starts.
var files = []string{".claude.json", ".gitconfig"}

// Sync copies the shared trees and files into the remote user's home
// directory. Problems with optional files are written to warnings.
func Sync(c *container.Container, home string, warnings io.Writer) error {
	a := archive.New(0, 0)
	if err := addTrees(a, home, Trees); err != nil {
		return err
	}
	for _, name := range files {
		if err := a.AddTree(name, filepath.Join(home, name), nil); err != nil {
			return err
		}
	}
	if err := addDockerConfig(a, home); err != nil {
		fmt.Fprintf(warnings, "devc: not sharing docker config: %v\n", err)
	}
	if config := microk8sConfig(); config != nil {
		a.AddFile(".kube/config", config, 0o600)
	}
	return extract(c, a)
}

// SyncTree copies one shared tree into the remote user's home directory.
func SyncTree(c *container.Container, home string, t Tree) error {
	a := archive.New(0, 0)
	if err := addTrees(a, home, []Tree{t}); err != nil {
		return err
	}
	return extract(c, a)
}

func addTrees(a *archive.Archive, home string, trees []Tree) error {
	for _, t := range trees {
		if err := a.AddTree(t.Name, filepath.Join(home, t.Name), t.Skip); err != nil {
			return err
		}
	}
	return nil
}

func extract(c *container.Container, a *archive.Archive) error {
	data, err := a.Bytes()
	if err != nil {
		return err
	}
	return c.ExtractHome(data)
}

// addDockerConfig adds the host's docker config without the credentials of
// registries containing "upload", leaving the container pull-only access.
func addDockerConfig(a *archive.Archive, home string) error {
	data, err := os.ReadFile(filepath.Join(home, ".docker", "config.json"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	filtered, err := filterDockerConfig(data)
	if err != nil {
		return err
	}
	a.AddFile(".docker/config.json", filtered, 0o444)
	return nil
}

// filterDockerConfig drops the registries whose name contains "upload",
// ignoring case, from a docker config.
func filterDockerConfig(data []byte) ([]byte, error) {
	var config map[string]any
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("parse docker config: %w", err)
	}
	for _, section := range []string{"auths", "credHelpers"} {
		entries, _ := config[section].(map[string]any)
		for registry := range entries {
			if strings.Contains(strings.ToLower(registry), "upload") {
				delete(entries, registry)
			}
		}
	}
	return json.MarshalIndent(config, "", "  ")
}

// microk8sConfig returns the kubeconfig of microk8s on the host, nil when
// microk8s is not installed or not running.
func microk8sConfig() []byte {
	if _, err := exec.LookPath("microk8s"); err != nil {
		return nil
	}
	config, err := exec.Command("microk8s", "config").Output()
	if err != nil {
		return nil
	}
	return config
}
