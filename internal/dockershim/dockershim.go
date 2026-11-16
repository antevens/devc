// Package dockershim rewrites the docker command that creates a
// devcontainer. The devcontainer CLI has no option for this, so devc passes
// itself as the CLI's docker executable: run with Env set to the real docker,
// devc rewrites `docker run` and `docker create` and runs the real docker
// with every other command unchanged.
//
// Bind mounts whose source is a hidden entry of the host home directory, or
// inside one, such as ~/.claude or ~/.m2/repository, are removed and
// recorded in the container's Label. devc copies the sources that exist into
// the container instead, so the container gets the files without access to
// the host's. The workspace folder stays mounted.
package dockershim

import (
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const (
	// Env holds the path of the real docker executable when devc runs as
	// the devcontainer CLI's docker.
	Env = "DEVC_DOCKER"
	// WorkspaceEnv holds the workspace folder, whose mounts are kept.
	WorkspaceEnv = "DEVC_WORKSPACE"
	// Label is the container label listing the bind mounts removed from its
	// creation, as JSON.
	Label = "devc.copied-mounts"
)

// Mount is a bind mount removed from a container's creation.
type Mount struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

// Exec runs the real docker with args rewritten by Rewrite, without the
// shim's environment variables. It returns only when docker cannot be
// started.
func Exec(docker string, args []string) error {
	home, _ := os.UserHomeDir()
	argv := append([]string{docker}, Rewrite(args, home, os.Getenv(WorkspaceEnv))...)
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if name != Env && name != WorkspaceEnv {
			env = append(env, kv)
		}
	}
	return syscall.Exec(docker, argv, env)
}

// ParseLabel returns the mounts recorded in a container's Label value.
func ParseLabel(value string) ([]Mount, error) {
	if value == "" {
		return nil, nil
	}
	var mounts []Mount
	if err := json.Unmarshal([]byte(value), &mounts); err != nil {
		return nil, err
	}
	return mounts, nil
}

// CopyInstead reports whether a bind mount of source is replaced by a copy:
// source is a hidden entry of home or inside one, and not in workspace.
func CopyInstead(source, home, workspace string) bool {
	if home == "" || !filepath.IsAbs(source) {
		return false
	}
	source = filepath.Clean(source)
	if workspace != "" && within(filepath.Clean(workspace), source) {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(home), source)
	if err != nil || rel == "." || !filepath.IsLocal(rel) {
		return false
	}
	first, _, _ := strings.Cut(rel, string(filepath.Separator))
	return strings.HasPrefix(first, ".")
}

// within reports whether path is dir or inside it.
func within(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && filepath.IsLocal(rel)
}

// Rewrite returns docker arguments with the bind mounts CopyInstead selects
// removed from `run` and `create` (also as `container run` and `container
// create`), and listed in Label. Other commands are returned unchanged. Only
// the options before the image are rewritten; the container's command is
// left alone.
func Rewrite(args []string, home, workspace string) []string {
	start := 0
	if len(args) > 0 && args[0] == "container" {
		start = 1
	}
	if len(args) <= start || (args[start] != "run" && args[start] != "create") {
		return args
	}
	start++
	end, values := parseOptions(args, start)

	var copied []Mount
	kept := make([]string, 0, len(args))
	for i := start; i < end; i++ {
		m, width, ok := mountAt(args, i, values)
		if ok && CopyInstead(m.Source, home, workspace) {
			copied = append(copied, m)
			i += width - 1
			continue
		}
		kept = append(kept, args[i])
	}
	if len(copied) == 0 {
		return args
	}
	label, err := json.Marshal(copied)
	if err != nil {
		return args
	}
	out := make([]string, 0, len(args)+2)
	out = append(out, args[:start]...)
	out = append(out, "--label", Label+"="+string(label))
	out = append(out, kept...)
	return append(out, args[end:]...)
}

// mountAt returns the bind mount given by the option at args[i], and the
// number of arguments the option takes.
func mountAt(args []string, i int, values map[int]bool) (Mount, int, bool) {
	a := args[i]
	if values[i] {
		return Mount{}, 0, false
	}
	switch {
	case a == "--mount" && i+1 < len(args):
		m, ok := bindMount(args[i+1])
		return m, 2, ok
	case strings.HasPrefix(a, "--mount="):
		m, ok := bindMount(strings.TrimPrefix(a, "--mount="))
		return m, 1, ok
	case (a == "-v" || a == "--volume") && i+1 < len(args):
		m, ok := volumeBind(args[i+1])
		return m, 2, ok
	case strings.HasPrefix(a, "--volume="):
		m, ok := volumeBind(strings.TrimPrefix(a, "--volume="))
		return m, 1, ok
	}
	return Mount{}, 0, false
}

// bindMount returns the source and target of a --mount specification of
// type bind: comma-separated key=value fields, CSV quoted.
func bindMount(spec string) (Mount, bool) {
	fields, err := csv.NewReader(strings.NewReader(spec)).Read()
	if err != nil {
		return Mount{}, false
	}
	kind := "volume"
	var m Mount
	for _, field := range fields {
		key, value, _ := strings.Cut(field, "=")
		switch strings.ToLower(key) {
		case "type":
			kind = value
		case "source", "src":
			m.Source = value
		case "target", "dst", "destination":
			m.Target = value
		}
	}
	return m, kind == "bind" && m.Source != "" && m.Target != ""
}

// volumeBind returns the source and target of a -v specification binding a
// host path: source:target[:options].
func volumeBind(spec string) (Mount, bool) {
	parts := strings.Split(spec, ":")
	if len(parts) < 2 || !filepath.IsAbs(parts[0]) || parts[1] == "" {
		return Mount{}, false
	}
	return Mount{Source: parts[0], Target: parts[1]}, true
}

// boolFlags are the docker run and create options that take no value.
var boolFlags = map[string]bool{
	"--detach": true, "--disable-content-trust": true, "--help": true,
	"--init": true, "--interactive": true, "--no-healthcheck": true,
	"--oom-kill-disable": true, "--privileged": true, "--publish-all": true,
	"--quiet": true, "--read-only": true, "--rm": true, "--sig-proxy": true,
	"--tty": true, "--use-api-socket": true,
}

// boolShortFlags are the single letter docker run and create options that
// take no value, which can be combined as in -it.
const boolShortFlags = "diPqt"

// parseOptions returns the index of the image, the first argument from start
// on that is neither an option nor an option's value, and the indexes of the
// option values before it.
func parseOptions(args []string, start int) (int, map[int]bool) {
	values := map[int]bool{}
	for i := start; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			return i + 1, values
		case !strings.HasPrefix(a, "-") || a == "-":
			return i, values
		case strings.Contains(a, "="):
		case strings.HasPrefix(a, "--"):
			if !boolFlags[a] {
				i++
				values[i] = true
			}
		case strings.Trim(a[1:], boolShortFlags) != "":
			i++
			values[i] = true
		}
	}
	return len(args), values
}
