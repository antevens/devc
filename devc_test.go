package main

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestFilterDockerConfig(t *testing.T) {
	in := `{"auths":{"ghcr.io":{},"Upload.example.com":{}},"credHelpers":{"upload-registry":"gcr","docker.io":"pass"},"other":1}`
	out, err := filterDockerConfig([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"auths":       map[string]any{"ghcr.io": map[string]any{}},
		"credHelpers": map[string]any{"docker.io": "pass"},
		"other":       float64(1),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestClaudeOverlay(t *testing.T) {
	user := `{"model":"opus","hooks":{"Stop":[{"hooks":[{"type":"command","command":"mine"}]}]}}`
	out, err := claudeOverlay([]byte(user))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Model string
		Hooks struct {
			Stop []struct {
				Hooks []struct{ Command string }
			}
		}
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got.Model != "opus" {
		t.Errorf("model = %q, want the user's", got.Model)
	}
	var commands []string
	for _, matcher := range got.Hooks.Stop {
		for _, h := range matcher.Hooks {
			commands = append(commands, h.Command)
		}
	}
	if want := []string{"mine", logHookPath}; !reflect.DeepEqual(commands, want) {
		t.Errorf("Stop hooks = %v, want %v", commands, want)
	}

	if _, err := claudeOverlay(nil); err != nil {
		t.Errorf("no user settings: %v", err)
	}
	if _, err := claudeOverlay([]byte("{")); err == nil {
		t.Error("invalid settings: want an error")
	}
}

func TestRemoteUser(t *testing.T) {
	tests := []struct {
		name, metadata, imageUser, want string
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
			got, err := remoteUser(tt.metadata, tt.imageUser)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestArchiveAddTree(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dotfiles := filepath.Join(dir, "dotfiles")
	for _, d := range []string{src + "/projects", src + "/agents", dotfiles} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, data string) {
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(src+"/projects/session.jsonl", "skipped")
	write(src+"/agents/a.md", "agent")
	write(dotfiles+"/settings.json", "linked")
	symlink := func(target, path string) {
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
	}
	symlink(dotfiles+"/settings.json", src+"/settings.json")
	symlink(src, src+"/agents/loop")
	symlink(dir+"/missing", src+"/broken")

	a := newArchive(0, 0)
	if err := a.addTree(".claude", src, map[string]bool{"projects": true}); err != nil {
		t.Fatal(err)
	}
	if err := a.addTree(".absent", filepath.Join(dir, "absent"), nil); err != nil {
		t.Fatal(err)
	}
	data, err := a.close()
	if err != nil {
		t.Fatal(err)
	}

	got := map[string]string{}
	tr := tar.NewReader(bytes.NewReader(data))
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(tr)
		got[h.Name] = string(body)
	}
	want := map[string]string{
		".claude/":              "",
		".claude/agents/":       "",
		".claude/agents/a.md":   "agent",
		".claude/settings.json": "linked",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("archive entries = %v, want %v", keys(got), keys(want))
	}
}

func keys(m map[string]string) []string {
	var k []string
	for name := range m {
		k = append(k, name)
	}
	sort.Strings(k)
	return k
}

func TestRefreshConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	shared := filepath.Join(dir, "shared.json")
	if err := os.WriteFile(shared, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := &project{workspace: filepath.Join(dir, "repo"), defaultConfig: shared, home: dir}
	if err := p.initConfig(); err != nil {
		t.Fatal(err)
	}
	if err := p.initConfig(); err == nil {
		t.Error("second init: want an error")
	}

	read := func() string {
		data, err := os.ReadFile(p.repoConfig())
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}

	if err := os.WriteFile(shared, []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := p.refreshConfig(); err != nil {
		t.Fatal(err)
	}
	if got := read(); got != "v2" {
		t.Errorf("unmodified config = %q, want it replaced", got)
	}

	if err := os.WriteFile(p.repoConfig(), []byte("local"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(shared, []byte("v3"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := p.refreshConfig(); err != nil {
		t.Fatal(err)
	}
	if got := read(); got != "local" {
		t.Errorf("modified config = %q, want it kept", got)
	}
}
