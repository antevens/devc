package container

import (
	"archive/tar"
	"bytes"
	"strings"
	"testing"
)

func TestRemoteUser(t *testing.T) {
	tests := []struct {
		name, label, imageUser, want string
	}{
		{"remote user wins", `[{"containerUser":"a"},{"remoteUser":"b"},{"containerUser":"c"}]`, "", "b"},
		{"last remote user", `[{"remoteUser":"a"},{"remoteUser":"b"}]`, "", "b"},
		{"container user", `[{"containerUser":"a"}]`, "img", "a"},
		{"single entry", `{"remoteUser":"a"}`, "", "a"},
		{"image user", ``, "img", "img"},
		{"root", ``, "", "root"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := remoteUser(tt.label, tt.imageUser)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}

	if _, err := remoteUser("{", ""); err == nil {
		t.Error("invalid label: want an error")
	}
}

func TestMountOverlap(t *testing.T) {
	c := &Container{mounts: []string{"/workspaces/repo", "/home/vscode/.claude", "/home/vscode/.local/share/x"}}
	tests := []struct {
		target            string
		covered, overlaps bool
	}{
		{"/home/vscode/.claude", true, true},
		{"/home/vscode/.claude/settings.json", true, true},
		{"/home/vscode/.claude/", true, true},
		{"/home/vscode/.claude.json", false, false},
		{"/home/vscode/.local", false, true},
		{"/home/vscode/.local/state/devc", false, false},
		{"/home/vscode", false, true},
		{"/workspaces/repository", false, false},
		{"/opt/devc", false, false},
	}
	for _, tt := range tests {
		if got := c.Covered(tt.target); got != tt.covered {
			t.Errorf("Covered(%s) = %v, want %v", tt.target, got, tt.covered)
		}
		if got := c.Overlaps(tt.target); got != tt.overlaps {
			t.Errorf("Overlaps(%s) = %v, want %v", tt.target, got, tt.overlaps)
		}
	}
}

func TestExtractRefusesMounts(t *testing.T) {
	c := &Container{ID: "unused", Home: "/home/vscode", mounts: []string{"/home/vscode/.claude.json"}}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, name := range []string{".gitconfig", ".claude.json"} {
		if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: name, Mode: 0o600}); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	err := c.ExtractHome(buf.Bytes())
	if err == nil || !strings.Contains(err.Error(), "/home/vscode/.claude.json") {
		t.Errorf("got %v, want an error naming the mounted file", err)
	}
}

func TestParseSummary(t *testing.T) {
	tests := []struct {
		line string
		want Summary
	}{
		{"abc\t/repo_devcontainer-app-1\trunning\t/src/repo\trepo_devcontainer",
			Summary{ID: "abc", Name: "repo_devcontainer-app-1", Running: true, Workspace: "/src/repo", Project: "repo_devcontainer"}},
		{"def\tdb\texited\t<no value>\t<no value>", Summary{ID: "def", Name: "db"}},
		{"ghi\tname\trunning\t/src/x\t", Summary{ID: "ghi", Name: "name", Running: true, Workspace: "/src/x"}},
	}
	for _, tt := range tests {
		got, err := parseSummary(tt.line)
		if err != nil {
			t.Fatal(err)
		}
		if got != tt.want {
			t.Errorf("parseSummary(%q) = %+v, want %+v", tt.line, got, tt.want)
		}
	}
	if _, err := parseSummary("too\tfew"); err == nil {
		t.Error("short line: want an error")
	}
	if got := (Summary{ID: "0123456789abcdef"}).ShortID(); got != "0123456789ab" {
		t.Errorf("ShortID = %q", got)
	}
}

func TestManagers(t *testing.T) {
	for _, m := range managers {
		for _, command := range []string{"bash", "vim", "shellcheck"} {
			if m.packages[command] == "" {
				t.Errorf("%s has no package for %s", m.name, command)
			}
		}
	}
}

func TestLastLines(t *testing.T) {
	if got := lastLines([]byte("a\nb\nc\n"), 2); got != "b\nc" {
		t.Errorf("lastLines = %q", got)
	}
	if got := lastLines([]byte("a"), 2); got != "a" {
		t.Errorf("lastLines = %q", got)
	}
}

func TestParseSummaries(t *testing.T) {
	out := "aaa\tapp\trunning\t/src/a\tproj\nbbb\tname\texited\t/src/b\t\n"
	got, err := parseSummaries(out)
	if err != nil {
		t.Fatal(err)
	}
	want := []Summary{
		{ID: "aaa", Name: "app", Running: true, Workspace: "/src/a", Project: "proj"},
		{ID: "bbb", Name: "name", Workspace: "/src/b"},
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("got %+v, want %+v", got, want)
	}
}
