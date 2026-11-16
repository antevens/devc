package sysbox

import (
	"reflect"
	"testing"
)

func TestArgs(t *testing.T) {
	// The shape of the devcontainer CLI's docker run, with a container
	// command that must not be rewritten.
	cli := []string{
		"run", "--sig-proxy=false", "-a", "STDOUT", "-a", "STDERR",
		"--mount", "type=bind,source=/src,target=/workspaces/src",
		"-l", "devcontainer.local_folder=/src", "--privileged", "--init",
		"--cap-add", "SYS_PTRACE", "--security-opt", "seccomp=unconfined",
		"-e", "A=b", "-it", "-h", "dev", "--entrypoint", "/bin/sh",
		"vsc-src-image", "-c", "echo --privileged", "-", "--privileged",
	}
	want := []string{
		"run", "--runtime=sysbox-runc", "--sig-proxy=false", "-a", "STDOUT", "-a", "STDERR",
		"--mount", "type=bind,source=/src,target=/workspaces/src",
		"-l", "devcontainer.local_folder=/src", "--init",
		"--cap-add", "SYS_PTRACE", "--security-opt", "seccomp=unconfined",
		"-e", "A=b", "-it", "-h", "dev", "--entrypoint", "/bin/sh",
		"vsc-src-image", "-c", "echo --privileged", "-", "--privileged",
	}

	tests := []struct {
		name string
		args []string
		want []string
	}{
		{"devcontainer CLI run", cli, want},
		{"container create", []string{"container", "create", "--privileged=true", "img"},
			[]string{"container", "create", "--runtime=sysbox-runc", "img"}},
		{"privileged false kept", []string{"run", "--privileged=false", "img"},
			[]string{"run", "--runtime=sysbox-runc", "--privileged=false", "img"}},
		{"value that looks like a flag", []string{"run", "-e", "--privileged", "img"},
			[]string{"run", "--runtime=sysbox-runc", "-e", "--privileged", "img"}},
		{"value that looks like a runtime", []string{"run", "--label", "--runtime=x", "--privileged", "img"},
			[]string{"run", "--runtime=sysbox-runc", "--label", "--runtime=x", "img"}},
		{"end of options", []string{"run", "--privileged", "--", "img", "--privileged"},
			[]string{"run", "--runtime=sysbox-runc", "--", "img", "--privileged"}},
		{"runtime chosen", []string{"run", "--runtime", "nvidia", "--privileged", "img"},
			[]string{"run", "--runtime", "nvidia", "--privileged", "img"}},
		{"runtime chosen with =", []string{"run", "--privileged", "--runtime=runc", "img"},
			[]string{"run", "--privileged", "--runtime=runc", "img"}},
		{"runtime in the command", []string{"run", "img", "--runtime=x"},
			[]string{"run", "--runtime=sysbox-runc", "img", "--runtime=x"}},
		{"exec", []string{"exec", "-it", "c", "run", "--privileged"},
			[]string{"exec", "-it", "c", "run", "--privileged"}},
		{"build", []string{"buildx", "build", "-t", "x", "."}, []string{"buildx", "build", "-t", "x", "."}},
		{"container without subcommand", []string{"container"}, []string{"container"}},
		{"none", nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Args(tt.args); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got  %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestHasRuntime(t *testing.T) {
	tests := []struct {
		json string
		want bool
	}{
		{`{"io.containerd.runc.v2":{"path":"runc"},"runc":{"path":"runc"},"sysbox-runc":{"path":"/usr/bin/sysbox-runc"}}`, true},
		{`{"io.containerd.runc.v2":{"path":"runc"},"runc":{"path":"runc"}}`, false},
		{`{}`, false},
	}
	for _, tt := range tests {
		got, err := hasRuntime([]byte(tt.json))
		if err != nil {
			t.Fatal(err)
		}
		if got != tt.want {
			t.Errorf("%s: got %v, want %v", tt.json, got, tt.want)
		}
	}
	if _, err := hasRuntime([]byte("not json")); err == nil {
		t.Error("invalid JSON: want an error")
	}
}
