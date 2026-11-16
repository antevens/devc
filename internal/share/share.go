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
	"path"
	"path/filepath"
	"strings"

	"github.com/antevens/devc/internal/archive"
	"github.com/antevens/devc/internal/assets"
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

// Copies reports whether Sync copies the host path source to the container
// path target: both are the same entry of the host and container home
// directories, under a top-level entry Sync copies with its own rules. A
// mount of ~/.docker is one: Sync shares only the filtered docker config.
func Copies(source, target, home, containerHome string) bool {
	hostRel, err := filepath.Rel(home, source)
	if err != nil || !filepath.IsLocal(hostRel) {
		return false
	}
	containerRel, err := filepath.Rel(containerHome, target)
	if err != nil || !filepath.IsLocal(containerRel) || filepath.ToSlash(hostRel) != filepath.ToSlash(containerRel) {
		return false
	}
	first, _, _ := strings.Cut(filepath.ToSlash(hostRel), "/")
	if first == ".docker" {
		return true
	}
	for _, t := range Trees {
		if first == t.Name {
			return true
		}
	}
	for _, name := range files {
		if first == name {
			return true
		}
	}
	return false
}

// kubeFragment is where the host cluster's kubeconfig is copied in the
// container, relative to the remote user's home directory, before it is
// merged into ~/.kube/config.
const kubeFragment = ".local/state/devc/host.kubeconfig"

// Sync copies the shared trees and files into the remote user's home
// directory and merges the kubeconfig of microk8s on the host into
// ~/.kube/config. Entries the container mounts are left out, as copying them
// would write into the mount. Problems with optional files are written to
// warnings.
func Sync(c *container.Container, home string, warnings io.Writer) error {
	a := archive.New(0, 0)
	for _, t := range Trees {
		if mounted(c, t.Name, warnings) {
			continue
		}
		if err := a.AddTree(t.Name, filepath.Join(home, t.Name), t.Skip); err != nil {
			return err
		}
	}
	for _, name := range files {
		if mounted(c, name, warnings) {
			continue
		}
		if err := a.AddTree(name, filepath.Join(home, name), nil); err != nil {
			return err
		}
	}
	if !mounted(c, ".docker", warnings) {
		if err := addDockerConfig(a, home); err != nil {
			fmt.Fprintf(warnings, "devc: not sharing docker config: %v\n", err)
		}
	}
	kubeconfig := microk8sConfig()
	if kubeconfig != nil && (mounted(c, ".kube", warnings) || mounted(c, kubeFragment, warnings)) {
		kubeconfig = nil
	}
	if kubeconfig != nil {
		a.AddFile(kubeFragment, kubeconfig, 0o600)
	}
	if err := extract(c, a); err != nil {
		return err
	}
	if kubeconfig != nil {
		if err := mergeKubeconfig(c); err != nil {
			fmt.Fprintf(warnings, "devc: not merging the microk8s kubeconfig: %v\n", err)
		}
	}
	return nil
}

// SyncTree copies one shared tree into the remote user's home directory,
// unless the container mounts it.
func SyncTree(c *container.Container, home string, t Tree) error {
	if c.Overlaps(path.Join(c.Home, t.Name)) {
		return nil
	}
	a := archive.New(0, 0)
	if err := a.AddTree(t.Name, filepath.Join(home, t.Name), t.Skip); err != nil {
		return err
	}
	return extract(c, a)
}

// mounted reports whether the home directory entry name overlaps a mount of
// the container, and warns when it does.
func mounted(c *container.Container, name string, warnings io.Writer) bool {
	target := path.Join(c.Home, name)
	if !c.Overlaps(target) {
		return false
	}
	fmt.Fprintf(warnings, "devc: not copying ~/%s: %s overlaps a mount in the container\n", name, target)
	return true
}

// mergeKubeconfig merges the copied host kubeconfig into ~/.kube/config in
// the container, keeping its current context.
func mergeKubeconfig(c *container.Container) error {
	out, err := c.Run(c.User, "bash", "-c", string(assets.MergeKubeconfig), "merge-kubeconfig", path.Join(c.Home, kubeFragment))
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
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
