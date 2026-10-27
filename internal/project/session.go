package project

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/antevens/devc/internal/archive"
	"github.com/antevens/devc/internal/assets"
	"github.com/antevens/devc/internal/claude"
	"github.com/antevens/devc/internal/container"
	"github.com/antevens/devc/internal/forge"
	"github.com/antevens/devc/internal/share"
	"github.com/antevens/devc/internal/xdg"
)

// Session runs cmd in the devcontainer through the launcher, starting the
// container if needed. While cmd runs, changed shared files are copied into
// the container and tokens are renewed, logging errors to devc.log in the
// state directory.
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
	return attached(ctx, "devcontainer", args...)
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

// sessionEnv returns the session environment: the Remote Control session
// name prefix and the forge variables.
func sessionEnv(name string, tokens []*forge.Token) string {
	lines := []string{
		"CLAUDE_REMOTE_CONTROL_SESSION_NAME_PREFIX=" + shellQuote(name),
		"export CLAUDE_REMOTE_CONTROL_SESSION_NAME_PREFIX",
	}
	lines = append(lines, forge.Env(tokens)...)
	return strings.Join(lines, "\n") + "\n"
}

// shellQuote quotes s for a POSIX shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
