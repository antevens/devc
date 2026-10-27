package project

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/antevens/devc/internal/config"
	"github.com/antevens/devc/internal/container"
	"github.com/antevens/devc/internal/forge"
)

// Devcontainers returns the workspace's devcontainers. all includes stopped
// containers.
func (p *Project) Devcontainers(all bool) ([]container.Summary, error) {
	return container.List(p.Workspace, all)
}

// Stop stops the devcontainers and the other containers of their Docker
// Compose projects. Stopped containers are reported to out.
func Stop(ctx context.Context, targets []container.Summary, out io.Writer) error {
	all, err := withProjects(targets, false)
	if err != nil {
		return err
	}
	return each(ctx, out, "Stopped", all, "stop")
}

// Remove removes the devcontainers, the other containers and networks of
// their Docker Compose projects, then revokes the per-repository tokens of
// their workspaces. Removed containers and networks are reported to out.
func Remove(ctx context.Context, home string, s config.Settings, targets []container.Summary, out io.Writer) error {
	all, err := withProjects(targets, true)
	if err != nil {
		return err
	}
	if err := each(ctx, out, "Removed", all, "rm", "--force"); err != nil {
		return err
	}

	var errs []error
	done := map[string]bool{}
	for _, t := range targets {
		if t.Project != "" && !done["project:"+t.Project] {
			done["project:"+t.Project] = true
			errs = append(errs, removeNetworks(ctx, out, t.Project))
		}
		if !done["workspace:"+t.Workspace] {
			done["workspace:"+t.Workspace] = true
			errs = append(errs, forge.NewClient(s, home, t.Workspace).RevokeAll(ctx))
		}
	}
	return errors.Join(errs...)
}

// withProjects returns the targets and the other containers of their Docker
// Compose projects. all includes stopped containers.
func withProjects(targets []container.Summary, all bool) ([]container.Summary, error) {
	var list []container.Summary
	seen := map[string]bool{}
	add := func(s container.Summary) {
		if !seen[s.ID] {
			seen[s.ID] = true
			list = append(list, s)
		}
	}
	for _, t := range targets {
		add(t)
		if t.Project == "" {
			continue
		}
		project, err := container.ListProject(t.Project, all)
		if err != nil {
			return nil, err
		}
		for _, s := range project {
			add(s)
		}
	}
	return list, nil
}

// each runs docker with args and the ID of each container, and reports each
// container to out prefixed by verb.
func each(ctx context.Context, out io.Writer, verb string, containers []container.Summary, args ...string) error {
	if len(containers) == 0 {
		return nil
	}
	for _, c := range containers {
		args = append(args, c.ID)
	}
	if err := run(ctx, args...); err != nil {
		return err
	}
	for _, c := range containers {
		fmt.Fprintf(out, "%s %s (%s)\n", verb, c.Name, c.ShortID())
	}
	return nil
}

// removeNetworks removes the networks of a Docker Compose project.
func removeNetworks(ctx context.Context, out io.Writer, project string) error {
	networks, err := container.ProjectNetworks(project)
	if err != nil || len(networks) == 0 {
		return err
	}
	if err := run(ctx, append([]string{"network", "rm"}, networks...)...); err != nil {
		return err
	}
	for _, n := range networks {
		fmt.Fprintf(out, "Removed network %s\n", n)
	}
	return nil
}

// run runs docker, returning its output in the error when it fails.
func run(ctx context.Context, args ...string) error {
	out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker %s: %w: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return nil
}
