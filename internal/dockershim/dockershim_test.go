package dockershim

import (
	"reflect"
	"strings"
	"testing"
)

const (
	home      = "/home/u"
	workspace = "/home/u/src/proj"
)

var (
	mountsOnly = Options{Home: home, Workspace: workspace}
	sysboxOnly = Options{Runtime: "sysbox-runc"}
)

// graftMounts are the bind mounts the graft dev-base feature declares, as
// the devcontainer CLI passes them to docker run.
var graftMounts = []Mount{
	{"/home/u/.claude", "/home/vscode/.claude"},
	{"/home/u/.claude.json", "/home/vscode/.claude.json"},
	{"/home/u/.gemini", "/home/vscode/.gemini"},
	{"/home/u/.m2/repository", "/home/vscode/.m2/repository"},
	{"/home/u/.expeto-dev-bash-history", "/commandhistory"},
}

func TestRewriteDevcontainerRun(t *testing.T) {
	args := []string{
		"run", "--sig-proxy=false", "-a", "STDOUT", "-a", "STDERR",
		"--mount", "type=bind,source=/home/u/src/proj,target=/workspaces/proj,consistency=cached",
		"--mount", "type=volume,src=wrkspc-proj-abc,dst=/workspaces",
		"--mount", "source=/home/u/.claude,target=/home/vscode/.claude,type=bind",
		"--mount", "type=bind,source=/home/u/.claude.json,target=/home/vscode/.claude.json",
		"--mount=type=bind,src=/home/u/.gemini,dst=/home/vscode/.gemini",
		"--mount", `"type=bind","source=/home/u/.m2/repository","target=/home/vscode/.m2/repository"`,
		"-v", "/home/u/.expeto-dev-bash-history:/commandhistory",
		"--mount", "type=bind,source=/var/run/docker.sock,target=/var/run/docker-host.sock",
		"-v", "/home/u/src/proj/.cache:/cache:ro",
		"-l", "devcontainer.local_folder=/home/u/src/proj", "--privileged",
		"--entrypoint", "/bin/sh", "vsc-proj-image",
		"-c", "echo started", "--mount", "type=bind,source=/home/u/.ssh,target=/x",
	}
	got := Rewrite(args, mountsOnly)

	if got[0] != "run" || got[1] != "--label" || !strings.HasPrefix(got[2], Label+"=") {
		t.Fatalf("label not added after run: %q", got[:3])
	}
	mounts, err := ParseLabel(strings.TrimPrefix(got[2], Label+"="))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(mounts, graftMounts) {
		t.Errorf("label mounts:\ngot  %v\nwant %v", mounts, graftMounts)
	}
	want := []string{
		"run", got[1], got[2], "--sig-proxy=false", "-a", "STDOUT", "-a", "STDERR",
		"--mount", "type=bind,source=/home/u/src/proj,target=/workspaces/proj,consistency=cached",
		"--mount", "type=volume,src=wrkspc-proj-abc,dst=/workspaces",
		"--mount", "type=bind,source=/var/run/docker.sock,target=/var/run/docker-host.sock",
		"-v", "/home/u/src/proj/.cache:/cache:ro",
		"-l", "devcontainer.local_folder=/home/u/src/proj", "--privileged",
		"--entrypoint", "/bin/sh", "vsc-proj-image",
		"-c", "echo started", "--mount", "type=bind,source=/home/u/.ssh,target=/x",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func TestRewriteUnchanged(t *testing.T) {
	tests := [][]string{
		nil,
		{"container"},
		{"exec", "-it", "c", "run", "--mount", "type=bind,source=/home/u/.claude,target=/x"},
		{"inspect", "c"},
		{"run", "--mount", "type=bind,source=/home/u/src/proj,target=/w", "img"},
		{"run", "-e", "--mount", "img"},
		{"container", "create", "-v", "named:/data", "img"},
	}
	for _, args := range tests {
		if got := Rewrite(args, mountsOnly); !reflect.DeepEqual(got, args) {
			t.Errorf("Rewrite(%q) = %q, want it unchanged", args, got)
		}
	}
	if got := Rewrite([]string{"run", "-v", "/home/u/.claude:/x", "img"}, Options{Workspace: workspace}); len(got) != 4 {
		t.Errorf("without a home directory: got %q", got)
	}
}

func TestRewriteRuntime(t *testing.T) {
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
			if got := Rewrite(tt.args, sysboxOnly); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got  %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestRewriteRuntimeAndMounts(t *testing.T) {
	o := Options{Home: home, Workspace: workspace, Runtime: "sysbox-runc"}
	args := []string{"run", "--privileged", "--mount", "type=bind,source=/home/u/.gemini,target=/g",
		"--mount", "type=bind,source=/home/u/src/proj,target=/w", "img"}
	want := []string{"run", "--runtime=sysbox-runc", "--label", Label + `=[{"source":"/home/u/.gemini","target":"/g"}]`,
		"--mount", "type=bind,source=/home/u/src/proj,target=/w", "img"}
	if got := Rewrite(args, o); !reflect.DeepEqual(got, want) {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func TestRewriteContainerCreate(t *testing.T) {
	got := Rewrite([]string{"container", "create", "--volume=/home/u/.gemini:/g", "img"}, mountsOnly)
	if len(got) != 5 || got[0] != "container" || got[1] != "create" || got[2] != "--label" || got[4] != "img" {
		t.Errorf("got %q", got)
	}
}

func TestCopyInstead(t *testing.T) {
	tests := []struct {
		source string
		want   bool
	}{
		{"/home/u/.claude", true},
		{"/home/u/.m2/repository", true},
		{"/home/u/.config/gh/hosts.yml", true},
		{"/home/u/src/proj", false},
		{"/home/u/src/proj/.git", false},
		{"/home/u/src/other", false},
		{"/home/u", false},
		{"/home/user/.claude", false},
		{"/home/u/../v/.x", false},
		{"/var/run/docker.sock", false},
		{"relative/.x", false},
	}
	for _, tt := range tests {
		if got := CopyInstead(tt.source, home, workspace); got != tt.want {
			t.Errorf("CopyInstead(%s) = %v, want %v", tt.source, got, tt.want)
		}
	}
	if !CopyInstead("/home/u/.dotfiles-workspace/x", home, "/home/u/.dotfiles-workspace/y") {
		t.Error("a hidden entry beside a hidden workspace: want a copy")
	}
	if CopyInstead("/home/u/.ws/sub", home, "/home/u/.ws") {
		t.Error("inside a hidden workspace: want a mount")
	}
}

func TestParseLabel(t *testing.T) {
	if mounts, err := ParseLabel(""); err != nil || mounts != nil {
		t.Errorf("empty: %v, %v", mounts, err)
	}
	if _, err := ParseLabel("{"); err == nil {
		t.Error("invalid: want an error")
	}
}
