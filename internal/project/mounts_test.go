package project

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestConfigurationMounts(t *testing.T) {
	data := `{"mounts": [
		"type=bind,src=/home/u/.m2/repository,dst=/home/vscode/.m2/repository",
		"source=/home/u/.gemini,target=/home/vscode/.gemini,type=bind,consistency=cached",
		"\"type=bind\",\"source=/home/u/a,b\",target=/b",
		"Type=volume,Source=cache,target=/cache",
		"target=/tmp/anon",
		{"type": "bind", "source": "/home/u/.claude", "target": "/home/vscode/.claude"}
	]}`
	want := []mount{
		{Type: "bind", Source: "/home/u/.m2/repository"},
		{Type: "bind", Source: "/home/u/.gemini"},
		{Type: "bind", Source: "/home/u/a,b"},
		{Type: "volume", Source: "cache"},
		{Type: "volume"},
		{Type: "bind", Source: "/home/u/.claude"},
	}
	var cfg configuration
	if err := json.Unmarshal([]byte(data), &cfg); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.Mounts, want) {
		t.Errorf("got %v, want %v", cfg.Mounts, want)
	}
}

func TestConfigurationMountsInvalid(t *testing.T) {
	for _, data := range []string{`{"mounts": [""]}`, `{"mounts": ["source=\"a"]}`, `{"mounts": [1]}`} {
		var cfg configuration
		if err := json.Unmarshal([]byte(data), &cfg); err == nil {
			t.Errorf("%s: got nil error", data)
		}
	}
}

func TestCreateMountSources(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	existing := filepath.Join(home, ".claude.json")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(existing, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	dangling := filepath.Join(home, ".dangling")
	if err := os.Symlink(filepath.Join(root, "missing"), dangling); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(home, ".m2", "repository")
	outside := filepath.Join(root, "outside")
	volume := filepath.Join(home, ".volume")
	mounts := []mount{
		{Type: "bind", Source: missing},
		{Type: "bind", Source: existing},
		{Type: "bind", Source: dangling},
		{Type: "bind", Source: outside},
		{Type: "bind", Source: filepath.Join(home, "..", "escape")},
		{Type: "bind", Source: "relative"},
		{Type: "volume", Source: volume},
	}

	var out strings.Builder
	if err := createMountSources(mounts, home, &out); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(missing); err != nil || !info.IsDir() {
		t.Errorf("%s: not created as a directory: %v", missing, err)
	}
	if info, err := os.Stat(existing); err != nil || !info.Mode().IsRegular() {
		t.Errorf("%s: changed: %v", existing, err)
	}
	if _, err := os.Readlink(dangling); err != nil {
		t.Errorf("%s: changed: %v", dangling, err)
	}
	for _, path := range []string{outside, filepath.Join(root, "escape"), volume, filepath.Join(home, "relative")} {
		if _, err := os.Lstat(path); err == nil {
			t.Errorf("%s: created", path)
		}
	}
	if want := "devc: created missing mount source " + missing + "\n"; out.String() != want {
		t.Errorf("got output %q, want %q", out.String(), want)
	}
}

func TestWithin(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/home/u", true},
		{"/home/u/.m2/repository", true},
		{"/home/u/../v", false},
		{"/home/user", false},
		{"/var/run/docker.sock", false},
		{"u/.m2", false},
	}
	for _, tt := range tests {
		if got := within("/home/u", tt.path); got != tt.want {
			t.Errorf("within(/home/u, %s) = %v, want %v", tt.path, got, tt.want)
		}
	}
}
