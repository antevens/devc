package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/antevens/devc/internal/config"
	"github.com/antevens/devc/internal/container"
	"github.com/antevens/devc/internal/project"
)

// errNotConfirmed is returned when the user declines the removal.
var errNotConfirmed = errors.New("not removed")

// remove removes the devcontainers named in args or, without names, the
// workspace's devcontainers after confirmation on in.
func remove(ctx context.Context, args []string, in *os.File) error {
	yes := false
	var refs []string
	for _, a := range args {
		switch {
		case a == "-y" || a == "--yes":
			yes = true
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("rm: unknown option %s", a)
		default:
			refs = append(refs, a)
		}
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	s, err := config.Load(home)
	if err != nil {
		return err
	}

	if len(refs) > 0 {
		var targets []container.Summary
		for _, ref := range refs {
			t, err := container.Lookup(ref)
			if err != nil {
				return err
			}
			targets = append(targets, t)
		}
		return project.Remove(ctx, home, s, targets, os.Stdout)
	}

	p, err := newProject()
	if err != nil {
		return err
	}
	targets, err := p.Devcontainers(true)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		fmt.Fprintf(os.Stderr, "devc: no devcontainer for %s\n", p.Workspace)
		return nil
	}
	if !yes {
		if !isTerminal(in) {
			return errors.New("rm: confirmation needs a terminal, pass -y or the devcontainer name or ID")
		}
		ok, err := confirm(in, os.Stderr, targets, p.Workspace)
		if err != nil {
			return err
		}
		if !ok {
			return errNotConfirmed
		}
	}
	return project.Remove(ctx, home, s, targets, os.Stdout)
}

// confirm asks on out whether to remove the targets and reads the answer
// from in. Only y and yes confirm.
func confirm(in io.Reader, out io.Writer, targets []container.Summary, workspace string) (bool, error) {
	for _, t := range targets {
		fmt.Fprintf(out, "Devcontainer %s (%s) for %s\n", t.Name, t.ShortID(), workspace)
	}
	fmt.Fprint(out, "Remove? [y/N] ")
	answer, err := bufio.NewReader(in).ReadString('\n')
	if errors.Is(err, io.EOF) {
		fmt.Fprintln(out)
	} else if err != nil {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

// isTerminal reports whether f is a terminal.
func isTerminal(f *os.File) bool {
	return term.IsTerminal(int(f.Fd()))
}
