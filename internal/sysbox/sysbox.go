// Package sysbox detects the sysbox runtime, which gives a container what
// nested containers need (docker-in-docker, kind) inside a user namespace
// instead of with host privileges: root in a sysbox container is an
// unprivileged user on the host. Package dockershim creates containers with
// it.
package sysbox

import (
	"encoding/json"
	"fmt"
	"os/exec"
)

// Runtime is the name sysbox registers its runtime with in Docker.
const Runtime = "sysbox-runc"

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
