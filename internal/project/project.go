// Package project runs devcontainers for a workspace: it creates and starts
// them with the devcontainer CLI, provisions them with the user's files,
// Claude settings, forge tools and tokens, and runs sessions in them.
package project

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/antevens/devc/internal/claude"
	"github.com/antevens/devc/internal/config"
	"github.com/antevens/devc/internal/container"
	"github.com/antevens/devc/internal/dockershim"
	"github.com/antevens/devc/internal/forge"
	"github.com/antevens/devc/internal/share"
	"github.com/antevens/devc/internal/xdg"
)

const (
	// claudeFeature installs claude in containers whose config lacks it.
	claudeFeature = "ghcr.io/anthropics/devcontainer-features/claude-code:1"
	// nodeFeature is added with claudeFeature, which requires Node.js.
	nodeFeature = "ghcr.io/devcontainers/features/node:1"
)

// requiredCommands are installed in every devcontainer that lacks them.
var requiredCommands = []string{"bash", "vim", "shellcheck"}

// LoginShell starts bash as a login shell, or sh in images without bash.
var LoginShell = []string{"sh", "-c", "if command -v bash >/dev/null; then exec bash -l; else exec sh -l; fi"}

// Project is a workspace and its devcontainer.
type Project struct {
	// Workspace is the workspace folder.
	Workspace string
	// SharedConfig is used when the workspace has no devcontainer.json.
	SharedConfig string
	Settings     config.Settings
	// History shares bash history with the host: the host's history is
	// readable in the container's shells and their commands are appended to
	// it.
	History bool
	home    string
	forges  *forge.Client
}

// New returns the project for the workspace of the current directory: its
// git top-level or, outside a git repository, the nearest directory with a
// devcontainer config or devcontainer, else the current directory.
func New(home, sharedConfig string, s config.Settings) (*Project, error) {
	workspace, err := workspaceFolder()
	if err != nil {
		return nil, err
	}
	return &Project{
		Workspace:    workspace,
		SharedConfig: sharedConfig,
		Settings:     s,
		History:      true,
		home:         home,
		forges:       forge.NewClient(s, home, workspace),
	}, nil
}

func workspaceFolder() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err == nil {
		return strings.TrimSpace(string(out)), nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	devcontainers, err := container.List("", true)
	if err != nil {
		return "", err
	}
	folders := map[string]bool{}
	for _, d := range devcontainers {
		folders[d.Workspace] = true
	}
	return nearestWorkspace(cwd, folders, hasConfig), nil
}

// nearestWorkspace returns the closest of dir and its parents that has a
// devcontainer config or is in folders, else dir.
func nearestWorkspace(dir string, folders map[string]bool, hasConfig func(string) bool) string {
	for d := dir; ; d = filepath.Dir(d) {
		if folders[d] || hasConfig(d) {
			return d
		}
		if d == filepath.Dir(d) {
			return dir
		}
	}
}

// hasConfig reports whether dir has a devcontainer.json the devcontainer
// CLI finds by itself.
func hasConfig(dir string) bool {
	for _, name := range []string{".devcontainer/devcontainer.json", ".devcontainer.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return true
		}
	}
	return false
}

// Name returns the workspace folder's name.
func (p *Project) Name() string {
	return filepath.Base(p.Workspace)
}

// ConfigCopy returns the workspace copy of the shared config.
func (p *Project) ConfigCopy() config.Workspace {
	return config.Workspace{Dir: p.Workspace, Shared: p.SharedConfig, StateDir: xdg.StateDir(p.home)}
}

// hasOwnConfig reports whether the workspace has a devcontainer.json the
// devcontainer CLI finds by itself.
func (p *Project) hasOwnConfig() bool {
	return hasConfig(p.Workspace)
}

// configArgs selects the workspace and, without a config of its own, the
// shared config. lockfile adds --no-lockfile for the shared config.
func (p *Project) configArgs(lockfile bool) []string {
	args := []string{"--workspace-folder", p.Workspace}
	if !p.hasOwnConfig() {
		args = append(args, "--override-config", p.SharedConfig)
		if lockfile {
			args = append(args, "--no-lockfile")
		}
	}
	return args
}

// Up creates and starts the devcontainer with extra devcontainer up
// arguments, then provisions it.
func (p *Project) Up(ctx context.Context, extra ...string) error {
	_, _, err := p.up(ctx, extra...)
	return err
}

// up runs devcontainer up with the missing features and devc as the CLI's
// docker, which replaces bind mounts of the home directory's hidden entries
// with copies. It copies those into a new container before running the
// lifecycle commands, so they find the files where the mounts would have
// put them, then provisions the container.
func (p *Project) up(ctx context.Context, extra ...string) (*container.Container, []forge.Forge, error) {
	args := append([]string{"up"}, p.configArgs(true)...)

	cfg, err := p.readConfiguration(ctx)
	if err != nil {
		return nil, nil, err
	}
	var mounted []mount
	for _, m := range cfg.Mounts {
		if !dockershim.CopyInstead(m.Source, p.home, p.Workspace) {
			mounted = append(mounted, m)
		}
	}
	if err := checkMountSources(mounted); err != nil {
		return nil, nil, err
	}
	features, err := featuresFor(cfg.Features)
	if err != nil {
		return nil, nil, err
	}
	if features != nil {
		args = append(args, "--additional-features", string(features))
	}

	shimArgs, env, err := p.dockerShim()
	if err != nil {
		return nil, nil, fmt.Errorf("run devc as the devcontainer CLI's docker: %w", err)
	}
	args = append(append(args, shimArgs...), "--skip-post-create")
	cmd := exec.CommandContext(ctx, "devcontainer")
	cmd.Args = append(append([]string{"devcontainer"}, args...), extra...)
	cmd.Env = env
	if err := runAttached(cmd); err != nil {
		return nil, nil, err
	}
	c, err := container.Find(p.Workspace)
	if err != nil {
		return nil, nil, err
	}
	if c == nil {
		return nil, nil, fmt.Errorf("no running container for %s after devcontainer up", p.Workspace)
	}
	if err := p.copyMounts(c); err != nil {
		return nil, nil, err
	}
	user := append([]string{"run-user-commands"}, p.configArgs(false)...)
	if err := attached(ctx, "devcontainer", append(user, "--skip-post-attach")...); err != nil {
		return nil, nil, err
	}
	forges, err := p.provision(ctx, c)
	return c, forges, err
}

// configuration is the part of a devcontainer config devc reads.
type configuration struct {
	Features map[string]any `json:"features"`
	Mounts   []mount        `json:"mounts"`
}

// readConfiguration returns the workspace's devcontainer config with local
// variables substituted.
func (p *Project) readConfiguration(ctx context.Context) (*configuration, error) {
	args := append([]string{"read-configuration"}, p.configArgs(false)...)
	out, err := exec.CommandContext(ctx, "devcontainer", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("devcontainer read-configuration: %w", err)
	}
	var result struct {
		Configuration configuration `json:"configuration"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("parse devcontainer configuration: %w", err)
	}
	return &result.Configuration, nil
}

// featuresFor returns the claude feature, and the Node.js feature unless
// configured has one, as JSON. It returns nil when configured has a
// claude-code feature.
func featuresFor(configured map[string]any) ([]byte, error) {
	hasClaude, hasNode := false, false
	for id := range configured {
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

// List prints all devcontainers.
func List(ctx context.Context) error {
	return attached(ctx, "docker", "ps", "--all", "--filter", "label="+container.FolderLabel,
		"--format", `table {{.ID}}\t{{.Names}}\t{{.Status}}\t{{.Label "`+container.FolderLabel+`"}}`)
}

// Command returns the claude command line for args.
func (p *Project) Command(args []string) []string {
	return claude.Command(args, p.Name(), p.Settings.RemoteControl)
}

// RemoteCommand returns the claude remote-control command line for args.
func (p *Project) RemoteCommand(args []string) []string {
	return claude.RemoteCommand(args, p.Name())
}

// provision installs requiredCommands, copies the shared files and installs
// the Claude settings, forge tools, tokens and session files in the
// container. Failures other than
// copying the shared files and installing the session files are written to
// stderr. It returns the workspace's forges.
func (p *Project) provision(ctx context.Context, c *container.Container) ([]forge.Forge, error) {
	if err := p.copyMounts(c); err != nil {
		warn(err)
	}
	installed, err := c.Install(requiredCommands...)
	if err != nil {
		warn(fmt.Errorf("install %s: %w", strings.Join(requiredCommands, ", "), err))
	}
	if len(installed) > 0 {
		fmt.Fprintf(os.Stderr, "devc: installed %s in the container\n", strings.Join(installed, ", "))
	}
	if err := share.Sync(c, p.home, os.Stderr); err != nil {
		return nil, err
	}
	if err := claude.Install(c, p.home); err != nil {
		warn(err)
	}
	forges := forge.Detect(p.Workspace, p.Settings)
	if err := p.forges.InstallTools(ctx, c, forges, xdg.CacheDir(p.home), os.Stderr); err != nil {
		warn(err)
	}
	if err := p.forges.Install(ctx, c, forges); err != nil {
		warn(err)
	}
	return forges, installSession(c, p.Name())
}

// attached runs a command attached to the terminal.
func attached(ctx context.Context, name string, args ...string) error {
	return runAttached(exec.CommandContext(ctx, name, args...))
}

// runAttached runs cmd attached to the terminal.
func runAttached(cmd *exec.Cmd) error {
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return commandError(cmd.Args[0], cmd.Run())
}

// commandError returns err, naming the command when it is not in PATH.
func commandError(name string, err error) error {
	if errors.Is(err, exec.ErrNotFound) {
		return fmt.Errorf("%s not found in PATH", name)
	}
	return err
}

func warn(err error) {
	fmt.Fprintf(os.Stderr, "devc: %v\n", err)
}
