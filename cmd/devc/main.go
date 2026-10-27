// Command devc creates, starts and attaches to devcontainers for the current
// repository and runs a shell or Claude Code in them.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime/debug"

	"github.com/antevens/devc/internal/config"
	"github.com/antevens/devc/internal/project"
)

// version is set with -ldflags "-X main.version=...".
var version string

const usage = `Usage: devc <command> [args...]
       devc --help | --version

Commands:
  up, start   Create and start the devcontainer
  shell [cmd] Open a login shell (or run cmd) in the devcontainer, starting it if needed
  claude      Run claude in the devcontainer, starting it if needed
  remote      Run claude remote-control in the devcontainer, so sessions in the
              project can be started from the Claude apps
  update      Recreate the devcontainer without image cache, applying config
              changes and installing the latest feature versions such as claude.
              A .devcontainer/devcontainer.json created by init is replaced
              with the shared config unless it has been modified
  rebuild     Recreate the devcontainer, reusing cached image layers
  stop, down  Stop the devcontainer
  rm [-y] [name|id...]
              Remove the given devcontainers, or the workspace's after
              confirmation (-y skips it), and revoke their repository tokens.
              Docker Compose projects are removed with their networks
  init        Copy the shared config into .devcontainer/ for per-repo customisation
  ls          List all devcontainers
  completion  Print the bash completion script, load with:
              source <(devc completion bash)

Options:
  -h, --help     Show this help
  -V, --version  Show the devc version

The workspace is the git top-level of the current directory, or the current
directory outside a git repository. Repositories without their own
devcontainer.json use the shared config, set DEVC_CONFIG to use another one.

Your shell history, git config, Claude Code settings, credentials and
~/.ai preferences are shared with the container. When claude is started
by devc your Claude settings and instructions take precedence over the
project's.

GitHub and GitLab repositories get gh or glab installed and, when configured
in ~/.config/devc/config.json, a token limited to the repository.
`

// commands lists the devc commands.
var commands = []string{"up", "start", "shell", "claude", "remote", "update", "rebuild", "stop", "down", "rm", "init", "ls", "completion", "help"}

func main() {
	os.Exit(run(context.Background(), os.Args[1:]))
}

// run executes a devc command and returns the process exit code.
func run(ctx context.Context, args []string) int {
	command := ""
	if len(args) > 0 {
		command, args = args[0], args[1:]
	}

	switch command {
	case "", "-h", "--help", "help":
		fmt.Print(usage)
		return 0
	case "-V", "--version":
		fmt.Printf("devc %s\n", versionString())
		return 0
	case "completion":
		return report(completion(os.Stdout, args))
	case "ls":
		return report(project.List(ctx))
	case "rm":
		return report(remove(ctx, args, os.Stdin))
	}

	p, err := newProject()
	if err != nil {
		return report(err)
	}

	switch command {
	case "up", "start":
		return report(p.Up(ctx, args...))
	case "shell":
		if len(args) == 0 {
			args = project.LoginShell
		}
		return report(p.Session(ctx, args))
	case "claude":
		return report(p.Session(ctx, p.Command(args)))
	case "remote":
		return report(p.Session(ctx, p.RemoteCommand(args)))
	case "update":
		replaced, err := p.ConfigCopy().Refresh()
		switch {
		case errors.Is(err, config.ErrLocalChanges):
			fmt.Fprintf(os.Stderr, "devc: %v\n", err)
		case err != nil:
			return report(err)
		case replaced:
			fmt.Printf("Updated %s from %s\n", p.ConfigCopy().Path(), p.SharedConfig)
		}
		return report(p.Up(ctx, append([]string{"--remove-existing-container", "--build-no-cache"}, args...)...))
	case "rebuild":
		return report(p.Up(ctx, append([]string{"--remove-existing-container"}, args...)...))
	case "stop", "down":
		targets, err := p.Devcontainers(false)
		if err != nil {
			return report(err)
		}
		if len(targets) == 0 {
			fmt.Fprintf(os.Stderr, "devc: no running devcontainer for %s\n", p.Workspace)
			return 0
		}
		return report(project.Stop(ctx, targets, os.Stdout))
	case "init":
		if err := p.ConfigCopy().Init(); err != nil {
			return report(err)
		}
		fmt.Printf("Created %s\n", p.ConfigCopy().Path())
		return 0
	default:
		fmt.Fprintf(os.Stderr, "devc: unknown command: %s\n", command)
		fmt.Fprint(os.Stderr, usage)
		return 1
	}
}

// newProject returns the project of the current directory.
func newProject() (*project.Project, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	shared, created, err := config.Shared(home)
	if err != nil {
		return nil, err
	}
	if created {
		fmt.Fprintf(os.Stderr, "devc: created the shared config %s\n", shared)
	}
	s, err := config.Load(home)
	if err != nil {
		return nil, err
	}
	return project.New(home, shared, s)
}

// versionString returns the version set at build time, or the module
// version for go install builds.
func versionString() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

// report prints err to stderr and returns its exit code. Failed child
// processes keep their exit code without a message.
func report(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() > 0 {
		return exitErr.ExitCode()
	}
	if errors.Is(err, errNotConfirmed) {
		return 1
	}
	fmt.Fprintf(os.Stderr, "devc: %v\n", err)
	return 1
}
