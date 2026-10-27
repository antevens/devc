package project

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/antevens/devc/internal/forge"
)

func TestFeaturesFor(t *testing.T) {
	tests := []struct {
		name       string
		configured map[string]any
		want       map[string]any
	}{
		{"none", nil, map[string]any{claudeFeature: map[string]any{}, nodeFeature: map[string]any{}}},
		{"node", map[string]any{"ghcr.io/devcontainers/features/node:1": map[string]any{"version": "22"}},
			map[string]any{claudeFeature: map[string]any{}}},
		{"claude", map[string]any{"ghcr.io/anthropics/devcontainer-features/claude-code:1.0": map[string]any{}}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := featuresFor(tt.configured)
			if err != nil {
				t.Fatal(err)
			}
			if tt.want == nil {
				if data != nil {
					t.Errorf("got %s, want nil", data)
				}
				return
			}
			var got map[string]any
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConfigArgs(t *testing.T) {
	dir := t.TempDir()
	p := &Project{Workspace: dir, SharedConfig: "/shared.json"}
	want := []string{"--workspace-folder", dir, "--override-config", "/shared.json", "--no-lockfile"}
	if got := p.configArgs(true); !reflect.DeepEqual(got, want) {
		t.Errorf("shared config: got %v, want %v", got, want)
	}

	if err := os.WriteFile(filepath.Join(dir, ".devcontainer.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	want = []string{"--workspace-folder", dir}
	if got := p.configArgs(true); !reflect.DeepEqual(got, want) {
		t.Errorf("own config: got %v, want %v", got, want)
	}
}

func TestSessionEnv(t *testing.T) {
	tokens := []*forge.Token{{Kind: forge.GitLab, Host: "gitlab.com", Token: "glpat_1"}}
	want := "CLAUDE_REMOTE_CONTROL_SESSION_NAME_PREFIX='my '\\''repo'\\'''\n" +
		"export CLAUDE_REMOTE_CONTROL_SESSION_NAME_PREFIX\n" +
		"CLAUDE_CODE_SCROLL_SPEED=\"${CLAUDE_CODE_SCROLL_SPEED:-12}\"\n" +
		"export CLAUDE_CODE_SCROLL_SPEED\n" +
		"GITLAB_HOST=gitlab.com\n" +
		"GITLAB_TOKEN=\"$(cat /opt/devc/tokens/gitlab.com)\"\n" +
		"export GITLAB_HOST GITLAB_TOKEN\n"
	if got := sessionEnv("my 'repo'", tokens); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestNearestWorkspace(t *testing.T) {
	configs := map[string]bool{"/src/withconfig": true}
	hasConfig := func(dir string) bool { return configs[dir] }
	folders := map[string]bool{"/src/shared": true}
	tests := []struct{ dir, want string }{
		{"/src/withconfig/sub/dir", "/src/withconfig"},
		{"/src/withconfig", "/src/withconfig"},
		{"/src/shared/sub", "/src/shared"},
		{"/src/other/sub", "/src/other/sub"},
	}
	for _, tt := range tests {
		if got := nearestWorkspace(tt.dir, folders, hasConfig); got != tt.want {
			t.Errorf("nearestWorkspace(%s) = %s, want %s", tt.dir, got, tt.want)
		}
	}
}
