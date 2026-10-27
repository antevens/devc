package main

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// tree is a directory in the home directory that is copied into the
// container and kept in sync while a session runs.
type tree struct {
	name string
	// skip lists top-level entries that are not copied.
	skip map[string]bool
}

// sharedTrees are copied with symlinks dereferenced, so dotfiles managed as
// symlinks into another directory work in the container.
var sharedTrees = []tree{
	{
		// Per-machine session state is not copied: it would overwrite the
		// container's own sessions and make every sync slow.
		name: ".claude",
		skip: map[string]bool{
			"backups": true, "cache": true, "debug": true, "file-history": true,
			"history.jsonl": true, "ide": true, "logs": true, "paste-cache": true,
			"plans": true, "projects": true, "session-env": true, "sessions": true,
			"shell-snapshots": true, "statsig": true, "tasks": true, "telemetry": true,
			"todos": true,
		},
	},
	{name: ".ai"},
}

// sharedFiles are copied from the home directory when the session starts.
var sharedFiles = []string{".claude.json", ".gitconfig"}

// sync copies the shared files into the container.
func (c *container) sync(home string) error {
	if err := c.syncHome(home, sharedTrees, true); err != nil {
		return err
	}
	if err := c.installClaude(home); err != nil {
		fmt.Fprintf(os.Stderr, "devc: %v\n", err)
	}
	return nil
}

// syncHome copies trees, and with all set the other shared files, from the
// host home directory into the remote user's home directory.
func (c *container) syncHome(home string, trees []tree, all bool) error {
	if err := c.lookupUser(); err != nil {
		return err
	}
	a := newArchive(0, 0)
	for _, t := range trees {
		if err := a.addTree(t.name, filepath.Join(home, t.name), t.skip); err != nil {
			return err
		}
	}
	if all {
		for _, name := range sharedFiles {
			if err := a.addTree(name, filepath.Join(home, name), nil); err != nil {
				return err
			}
		}
		if err := addDockerConfig(a, home); err != nil {
			fmt.Fprintf(os.Stderr, "devc: not sharing docker config: %v\n", err)
		}
		if config, err := microk8sConfig(); err == nil && config != nil {
			a.addFile(".kube/config", config, 0o600)
		}
	}
	data, err := a.close()
	if err != nil {
		return err
	}
	return c.extract(c.user, c.home, data)
}

// addDockerConfig adds the host's docker config without the credentials of
// registries containing "upload", leaving the container pull-only access.
func addDockerConfig(a *archive, home string) error {
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
	a.addFile(".docker/config.json", filtered, 0o444)
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
func microk8sConfig() ([]byte, error) {
	if _, err := exec.LookPath("microk8s"); err != nil {
		return nil, nil
	}
	config, err := exec.Command("microk8s", "config").Output()
	if err != nil {
		return nil, nil
	}
	return config, nil
}

// archive builds a tar archive in memory.
type archive struct {
	buf      bytes.Buffer
	tw       *tar.Writer
	uid, gid int
	err      error
}

// newArchive returns an archive whose entries are owned by uid and gid. The
// owners only apply when the archive is extracted as root.
func newArchive(uid, gid int) *archive {
	a := &archive{uid: uid, gid: gid}
	a.tw = tar.NewWriter(&a.buf)
	return a
}

// addDir adds a directory entry.
func (a *archive) addDir(name string, mode fs.FileMode, modTime time.Time) {
	a.write(&tar.Header{Typeflag: tar.TypeDir, Name: name + "/", Mode: int64(mode.Perm()), ModTime: modTime}, nil)
}

// addFile adds a file with the given contents.
func (a *archive) addFile(name string, data []byte, mode fs.FileMode) {
	a.write(&tar.Header{Typeflag: tar.TypeReg, Name: name, Mode: int64(mode.Perm()), Size: int64(len(data)), ModTime: time.Now()}, data)
}

func (a *archive) write(h *tar.Header, data []byte) {
	if a.err != nil {
		return
	}
	h.Uid, h.Gid = a.uid, a.gid
	if a.err = a.tw.WriteHeader(h); a.err == nil && data != nil {
		_, a.err = a.tw.Write(data)
	}
}

// addTree adds the file or directory src as name, following symlinks. A
// missing src is ignored, as are unreadable files, broken symlinks and
// special files. skip lists entries of src that are left out.
func (a *archive) addTree(name, src string, skip map[string]bool) error {
	return a.addPath(name, src, skip, map[string]bool{})
}

// addPath adds src as name. ancestors holds the real paths of the
// directories being added, to stop at symlink loops.
func (a *archive) addPath(name, src string, skip, ancestors map[string]bool) error {
	info, err := os.Stat(src)
	if os.IsNotExist(err) || os.IsPermission(err) {
		return nil
	}
	if err != nil {
		return err
	}

	switch {
	case info.Mode().IsRegular():
		data, err := os.ReadFile(src)
		if os.IsPermission(err) {
			return nil
		}
		if err != nil {
			return err
		}
		a.write(&tar.Header{Typeflag: tar.TypeReg, Name: name, Mode: int64(info.Mode().Perm()), Size: int64(len(data)), ModTime: info.ModTime()}, data)
	case info.IsDir():
		real, err := filepath.EvalSymlinks(src)
		if err != nil {
			return err
		}
		if ancestors[real] {
			return nil
		}
		ancestors[real] = true
		defer delete(ancestors, real)

		entries, err := os.ReadDir(src)
		if os.IsPermission(err) {
			return nil
		}
		if err != nil {
			return err
		}
		a.addDir(name, info.Mode(), info.ModTime())
		for _, e := range entries {
			if skip[e.Name()] {
				continue
			}
			if err := a.addPath(path.Join(name, e.Name()), filepath.Join(src, e.Name()), nil, ancestors); err != nil {
				return err
			}
		}
	}
	return a.err
}

// close finishes the archive and returns its contents.
func (a *archive) close() ([]byte, error) {
	if a.err != nil {
		return nil, fmt.Errorf("build archive: %w", a.err)
	}
	if err := a.tw.Close(); err != nil {
		return nil, fmt.Errorf("build archive: %w", err)
	}
	return a.buf.Bytes(), nil
}
