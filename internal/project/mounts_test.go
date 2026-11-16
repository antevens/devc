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

func TestCheckMountSources(t *testing.T) {
	root := t.TempDir()
	existing := filepath.Join(root, ".claude.json")
	if err := os.WriteFile(existing, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	dangling := filepath.Join(root, ".dangling")
	if err := os.Symlink(filepath.Join(root, "gone"), dangling); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(root, ".m2", "repository")
	volume := filepath.Join(root, ".volume")

	if err := checkMountSources([]mount{
		{Type: "bind", Source: existing},
		{Type: "bind", Source: "relative"},
		{Type: "volume", Source: volume},
	}); err != nil {
		t.Errorf("existing sources: %v", err)
	}

	err := checkMountSources([]mount{
		{Type: "bind", Source: missing},
		{Type: "bind", Source: existing},
		{Type: "bind", Source: dangling},
	})
	if err == nil {
		t.Fatal("missing sources: want an error")
	}
	for _, path := range []string{missing, dangling} {
		if !strings.Contains(err.Error(), path) {
			t.Errorf("error %q does not name %s", err, path)
		}
	}
	if strings.Contains(err.Error(), existing+",") || strings.HasSuffix(err.Error(), existing) {
		t.Errorf("error %q names the existing source", err)
	}
	for _, path := range []string{missing, filepath.Dir(missing)} {
		if _, err := os.Lstat(path); err == nil {
			t.Errorf("%s: created", path)
		}
	}
}
