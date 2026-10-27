// Package container finds devcontainers and copies files into them, using
// the docker CLI.
package container

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// FolderLabel is set by the devcontainer CLI on every container it creates.
const FolderLabel = "devcontainer.local_folder"

// Container is a running devcontainer and the user devcontainer exec runs
// commands as.
type Container struct {
	ID     string
	User   string
	Home   string
	UID    int
	GID    int
	mounts []string
}

// FindID returns the ID of the workspace's running container, or of any of
// its containers when all is set, empty if there is none.
func FindID(workspace string, all bool) (string, error) {
	args := []string{"ps", "--quiet", "--filter", "label=" + FolderLabel + "=" + workspace}
	if all {
		args = append(args, "--all")
	}
	out, err := exec.Command("docker", args...).Output()
	if err != nil {
		return "", fmt.Errorf("docker ps: %w", err)
	}
	ids := strings.Fields(string(out))
	if len(ids) == 0 {
		return "", nil
	}
	return ids[0], nil
}

// Find returns the workspace's running container, nil if there is none.
func Find(workspace string) (*Container, error) {
	id, err := FindID(workspace, false)
	if err != nil || id == "" {
		return nil, err
	}
	return Inspect(id)
}

// Inspect returns the running container with the given ID.
func Inspect(id string) (*Container, error) {
	out, err := exec.Command("docker", "inspect", id).Output()
	if err != nil {
		return nil, fmt.Errorf("docker inspect %s: %w", id, err)
	}
	var result []struct {
		Config struct {
			User   string
			Labels map[string]string
		}
		Mounts []struct {
			Destination string
		}
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("parse docker inspect %s: %w", id, err)
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("docker inspect %s: no such container", id)
	}
	c := &Container{ID: id}
	if c.User, err = remoteUser(result[0].Config.Labels["devcontainer.metadata"], result[0].Config.User); err != nil {
		return nil, err
	}
	for _, m := range result[0].Mounts {
		c.mounts = append(c.mounts, m.Destination)
	}
	if err := c.lookupUser(); err != nil {
		return nil, err
	}
	return c, nil
}

// metadata is the part of an entry of the devcontainer.metadata label naming
// users.
type metadata struct {
	RemoteUser    string `json:"remoteUser"`
	ContainerUser string `json:"containerUser"`
}

// remoteUser returns the user devcontainer exec runs commands as: the last
// remoteUser in the devcontainer metadata label, else the last
// containerUser, else the image user, else root.
func remoteUser(label, imageUser string) (string, error) {
	var entries []metadata
	if label != "" {
		if err := json.Unmarshal([]byte(label), &entries); err != nil {
			// A single entry is stored as an object rather than an array.
			var entry metadata
			if err := json.Unmarshal([]byte(label), &entry); err != nil {
				return "", fmt.Errorf("parse devcontainer metadata: %w", err)
			}
			entries = []metadata{entry}
		}
	}
	remote, containerUser := "", imageUser
	for _, e := range entries {
		if e.RemoteUser != "" {
			remote = e.RemoteUser
		}
		if e.ContainerUser != "" {
			containerUser = e.ContainerUser
		}
	}
	switch {
	case remote != "":
		return remote, nil
	case containerUser != "":
		return containerUser, nil
	default:
		return "root", nil
	}
}

// lookupUser fills in the remote user's home directory, uid and gid.
func (c *Container) lookupUser() error {
	out, err := c.Output(c.User, "sh", "-c", `printf '%s\n' "$HOME"; id -u; id -g`)
	if err != nil {
		return fmt.Errorf("look up home of %s in container: %w", c.User, err)
	}
	fields := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(fields) != 3 || !strings.HasPrefix(fields[0], "/") {
		return fmt.Errorf("unexpected user details from container: %q", out)
	}
	c.Home = fields[0]
	if c.UID, err = strconv.Atoi(fields[1]); err != nil {
		return fmt.Errorf("parse uid %q: %w", fields[1], err)
	}
	if c.GID, err = strconv.Atoi(fields[2]); err != nil {
		return fmt.Errorf("parse gid %q: %w", fields[2], err)
	}
	return nil
}

// HasMount reports whether the container has a mount at target.
func (c *Container) HasMount(target string) bool {
	for _, m := range c.mounts {
		if m == target {
			return true
		}
	}
	return false
}

// Output runs a command in the container as user and returns its output.
func (c *Container) Output(user string, args ...string) ([]byte, error) {
	return exec.Command("docker", append([]string{"exec", "--user", user, c.ID}, args...)...).Output()
}

// HasCommand reports whether name is on the remote user's login PATH.
func (c *Container) HasCommand(name string) bool {
	_, err := c.Output(c.User, "sh", "-lc", `command -v "$1"`, "sh", name)
	return err == nil
}

// Arch returns the container's architecture as Go names it.
func (c *Container) Arch() (string, error) {
	out, err := c.Output("root", "uname", "-m")
	if err != nil {
		return "", fmt.Errorf("uname in container: %w", err)
	}
	switch machine := strings.TrimSpace(string(out)); machine {
	case "x86_64":
		return "amd64", nil
	case "aarch64", "arm64":
		return "arm64", nil
	default:
		return "", fmt.Errorf("unsupported container architecture %s", machine)
	}
}

// ExtractHome unpacks a tar archive into the remote user's home directory,
// as that user so the files are owned by it.
func (c *Container) ExtractHome(archive []byte) error {
	return c.extract(c.User, c.Home, archive)
}

// ExtractRoot unpacks a tar archive at the root directory as root, keeping
// the owners recorded in the archive.
func (c *Container) ExtractRoot(archive []byte) error {
	return c.extract("root", "/", archive)
}

func (c *Container) extract(user, dir string, archive []byte) error {
	cmd := exec.Command("docker", "exec", "--interactive", "--user", user, c.ID,
		"tar", "-x", "-f", "-", "-C", dir)
	cmd.Stdin = bytes.NewReader(archive)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("extract files to %s in container: %w: %s", dir, err, bytes.TrimSpace(out))
	}
	return nil
}
