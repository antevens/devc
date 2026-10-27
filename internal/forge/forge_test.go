package forge

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/antevens/devc/internal/config"
)

func TestParseRemote(t *testing.T) {
	tests := []struct {
		url        string
		host, path string
		ssh        []string
		ok         bool
	}{
		{"https://github.com/owner/repo.git", "github.com", "owner/repo", []string{"git@github.com:", "ssh://git@github.com/"}, true},
		{"git@gitlab.com:group/sub/repo.git", "gitlab.com", "group/sub/repo", []string{"git@gitlab.com:", "ssh://git@gitlab.com/"}, true},
		{"ssh://git@gitlab.example.com:2222/group/repo", "gitlab.example.com", "group/repo",
			[]string{"git@gitlab.example.com:", "ssh://git@gitlab.example.com/", "ssh://git@gitlab.example.com:2222/"}, true},
		{"https://user@github.com/owner/repo/", "github.com", "owner/repo", []string{"git@github.com:", "ssh://git@github.com/"}, true},
		{"/srv/git/repo.git", "", "", nil, false},
		{"file:///srv/git/repo.git", "", "", nil, false},
	}
	for _, tt := range tests {
		r, ok := parseRemote(tt.url)
		if ok != tt.ok {
			t.Errorf("%s: ok = %v, want %v", tt.url, ok, tt.ok)
			continue
		}
		if !ok {
			continue
		}
		if r.host != tt.host || r.path != tt.path || !reflect.DeepEqual(r.sshURLs(), tt.ssh) {
			t.Errorf("%s: got %s %s %v, want %s %s %v", tt.url, r.host, r.path, r.sshURLs(), tt.host, tt.path, tt.ssh)
		}
	}
}

func TestDetect(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	git("init", "-q")
	git("remote", "add", "mirror", "https://github.com/owner/mirror.git")
	git("remote", "add", "origin", "git@code.example.com:group/repo.git")
	git("remote", "add", "fork", "https://github.com/me/fork.git")
	if err := os.WriteFile(filepath.Join(dir, ".gitlab-ci.yml"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	s := config.Settings{GitLab: []config.GitLab{{Host: "code.example.com"}}}
	want := []Forge{
		{Kind: GitLab, Host: "code.example.com", Project: "group/repo", SSHURLs: []string{"git@code.example.com:", "ssh://git@code.example.com/"}},
		{Kind: GitHub, Host: "github.com", Project: "owner/mirror", SSHURLs: []string{"git@github.com:", "ssh://git@github.com/"}},
	}
	if got := Detect(dir, s); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}

	plain := t.TempDir()
	if err := os.Mkdir(filepath.Join(plain, ".github"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := Detect(plain, config.Settings{}); !reflect.DeepEqual(got, []Forge{{Kind: GitHub, Host: "github.com"}}) {
		t.Errorf("from files: got %+v", got)
	}
}

func TestContainerConfig(t *testing.T) {
	forges := []Forge{
		{Kind: GitHub, Host: "github.com", Project: "o/r", SSHURLs: []string{"git@github.com:"}},
		{Kind: GitLab, Host: "gitlab.com", Project: "g/r", SSHURLs: []string{"git@gitlab.com:"}},
	}
	tokens := []*Token{
		{Kind: GitHub, Host: "github.com", Token: "ghs_1"},
		{Kind: GitLab, Host: "gitlab.com", Token: "glpat_1"},
	}

	wantGit := "[credential]\n\thelper = /opt/devc/git-credential-devc\n" +
		"[url \"https://github.com/\"]\n\tinsteadOf = git@github.com:\n" +
		"[url \"https://gitlab.com/\"]\n\tinsteadOf = git@gitlab.com:\n"
	if got := gitConfig(forges, tokens); got != wantGit {
		t.Errorf("gitConfig:\n%s\nwant:\n%s", got, wantGit)
	}

	wantEnv := []string{"GITLAB_HOST=gitlab.com", `GITLAB_TOKEN="$(cat /opt/devc/tokens/gitlab.com)"`, "export GITLAB_HOST GITLAB_TOKEN"}
	if got := Env(tokens); !reflect.DeepEqual(got, wantEnv) {
		t.Errorf("Env = %q, want %q", got, wantEnv)
	}
	if strings.Contains(strings.Join(Env(tokens), "\n"), "glpat_1") {
		t.Error("Env contains the token")
	}

	wantHosts := "github.com:\n    oauth_token: ghs_1\n    git_protocol: https\n    user: x-access-token\n"
	if got := ghHosts(tokens); got != wantHosts {
		t.Errorf("ghHosts:\n%s\nwant:\n%s", got, wantHosts)
	}
}
