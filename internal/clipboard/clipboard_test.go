package clipboard

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestFindHost(t *testing.T) {
	tests := []struct {
		name      string
		env       map[string]string
		installed []string
		want      string
	}{
		{"x11", map[string]string{"DISPLAY": ":0"}, []string{"xclip", "xsel", "wl-copy"}, "xclip"},
		{"xsel", map[string]string{"DISPLAY": ":0"}, []string{"xsel"}, "xsel"},
		{"xwayland", map[string]string{"DISPLAY": ":0", "WAYLAND_DISPLAY": "wayland-0"}, []string{"xclip", "wl-copy"}, "xclip"},
		{"wayland", map[string]string{"DISPLAY": ":0", "WAYLAND_DISPLAY": "wayland-0"}, []string{"wl-copy"}, "wl-copy"},
		{"wayland only", map[string]string{"WAYLAND_DISPLAY": "wayland-0"}, []string{"xclip", "wl-copy"}, "wl-copy"},
	}
	for _, tt := range tests {
		h, err := findHost(getenv(tt.env), lookPath(tt.installed...))
		if err != nil {
			t.Errorf("%s: %v", tt.name, err)
			continue
		}
		if got := h.commands[0][0]; got != tt.want {
			t.Errorf("%s: got %s, want %s", tt.name, got, tt.want)
		}
		if len(h.commands) != 2 {
			t.Errorf("%s: %d commands, want clipboard and primary", tt.name, len(h.commands))
		}
	}

	if _, err := findHost(getenv(nil), lookPath("xclip")); !errors.Is(err, ErrNoDisplay) {
		t.Errorf("no display: %v", err)
	}
	if _, err := findHost(getenv(map[string]string{"WAYLAND_DISPLAY": "wayland-0"}), lookPath("xclip")); err == nil || errors.Is(err, ErrNoDisplay) {
		t.Errorf("no tool for the display: %v", err)
	}
}

func getenv(env map[string]string) func(string) string {
	return func(name string) string { return env[name] }
}

func lookPath(installed ...string) func(string) (string, error) {
	return func(name string) (string, error) {
		for _, i := range installed {
			if i == name {
				return "/usr/bin/" + name, nil
			}
		}
		return "", errors.New("not found")
	}
}

func TestRelay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clipboard")
	h := &Host{commands: [][]string{{"sh", "-c", `cat >>"$0"`, path}, {"sh", "-c", `cat >>"$0"`, path}}}
	r := NewRelay(h, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r.Copy([]byte("hi"))
	r.Close()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hihi" {
		t.Errorf("got %q, want the copy written by both commands", data)
	}
}
