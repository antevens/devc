package claude

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/antevens/devc/internal/assets"
)

func TestOverlay(t *testing.T) {
	user := `{"model":"opus","hooks":{"Stop":[{"hooks":[{"type":"command","command":"mine"}]}]}}`
	out, err := Overlay([]byte(user))
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
	if want := []string{"mine", assets.LogHookPath}; !reflect.DeepEqual(commands, want) {
		t.Errorf("Stop hooks = %v, want %v", commands, want)
	}

	if _, err := Overlay(nil); err != nil {
		t.Errorf("no user settings: %v", err)
	}
	if _, err := Overlay([]byte("{")); err == nil {
		t.Error("invalid settings: want an error")
	}
}

func TestCommand(t *testing.T) {
	flags := strings.Join(Flags, " ")
	tests := []struct {
		args   []string
		remote bool
		want   string
	}{
		{nil, true, "claude " + flags + " --remote-control myrepo"},
		{nil, false, "claude " + flags},
		{[]string{"--rc", "x"}, true, "claude " + flags + " --rc x"},
		{[]string{"--remote-control=x"}, true, "claude " + flags + " --remote-control=x"},
		{[]string{"-p", "hi"}, true, "claude " + flags + " -p hi"},
		{[]string{"remote-control", "--spawn", "worktree"}, true, "claude remote-control --spawn worktree"},
	}
	for _, tt := range tests {
		if got := strings.Join(Command(tt.args, "myrepo", tt.remote), " "); got != tt.want {
			t.Errorf("Command(%v, %v) = %q, want %q", tt.args, tt.remote, got, tt.want)
		}
	}
}

func TestRemoteCommand(t *testing.T) {
	if got := strings.Join(RemoteCommand(nil, "myrepo"), " "); got != "claude remote-control --name myrepo" {
		t.Errorf("got %q", got)
	}
	if got := strings.Join(RemoteCommand([]string{"--name", "x"}, "myrepo"), " "); got != "claude remote-control --name x" {
		t.Errorf("with --name: got %q", got)
	}
}
