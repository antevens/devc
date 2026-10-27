package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))

	s, err := Load(home)
	if err != nil || s.RemoteControl || len(s.GitHub) != 0 {
		t.Fatalf("missing config: %+v, %v", s, err)
	}

	dir := filepath.Join(home, "config", "devc")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	config := `{"remote_control": true, "gitlab": [{"host": "gl.example.com", "token_command": "pass x"}],
		"github": [{"host": "github.com", "app_id": "1", "private_key_file": "~/k.pem"}]}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	if s, err = Load(home); err != nil {
		t.Fatal(err)
	}
	if !s.RemoteControl || s.GitLabFor("gl.example.com").TokenCommand != "pass x" || s.GitHubFor("github.com").AppID != "1" {
		t.Errorf("settings = %+v", s)
	}
	if s.GitLabFor("github.com") != nil || s.GitHubFor("gl.example.com") != nil {
		t.Error("credential found for the wrong host")
	}
}

func TestShared(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("DEVC_CONFIG", "")

	path, created, err := Shared(home)
	if err != nil || !created || path != filepath.Join(home, ".config", "devcontainer", "devcontainer.json") {
		t.Fatalf("first call: %s, %v, %v", path, created, err)
	}
	if _, created, err = Shared(home); err != nil || created {
		t.Errorf("second call: created %v, %v", created, err)
	}

	t.Setenv("DEVC_CONFIG", filepath.Join(home, "missing.json"))
	if _, _, err := Shared(home); err == nil {
		t.Error("missing DEVC_CONFIG: want an error")
	}
}

func TestWorkspace(t *testing.T) {
	dir := t.TempDir()
	shared := filepath.Join(dir, "shared.json")
	write := func(path, data string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	read := func(path string) string {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}

	write(shared, "v1")
	w := Workspace{Dir: filepath.Join(dir, "repo"), Shared: shared, StateDir: filepath.Join(dir, "state")}
	if err := w.Init(); err != nil {
		t.Fatal(err)
	}
	if err := w.Init(); err == nil {
		t.Error("second Init: want an error")
	}

	write(shared, "v2")
	if replaced, err := w.Refresh(); err != nil || !replaced || read(w.Path()) != "v2" {
		t.Errorf("unmodified config: replaced %v, %v, content %q", replaced, err, read(w.Path()))
	}
	if replaced, err := w.Refresh(); err != nil || replaced {
		t.Errorf("unchanged shared config: replaced %v, %v", replaced, err)
	}

	write(w.Path(), "local")
	write(shared, "v3")
	if _, err := w.Refresh(); !errors.Is(err, ErrLocalChanges) || read(w.Path()) != "local" {
		t.Errorf("edited config: %v, content %q", err, read(w.Path()))
	}
}
