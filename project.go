package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
)

const (
	// folderLabel is set by the devcontainer CLI on every container it creates.
	folderLabel = "devcontainer.local_folder"
	// historyMount is where the host's bash history is bind mounted. A fixed
	// path keeps the mount independent of the remote user's home directory.
	historyMount = "/mnt/devc/bash_history"
	// claudeFeature installs claude in containers whose config lacks it.
	claudeFeature = "ghcr.io/anthropics/devcontainer-features/claude-code:1"
	// nodeFeature is added with claudeFeature, whose own fallback install of
	// Node.js fails on recent images.
	nodeFeature = "ghcr.io/devcontainers/features/node:1"
)

// loginShell starts bash as a login shell, or sh in images without bash.
var loginShell = []string{"sh", "-c", "if command -v bash >/dev/null; then exec bash -l; else exec sh -l; fi"}

// project is the workspace devc operates on.
type project struct {
	workspace     string
	defaultConfig string
	home          string
	settings      settings
}

// newProject returns the project for the current directory.
func newProject() (*project, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	workspace, err := workspaceFolder()
	if err != nil {
		return nil, err
	}
	defaultConfig, err := ensureDefaultConfig(home)
	if err != nil {
		return nil, err
	}
	s, err := loadSettings(home)
	if err != nil {
		return nil, err
	}
	return &project{workspace: workspace, defaultConfig: defaultConfig, home: home, settings: s}, nil
}

// name returns the workspace's name, used to name Remote Control sessions.
func (p *project) name() string {
	return filepath.Base(p.workspace)
}

// workspaceFolder returns the git top-level of the current directory, or the
// current directory outside a git repository.
func workspaceFolder() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err == nil {
		return strings.TrimSpace(string(out)), nil
	}
	return os.Getwd()
}

// hasOwnConfig reports whether the workspace has a devcontainer.json the
// devcontainer CLI picks up by itself.
func (p *project) hasOwnConfig() bool {
	for _, name := range []string{".devcontainer/devcontainer.json", ".devcontainer.json"} {
		if _, err := os.Stat(filepath.Join(p.workspace, name)); err == nil {
			return true
		}
	}
	return false
}

// configArgs selects the workspace and, when it has none of its own, the
// shared config. Lockfiles are disabled for the shared config as there is no
// .devcontainer directory to write them to.
func (p *project) configArgs(lockfile bool) []string {
	args := []string{"--workspace-folder", p.workspace}
	if !p.hasOwnConfig() {
		args = append(args, "--override-config", p.defaultConfig)
		if lockfile {
			args = append(args, "--no-lockfile")
		}
	}
	return args
}

// up creates and starts the devcontainer, then provisions it.
// The repository's config is used unchanged: shared state is added with
// command line options, which only apply when the container is created.
func (p *project) up(extra ...string) error {
	args := append([]string{"up"}, p.configArgs(true)...)

	history := filepath.Join(p.home, ".bash_history")
	if err := touch(history); err != nil {
		return err
	}
	args = append(args, "--mount", fmt.Sprintf("type=bind,source=%s,target=%s", history, historyMount))

	features, err := p.missingFeatures()
	if err != nil {
		return err
	}
	if len(features) > 0 {
		args = append(args, "--additional-features", string(features))
	}

	if err := devcontainer(append(args, extra...)...); err != nil {
		return err
	}
	c, err := p.container(false)
	if err != nil {
		return err
	}
	if c == nil {
		return fmt.Errorf("no running container for %s after devcontainer up", p.workspace)
	}
	return p.provision(c)
}

// missingFeatures returns the features to add for claude, as JSON, empty if
// the config already installs it. Features the config has are not added, so
// their options are kept.
func (p *project) missingFeatures() ([]byte, error) {
	args := append([]string{"read-configuration"}, p.configArgs(false)...)
	out, err := exec.Command("devcontainer", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("devcontainer read-configuration: %w", err)
	}
	var result struct {
		Configuration struct {
			Features map[string]any `json:"features"`
		} `json:"configuration"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("parse devcontainer configuration: %w", err)
	}
	hasClaude, hasNode := false, false
	for id := range result.Configuration.Features {
		hasClaude = hasClaude || strings.Contains(id, "/claude-code")
		hasNode = hasNode || strings.Contains(id, "/node")
	}
	if hasClaude {
		return nil, nil
	}
	features := map[string]any{claudeFeature: map[string]any{}}
	if !hasNode {
		features[nodeFeature] = map[string]any{}
	}
	return json.Marshal(features)
}

// session runs cmd in the devcontainer, starting it if needed, while keeping
// the shared files in sync. It returns the exit code of cmd.
func (p *project) session(cmd []string) int {
	c, err := p.container(false)
	if err != nil {
		return report(err)
	}
	if c == nil {
		if err := p.up(); err != nil {
			return report(err)
		}
		if c, err = p.container(false); err != nil || c == nil {
			return report(err)
		}
	} else if err := p.provision(c); err != nil {
		return report(err)
	}

	stop, err := watch(c, p.home)
	if err != nil {
		fmt.Fprintf(os.Stderr, "devc: not watching shared files for changes: %v\n", err)
	}
	if stop != nil {
		defer stop()
	}
	renewing := make(chan struct{})
	defer close(renewing)
	go p.renewTokens(c, renewing)

	args := append([]string{"exec"}, p.configArgs(false)...)
	if c.hasMount(historyMount) {
		args = append(args, "--remote-env", "HISTFILE="+historyMount)
	}
	args = append(args, launcherPath)
	args = append(args, cmd...)

	// The terminal delivers interrupts to the whole foreground process group,
	// leave handling them to the command in the container.
	signal.Ignore(syscall.SIGINT, syscall.SIGQUIT)
	defer signal.Reset(syscall.SIGINT, syscall.SIGQUIT)
	return report(devcontainer(args...))
}

// stop stops the devcontainer if it is running.
func (p *project) stop() error {
	c, err := p.container(false)
	if err != nil || c == nil {
		return err
	}
	return docker("stop", c.id)
}

// remove removes the devcontainer if it exists and revokes its
// per-repository tokens.
func (p *project) remove() error {
	p.revokeTokens()
	c, err := p.container(true)
	if err != nil || c == nil {
		return err
	}
	return docker("rm", "--force", c.id)
}

// list prints all devcontainers.
func list() error {
	return docker("ps", "--all", "--filter", "label="+folderLabel,
		"--format", `table {{.ID}}\t{{.Status}}\t{{.Label "`+folderLabel+`"}}`)
}

// container returns the workspace's running container, or any container when
// all is set, nil if there is none.
func (p *project) container(all bool) (*container, error) {
	args := []string{"ps", "--quiet", "--filter", "label=" + folderLabel + "=" + p.workspace}
	if all {
		args = append(args, "--all")
	}
	out, err := exec.Command("docker", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("docker ps: %w", err)
	}
	ids := strings.Fields(string(out))
	if len(ids) == 0 {
		return nil, nil
	}
	return inspect(ids[0])
}

// devcontainer runs the devcontainer CLI attached to the terminal.
func devcontainer(args ...string) error {
	return attached("devcontainer", args...)
}

// docker runs the docker CLI attached to the terminal.
func docker(args ...string) error {
	return attached("docker", args...)
}

func attached(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := cmd.Run()
	if errors.Is(err, exec.ErrNotFound) {
		return fmt.Errorf("%s not found in PATH", name)
	}
	return err
}

// touch creates path if it does not exist, so a bind mount of it is a file
// rather than a directory created by docker.
func touch(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDONLY, 0o600)
	if err != nil {
		return err
	}
	return f.Close()
}
