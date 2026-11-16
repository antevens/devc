package project

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"

	"github.com/antevens/devc/internal/archive"
	"github.com/antevens/devc/internal/assets"
	"github.com/antevens/devc/internal/container"
	"github.com/antevens/devc/internal/dockershim"
	"github.com/antevens/devc/internal/share"
	"github.com/antevens/devc/internal/sysbox"
)

// dockerShim returns the devcontainer CLI arguments and environment that
// make devc the CLI's docker, which replaces bind mounts of the home
// directory's hidden entries with copies and, with Sysbox set and the
// Docker daemon having the sysbox runtime, creates the container with it.
func (p *Project) dockerShim() ([]string, []string, error) {
	docker, err := exec.LookPath("docker")
	if err != nil {
		return nil, nil, err
	}
	if docker, err = filepath.Abs(docker); err != nil {
		return nil, nil, err
	}
	devc, err := os.Executable()
	if err != nil {
		return nil, nil, err
	}
	env := append(os.Environ(), dockershim.Env+"="+docker, dockershim.WorkspaceEnv+"="+p.Workspace)
	if p.Sysbox {
		ok, err := sysbox.Available()
		if err != nil {
			warn(fmt.Errorf("not using sysbox: %w", err))
		} else if ok {
			env = append(env, dockershim.RuntimeEnv+"="+sysbox.Runtime)
		}
	}
	return []string{"--docker-path", devc}, env, nil
}

// copyMounts copies the sources of the bind mounts the docker shim removed
// from the container's creation into the container, once per container.
// Sources missing on the host are skipped, as are the files Sync copies with
// its own rules.
func (p *Project) copyMounts(c *container.Container) error {
	mounts, err := dockershim.ParseLabel(c.Labels[dockershim.Label])
	if err != nil {
		return fmt.Errorf("parse label %s: %w", dockershim.Label, err)
	}
	if len(mounts) == 0 {
		return nil
	}
	if _, err := c.Output("root", "test", "-e", assets.MountsCopiedPath); err == nil {
		return nil
	}
	for _, m := range mounts {
		if share.Copies(m.Source, m.Target, p.home, c.Home) {
			continue
		}
		if _, err := os.Stat(m.Source); errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintf(os.Stderr, "devc: not copying %s: it does not exist on the host\n", m.Source)
			continue
		}
		if err := copyTree(c, m); err != nil {
			return fmt.Errorf("copy %s to %s: %w", m.Source, m.Target, err)
		}
		fmt.Fprintf(os.Stderr, "devc: copied %s to %s instead of mounting it\n", m.Source, m.Target)
	}
	a := archive.New(0, 0)
	a.AddDir(archive.Entry(assets.Dir), 0o755)
	a.AddFile(archive.Entry(assets.MountsCopiedPath), nil, 0o644)
	data, err := a.Bytes()
	if err != nil {
		return err
	}
	return c.ExtractRoot(data)
}

// copyTree streams the host file or directory m.Source to m.Target in the
// container, owned by the remote user. Directories between the remote
// user's home directory and the target are created for the user too.
func copyTree(c *container.Container, m dockershim.Mount) error {
	if c.Overlaps(m.Target) {
		return fmt.Errorf("%s overlaps a mount in the container", m.Target)
	}
	pr, pw := io.Pipe()
	defer pr.Close()
	go func() {
		a := archive.NewStream(pw, c.UID, c.GID)
		for _, dir := range parentsWithin(c.Home, m.Target) {
			a.AddDir(archive.Entry(dir), 0o755)
		}
		err := a.AddTree(archive.Entry(m.Target), m.Source, nil)
		if err == nil {
			err = a.Close()
		}
		pw.CloseWithError(err)
	}()
	return c.ExtractRootFrom(pr)
}

// parentsWithin returns the directories strictly between dir and target,
// outermost first, when target is inside dir.
func parentsWithin(dir, target string) []string {
	dir = path.Clean(dir)
	var parents []string
	for p := path.Dir(path.Clean(target)); p != dir && p != "/" && p != "."; p = path.Dir(p) {
		parents = append([]string{p}, parents...)
	}
	if len(parents) > 0 && path.Dir(parents[0]) != dir {
		return nil
	}
	return parents
}
