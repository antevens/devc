// Command devc creates, starts and attaches to devcontainers for the current
// repository, sharing the user's shell history, git config and Claude Code
// settings with the container.
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const usage = `Usage: devc <command> [args...]
       devc --help | --version

Commands:
  up          Create and start the devcontainer
  shell [cmd] Open a login shell (or run cmd) in the devcontainer, starting it if needed
  claude      Run claude in the devcontainer, starting it if needed
  remote      Run claude remote-control in the devcontainer, so sessions in the
              project can be started from the Claude apps
  update      Recreate the devcontainer without image cache, applying config
              changes and installing the latest feature versions such as claude.
              A .devcontainer/devcontainer.json created by init is replaced
              with the shared config unless it has been modified
  rebuild     Recreate the devcontainer, reusing cached image layers
  stop        Stop the devcontainer
  rm          Remove the devcontainer
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

// commands lists the devc commands, used for bash completion.
var commands = []string{"up", "shell", "claude", "remote", "update", "rebuild", "stop", "rm", "init", "ls", "completion", "help"}

func main() {
	os.Exit(run(os.Args[1:]))
}

// run executes a devc command and returns the process exit code.
func run(args []string) int {
	command := ""
	if len(args) > 0 {
		command, args = args[0], args[1:]
	}

	switch command {
	case "", "-h", "--help", "help":
		fmt.Print(usage)
		return 0
	case "-V", "--version":
		fmt.Printf("devc %s\n", version)
		return 0
	case "completion":
		return report(completion(args))
	}

	p, err := newProject()
	if err != nil {
		return report(err)
	}

	switch command {
	case "up":
		return report(p.up(args...))
	case "shell":
		if len(args) == 0 {
			args = loginShell
		}
		return p.session(args)
	case "claude":
		return p.session(p.claudeCommand(args))
	case "remote":
		return p.session(p.remoteCommand(args))
	case "update":
		if err := p.refreshConfig(); err != nil {
			return report(err)
		}
		return report(p.up(append([]string{"--remove-existing-container", "--build-no-cache"}, args...)...))
	case "rebuild":
		return report(p.up(append([]string{"--remove-existing-container"}, args...)...))
	case "stop":
		return report(p.stop())
	case "rm":
		return report(p.remove())
	case "init":
		return report(p.initConfig())
	case "ls":
		return report(list())
	default:
		fmt.Fprintf(os.Stderr, "devc: unknown command: %s\n", command)
		fmt.Fprint(os.Stderr, usage)
		return 1
	}
}

// report prints err to stderr and returns the exit code for it. Failures of
// child processes keep their exit code, their output already explains them.
func report(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() > 0 {
		return exitErr.ExitCode()
	}
	fmt.Fprintf(os.Stderr, "devc: %v\n", err)
	return 1
}
