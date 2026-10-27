package container

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// ComposeProjectLabel is set by Docker Compose on the containers and
// networks of a project.
const ComposeProjectLabel = "com.docker.compose.project"

// ErrNotDevcontainer is returned by Lookup for containers without
// FolderLabel.
var ErrNotDevcontainer = errors.New("not a devcontainer")

// Summary describes a devcontainer.
type Summary struct {
	ID        string
	Name      string
	Workspace string
	// Project is the Docker Compose project, empty for other containers.
	Project string
	Running bool
}

// inspectFormat and psFormat are the docker container inspect and docker ps
// formats parsed by parseSummary.
const inspectFormat = `{{.Id}}	{{.Name}}	{{.State.Status}}	{{index .Config.Labels "` + FolderLabel + `"}}	{{index .Config.Labels "` + ComposeProjectLabel + `"}}`

const psFormat = `{{.ID}}	{{.Names}}	{{.State}}	{{.Label "` + FolderLabel + `"}}	{{.Label "` + ComposeProjectLabel + `"}}`

// List returns the devcontainers of workspace, or of every workspace when
// it is empty. all includes stopped containers.
func List(workspace string, all bool) ([]Summary, error) {
	filter := "label=" + FolderLabel
	if workspace != "" {
		filter += "=" + workspace
	}
	return ps(filter, all)
}

// ListProject returns the containers of a Docker Compose project. all
// includes stopped containers.
func ListProject(project string, all bool) ([]Summary, error) {
	return ps("label="+ComposeProjectLabel+"="+project, all)
}

func ps(filter string, all bool) ([]Summary, error) {
	args := []string{"ps", "--no-trunc", "--filter", filter, "--format", psFormat}
	if all {
		args = append(args, "--all")
	}
	out, err := exec.Command("docker", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("docker ps: %w", err)
	}
	return parseSummaries(string(out))
}

// parseSummaries parses docker output with one line in psFormat per
// container.
func parseSummaries(out string) ([]Summary, error) {
	var list []Summary
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		s, err := parseSummary(line)
		if err != nil {
			return nil, err
		}
		list = append(list, s)
	}
	return list, nil
}

// Lookup returns the devcontainer with the given name or full or short ID.
// It returns ErrNotDevcontainer for other containers.
func Lookup(ref string) (Summary, error) {
	out, err := exec.Command("docker", "container", "inspect", "--format", inspectFormat, ref).Output()
	if err != nil {
		return Summary{}, fmt.Errorf("no such container: %s", ref)
	}
	s, err := parseSummary(strings.TrimRight(string(out), "\n"))
	if err != nil {
		return Summary{}, err
	}
	if s.Workspace == "" {
		return Summary{}, fmt.Errorf("%s: %w", ref, ErrNotDevcontainer)
	}
	return s, nil
}

// ShortID returns the ID as docker ps shows it.
func (s Summary) ShortID() string {
	if len(s.ID) > 12 {
		return s.ID[:12]
	}
	return s.ID
}

// parseSummary parses a line in inspectFormat or psFormat.
func parseSummary(line string) (Summary, error) {
	fields := strings.Split(line, "\t")
	if len(fields) != 5 {
		return Summary{}, fmt.Errorf("unexpected docker output: %q", line)
	}
	for i, f := range fields {
		if f == "<no value>" {
			fields[i] = ""
		}
	}
	return Summary{
		ID:        fields[0],
		Name:      strings.TrimPrefix(fields[1], "/"),
		Running:   fields[2] == "running",
		Workspace: fields[3],
		Project:   fields[4],
	}, nil
}

// ProjectNetworks returns the names of the networks of a Docker Compose
// project.
func ProjectNetworks(project string) ([]string, error) {
	out, err := exec.Command("docker", "network", "ls", "--filter", "label="+ComposeProjectLabel+"="+project, "--format", "{{.Name}}").Output()
	if err != nil {
		return nil, fmt.Errorf("docker network ls: %w", err)
	}
	return strings.Fields(string(out)), nil
}
