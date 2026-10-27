package container

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// manager is a package manager and the packages providing commands.
type manager struct {
	name     string
	refresh  []string
	install  []string
	packages map[string]string
}

var (
	defaultPackages = map[string]string{"bash": "bash", "vim": "vim", "shellcheck": "shellcheck"}
	redHatPackages  = map[string]string{"bash": "bash", "vim": "vim-enhanced", "shellcheck": "ShellCheck"}
)

// managers are tried in order.
var managers = []manager{
	{"apt-get", []string{"apt-get", "update"}, []string{"apt-get", "install", "--yes", "--no-install-recommends"}, defaultPackages},
	{"apk", nil, []string{"apk", "add", "--no-cache"}, defaultPackages},
	{"dnf", nil, []string{"dnf", "install", "--assumeyes"}, redHatPackages},
	{"microdnf", nil, []string{"microdnf", "install", "--assumeyes"}, redHatPackages},
	{"yum", nil, []string{"yum", "install", "--assumeyes"}, redHatPackages},
	{"zypper", nil, []string{"zypper", "--non-interactive", "install"},
		map[string]string{"bash": "bash", "vim": "vim", "shellcheck": "ShellCheck"}},
	{"pacman", nil, []string{"pacman", "--sync", "--refresh", "--noconfirm"}, defaultPackages},
}

// Install installs the packages providing the commands missing from the
// remote user's login PATH with the container's package manager. It returns
// the commands it installed.
func (c *Container) Install(commands ...string) ([]string, error) {
	missing, err := c.missing(commands)
	if err != nil || len(missing) == 0 {
		return nil, err
	}
	m, err := c.manager()
	if err != nil {
		return nil, err
	}
	install := m.install
	for _, command := range missing {
		pkg, ok := m.packages[command]
		if !ok {
			return nil, fmt.Errorf("no %s package known for %s", m.name, command)
		}
		install = append(install, pkg)
	}
	if m.refresh != nil {
		if err := c.runRoot(m.refresh); err != nil {
			return nil, err
		}
	}
	if err := c.runRoot(install); err != nil {
		return nil, err
	}
	return missing, nil
}

// missing returns the commands that are not on the remote user's login PATH.
func (c *Container) missing(commands []string) ([]string, error) {
	script := `for command in "$@"; do command -v "${command}" >/dev/null || echo "${command}"; done`
	out, err := c.Output(c.User, append([]string{"sh", "-lc", script, "sh"}, commands...)...)
	if err != nil {
		return nil, fmt.Errorf("check commands in container: %w", err)
	}
	return strings.Fields(string(out)), nil
}

// manager returns the first of managers installed in the container.
func (c *Container) manager() (manager, error) {
	for _, m := range managers {
		if _, err := c.Output("root", "sh", "-c", `command -v "$1"`, "sh", m.name); err == nil {
			return m, nil
		}
	}
	return manager{}, errors.New("no supported package manager in container")
}

// runRoot runs a command as root in the container, without prompts.
func (c *Container) runRoot(args []string) error {
	cmdArgs := append([]string{"exec", "--user", "root", "--env", "DEBIAN_FRONTEND=noninteractive", c.ID}, args...)
	if out, err := exec.Command("docker", cmdArgs...).CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %w: %s", strings.Join(args, " "), err, lastLines(out, 5))
	}
	return nil
}

// lastLines returns the last n lines of out.
func lastLines(out []byte, n int) string {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
