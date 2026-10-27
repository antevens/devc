package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// container is a running devcontainer.
type container struct {
	id     string
	user   string
	mounts []string
	// home, uid and gid of the remote user, looked up on first use.
	home     string
	uid, gid int
}

// inspect returns the container with the given ID.
func inspect(id string) (*container, error) {
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
	if err := json.Unmarshal(out, &result); err != nil || len(result) == 0 {
		return nil, fmt.Errorf("parse docker inspect %s: %v", id, err)
	}
	c := &container{id: id}
	c.user, err = remoteUser(result[0].Config.Labels["devcontainer.metadata"], result[0].Config.User)
	if err != nil {
		return nil, err
	}
	for _, m := range result[0].Mounts {
		c.mounts = append(c.mounts, m.Destination)
	}
	return c, nil
}

// remoteUser returns the user devcontainer exec runs commands as: the last
// remoteUser or containerUser in the container's devcontainer metadata, which
// the CLI merges from the image, features and devcontainer.json in order.
func remoteUser(metadata, imageUser string) (string, error) {
	var entries []struct {
		RemoteUser    string `json:"remoteUser"`
		ContainerUser string `json:"containerUser"`
	}
	if metadata != "" {
		if err := json.Unmarshal([]byte(metadata), &entries); err != nil {
			// A single entry is stored as an object rather than an array.
			entries = entries[:0:0]
			var entry struct {
				RemoteUser    string `json:"remoteUser"`
				ContainerUser string `json:"containerUser"`
			}
			if err := json.Unmarshal([]byte(metadata), &entry); err != nil {
				return "", fmt.Errorf("parse devcontainer metadata: %w", err)
			}
			entries = append(entries, entry)
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

// hasMount reports whether the container has a mount at target.
func (c *container) hasMount(target string) bool {
	for _, m := range c.mounts {
		if m == target {
			return true
		}
	}
	return false
}

// lookupUser fills in the remote user's home directory, uid and gid.
func (c *container) lookupUser() error {
	if c.home != "" {
		return nil
	}
	out, err := exec.Command("docker", "exec", "--user", c.user, c.id,
		"sh", "-c", `printf '%s\n' "$HOME"; id -u; id -g`).Output()
	if err != nil {
		return fmt.Errorf("look up home of %s in container: %w", c.user, err)
	}
	fields := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(fields) != 3 || !strings.HasPrefix(fields[0], "/") {
		return fmt.Errorf("unexpected user details from container: %q", out)
	}
	c.home = fields[0]
	if c.uid, err = strconv.Atoi(fields[1]); err != nil {
		return fmt.Errorf("parse uid %q: %w", fields[1], err)
	}
	if c.gid, err = strconv.Atoi(fields[2]); err != nil {
		return fmt.Errorf("parse gid %q: %w", fields[2], err)
	}
	return nil
}

// extract unpacks a tar archive into dir in the container as user. Extracting
// as the remote user gives the files the right owner, as root the archive's
// owners are kept.
func (c *container) extract(user, dir string, archive []byte) error {
	cmd := exec.Command("docker", "exec", "--interactive", "--user", user, c.id,
		"tar", "-x", "-f", "-", "-C", dir)
	cmd.Stdin = bytes.NewReader(archive)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("extract files to %s in container: %w: %s", dir, err, bytes.TrimSpace(out))
	}
	return nil
}
