package project

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/term"

	"github.com/antevens/devc/internal/archive"
	"github.com/antevens/devc/internal/assets"
	"github.com/antevens/devc/internal/claude"
	"github.com/antevens/devc/internal/clipboard"
	"github.com/antevens/devc/internal/container"
	"github.com/antevens/devc/internal/forge"
	"github.com/antevens/devc/internal/share"
	"github.com/antevens/devc/internal/terminal"
	"github.com/antevens/devc/internal/xdg"
)

// Session runs cmd in the devcontainer through the launcher, starting the
// container if needed. While cmd runs, changed shared files are copied into
// the container, tokens are renewed and OSC 52 copies are written to the
// host clipboard, logging errors to devc.log in the state directory.
func (p *Project) Session(ctx context.Context, cmd []string) error {
	c, err := container.Find(p.Workspace)
	if err != nil {
		return err
	}
	var forges []forge.Forge
	if c == nil {
		c, forges, err = p.up(ctx)
	} else {
		forges, err = p.provision(ctx, c)
	}
	if err != nil {
		return err
	}

	logFile, err := openLog(p.home)
	if err != nil {
		return err
	}
	defer logFile.Close()
	log := slog.New(slog.NewTextHandler(logFile, nil))

	watcher, err := share.Watch(p.home, log, func(t share.Tree) {
		if err := share.SyncTree(c, p.home, t); err != nil {
			log.Warn("sync", "tree", t.Name, "err", err)
			return
		}
		if t.Name == ".claude" {
			if err := claude.Install(c, p.home); err != nil {
				log.Warn("install claude settings", "err", err)
			}
		}
	})
	if err != nil {
		warn(fmt.Errorf("not watching shared files for changes: %w", err))
	} else {
		defer watcher.Close()
	}

	renewCtx, stopRenew := context.WithCancel(ctx)
	defer stopRenew()
	go p.forges.Renew(renewCtx, c, forges, log)

	args := append([]string{"exec"}, p.configArgs(false)...)
	if c.HasMount(historyMount) {
		args = append(args, "--remote-env", "HISTFILE="+historyMount)
	}
	args = append(args, assets.LauncherPath)
	args = append(args, cmd...)

	// The command in the container handles interrupts.
	signal.Ignore(syscall.SIGINT, syscall.SIGQUIT)
	defer signal.Reset(syscall.SIGINT, syscall.SIGQUIT)
	return runSession(ctx, args, log)
}

// runSession runs devcontainer with args attached to the terminal. When
// stdin and stdout are terminals and the host has a clipboard tool, it runs
// on a pseudo-terminal and OSC 52 copies are written to the host clipboard
// and primary selection, logging errors to log.
func runSession(ctx context.Context, args []string, log *slog.Logger) error {
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return attached(ctx, "devcontainer", args...)
	}
	host, err := clipboard.FindHost()
	if err != nil {
		if !errors.Is(err, clipboard.ErrNoDisplay) {
			warn(fmt.Errorf("not copying to the clipboard: %w", err))
		}
		return attached(ctx, "devcontainer", args...)
	}
	relay := clipboard.NewRelay(host, log)
	defer relay.Close()
	filter := clipboard.NewFilter(os.Stdout, relay.Copy)
	err = terminal.Run(exec.CommandContext(ctx, "devcontainer", args...), os.Stdin, filter)
	if errors.Is(err, errors.ErrUnsupported) {
		return attached(ctx, "devcontainer", args...)
	}
	if flushErr := filter.Flush(); err == nil {
		err = flushErr
	}
	return commandError("devcontainer", err)
}

func openLog(home string) (*os.File, error) {
	dir := xdg.StateDir(home)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return os.OpenFile(filepath.Join(dir, "devc.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
}

// installSession writes the launcher and the session environment into the
// container.
func installSession(c *container.Container, name string, tokens []*forge.Token) error {
	a := archive.New(0, c.GID)
	a.AddDir(archive.Entry(assets.Dir), 0o755)
	a.AddFile(archive.Entry(assets.LauncherPath), assets.Launcher, 0o755)
	a.AddFile(archive.Entry(assets.EnvPath), []byte(sessionEnv(name, tokens)), 0o644)
	data, err := a.Bytes()
	if err != nil {
		return err
	}
	return c.ExtractRoot(data)
}

// scrollSpeed is the default number of lines claude scrolls per mouse wheel
// step in fullscreen mode.
const scrollSpeed = 12

// sessionEnv returns the session environment: the Remote Control session
// name prefix, the default claude scroll speed and the forge variables.
func sessionEnv(name string, tokens []*forge.Token) string {
	lines := []string{
		"CLAUDE_REMOTE_CONTROL_SESSION_NAME_PREFIX=" + shellQuote(name),
		"export CLAUDE_REMOTE_CONTROL_SESSION_NAME_PREFIX",
		fmt.Sprintf(`CLAUDE_CODE_SCROLL_SPEED="${CLAUDE_CODE_SCROLL_SPEED:-%d}"`, scrollSpeed),
		"export CLAUDE_CODE_SCROLL_SPEED",
	}
	lines = append(lines, forge.Env(tokens)...)
	return strings.Join(lines, "\n") + "\n"
}

// shellQuote quotes s for a POSIX shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
