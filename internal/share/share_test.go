package share

import (
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestCopies(t *testing.T) {
	tests := []struct {
		source, target string
		want           bool
	}{
		{"/home/u/.claude", "/home/vscode/.claude", true},
		{"/home/u/.claude.json", "/home/vscode/.claude.json", true},
		{"/home/u/.claude/agents", "/home/vscode/.claude/agents", true},
		{"/home/u/.ai", "/home/vscode/.ai", true},
		{"/home/u/.gitconfig", "/home/vscode/.gitconfig", true},
		{"/home/u/.docker", "/home/vscode/.docker", true},
		{"/home/u/.docker/config.json", "/home/vscode/.docker/config.json", true},
		{"/home/u/.claude", "/root/.claude", false},
		{"/home/u/.claude", "/home/vscode/.claude-host", false},
		{"/home/u/.gemini", "/home/vscode/.gemini", false},
		{"/home/u/.kube", "/home/vscode/.kube", false},
		{"/home/u/.m2/repository", "/home/vscode/.m2/repository", false},
		{"/other/.claude", "/home/vscode/.claude", false},
	}
	for _, tt := range tests {
		if got := Copies(tt.source, tt.target, "/home/u", "/home/vscode"); got != tt.want {
			t.Errorf("Copies(%s, %s) = %v, want %v", tt.source, tt.target, got, tt.want)
		}
	}
}

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

func TestWatch(t *testing.T) {
	home := t.TempDir()
	dotfiles := filepath.Join(home, "dotfiles")
	claude := filepath.Join(home, ".claude")
	for _, dir := range []string{dotfiles, filepath.Join(claude, "projects"), filepath.Join(home, ".ai")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	settings := filepath.Join(dotfiles, "settings.json")
	if err := os.WriteFile(settings, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(settings, filepath.Join(claude, "settings.json")); err != nil {
		t.Fatal(err)
	}

	changed := make(chan string, 10)
	w, err := Watch(home, slog.New(slog.NewTextHandler(io.Discard, nil)), func(tr Tree) { changed <- tr.Name })
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	expect := func(want string) {
		t.Helper()
		select {
		case got := <-changed:
			if got != want {
				t.Errorf("changed %s, want %s", got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("no change reported for %s", want)
		}
	}
	write := func(path string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(time.Now().String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// A symlink target outside the tree is watched.
	write(settings)
	expect(".claude")
	write(filepath.Join(home, ".ai", "prefs.md"))
	expect(".ai")

	// Skipped entries are not.
	write(filepath.Join(claude, "projects", "session.jsonl"))
	write(filepath.Join(claude, "history.jsonl"))
	select {
	case got := <-changed:
		t.Errorf("change reported for skipped entries in %s", got)
	case <-time.After(3 * debounce):
	}
}
