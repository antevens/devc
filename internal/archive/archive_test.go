package archive

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestAddTree(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dotfiles := filepath.Join(dir, "dotfiles")
	for _, d := range []string{src + "/projects", src + "/agents", dotfiles} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, data string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	symlink := func(target, path string) {
		t.Helper()
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
	}
	write(src+"/projects/session.jsonl", "skipped")
	write(src+"/agents/a.md", "agent")
	write(dotfiles+"/settings.json", "linked")
	symlink(dotfiles+"/settings.json", src+"/settings.json")
	symlink(src, src+"/agents/loop")
	symlink(dir+"/missing", src+"/broken")

	a := New(0, 0)
	if err := a.AddTree(".claude", src, map[string]bool{"projects": true}); err != nil {
		t.Fatal(err)
	}
	if err := a.AddTree(".absent", filepath.Join(dir, "absent"), nil); err != nil {
		t.Fatal(err)
	}
	a.AddFile("opt/devc/run", []byte("run"), 0o755)

	want := map[string]string{
		".claude/":              "",
		".claude/agents/":       "",
		".claude/agents/a.md":   "agent",
		".claude/settings.json": "linked",
		"opt/devc/run":          "run",
	}
	if got := entries(t, a); !reflect.DeepEqual(got, want) {
		t.Errorf("entries = %v, want %v", got, want)
	}
}

func TestNewStream(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "repo", "org"), 0o755); err != nil {
		t.Fatal(err)
	}
	big := bytes.Repeat([]byte("x"), 3<<20)
	if err := os.WriteFile(filepath.Join(dir, "repo", "org", "a.jar"), big, 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	a := NewStream(&buf, 1000, 1001)
	a.AddDir("home/vscode/.m2", 0o755)
	if err := a.AddTree("home/vscode/.m2/repository", filepath.Join(dir, "repo"), nil); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}

	tr := tar.NewReader(&buf)
	var names []string
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Uid != 1000 || h.Gid != 1001 {
			t.Errorf("%s: owner %d:%d", h.Name, h.Uid, h.Gid)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg && !bytes.Equal(body, big) {
			t.Errorf("%s: %d bytes, want %d", h.Name, len(body), len(big))
		}
		names = append(names, h.Name)
	}
	want := []string{"home/vscode/.m2/", "home/vscode/.m2/repository/", "home/vscode/.m2/repository/org/", "home/vscode/.m2/repository/org/a.jar"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("names = %v, want %v", names, want)
	}
}

func TestEntry(t *testing.T) {
	if got := Entry("/opt/devc/run"); got != "opt/devc/run" {
		t.Errorf("Entry = %q", got)
	}
}

func entries(t *testing.T, a *Archive) map[string]string {
	t.Helper()
	data, err := a.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	tr := tar.NewReader(bytes.NewReader(data))
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return got
		}
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		got[h.Name] = string(body)
	}
}
