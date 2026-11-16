// Package sysbox runs devcontainers with the sysbox runtime, which gives a
// container what nested containers need (docker-in-docker, kind) inside a
// user namespace instead of with host privileges: root in a sysbox container
// is an unprivileged user on the host.
//
// The devcontainer CLI has no option for the container runtime, so devc
// passes itself as the CLI's docker executable. Invoked with ShimEnv set to
// the real docker, devc rewrites container creation to the sysbox runtime and
// runs the real docker with every other command unchanged.
package sysbox

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// Runtime is the name sysbox registers its runtime with in Docker.
const Runtime = "sysbox-runc"

// ShimEnv names the environment variable holding the path of the real docker
// executable when devc runs as the devcontainer CLI's docker shim.
const ShimEnv = "DEVC_SYSBOX_DOCKER"

// Available reports whether the Docker daemon has the sysbox runtime.
func Available() (bool, error) {
	out, err := exec.Command("docker", "info", "--format", "{{json .Runtimes}}").Output()
	if err != nil {
		return false, fmt.Errorf("docker info: %w", err)
	}
	return hasRuntime(out)
}

func hasRuntime(runtimesJSON []byte) (bool, error) {
	var runtimes map[string]any
	if err := json.Unmarshal(runtimesJSON, &runtimes); err != nil {
		return false, fmt.Errorf("parse docker runtimes: %w", err)
	}
	_, ok := runtimes[Runtime]
	return ok, nil
}

// Shim runs the real docker with args, rewritten by Args. It returns only
// when the real docker cannot be started.
func Shim(docker string, args []string) error {
	argv := append([]string{docker}, Args(args)...)
	return syscall.Exec(docker, argv, os.Environ())
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

// Args returns docker arguments with `run` and `create` (also as `container
// run` and `container create`) moved to the sysbox runtime: --runtime is
// added and --privileged is removed, as the container gets what nested
// containers need from sysbox instead. Commands that already choose a
// runtime, and all other commands, are returned unchanged. Only the options
// before the image are rewritten; the container's command is left alone.
func Args(args []string) []string {
	start := 0
	if len(args) > 0 && args[0] == "container" {
		start = 1
	}
	if len(args) <= start || (args[start] != "run" && args[start] != "create") {
		return args
	}
	start++
	end, values := parseOptions(args, start)
	for i := start; i < end; i++ {
		if !values[i] && (args[i] == "--runtime" || strings.HasPrefix(args[i], "--runtime=")) {
			return args
		}
	}

	out := make([]string, 0, len(args)+1)
	out = append(out, args[:start]...)
	out = append(out, "--runtime="+Runtime)
	for i := start; i < end; i++ {
		if !values[i] && privileged(args[i]) {
			continue
		}
		out = append(out, args[i])
	}
	return append(out, args[end:]...)
}

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

// privileged reports whether a is an option making the container privileged.
func privileged(a string) bool {
	switch a {
	case "--privileged", "--privileged=true", "--privileged=1":
		return true
	}
	return false
}
