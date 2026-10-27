package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
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

func TestDetectForges(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
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

	s := settings{GitLab: []gitlabConfig{{Host: "code.example.com"}}}
	got := detectForges(dir, s)
	want := []forge{
		{kind: gitlab, host: "code.example.com", project: "group/repo", sshURLs: []string{"git@code.example.com:", "ssh://git@code.example.com/"}},
		{kind: github, host: "github.com", project: "owner/mirror", sshURLs: []string{"git@github.com:", "ssh://git@github.com/"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}

	// Files alone select the tools to install, without a project for tokens.
	plain := t.TempDir()
	if err := os.Mkdir(filepath.Join(plain, ".github"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := detectForges(plain, settings{}); !reflect.DeepEqual(got, []forge{{kind: github, host: "github.com"}}) {
		t.Errorf("from files: got %+v", got)
	}
}

func TestContainerConfig(t *testing.T) {
	forges := []forge{
		{kind: github, host: "github.com", project: "o/r", sshURLs: []string{"git@github.com:"}},
		{kind: gitlab, host: "gitlab.com", project: "g/r", sshURLs: []string{"git@gitlab.com:"}},
	}
	tokens := []*token{
		{Kind: github, Host: "github.com", Token: "ghs_1"},
		{Kind: gitlab, Host: "gitlab.com", Token: "glpat_1"},
	}

	if got, want := gitConfig(forges, tokens), "# Installed by devc.\n[credential]\n\thelper = /opt/devc/git-credential-devc\n"+
		"[url \"https://github.com/\"]\n\tinsteadOf = git@github.com:\n"+
		"[url \"https://gitlab.com/\"]\n\tinsteadOf = git@gitlab.com:\n"; got != want {
		t.Errorf("gitConfig:\n%s\nwant:\n%s", got, want)
	}

	env := envScript("my 'repo'", tokens)
	for _, want := range []string{
		`CLAUDE_REMOTE_CONTROL_SESSION_NAME_PREFIX='my '\''repo'\'''`,
		"GITLAB_HOST=gitlab.com\n",
		`GITLAB_TOKEN="$(cat /opt/devc/tokens/gitlab.com)"`,
	} {
		if !strings.Contains(env, want) {
			t.Errorf("envScript lacks %q:\n%s", want, env)
		}
	}
	if strings.Contains(env, "ghs_1") || strings.Contains(env, "glpat_1") {
		t.Errorf("envScript contains a token:\n%s", env)
	}

	if got, want := ghHosts(tokens), "github.com:\n    oauth_token: ghs_1\n    git_protocol: https\n    user: x-access-token\n"; got != want {
		t.Errorf("ghHosts:\n%s\nwant:\n%s", got, want)
	}
}

func TestClaudeCommand(t *testing.T) {
	p := &project{workspace: "/src/myrepo", settings: settings{RemoteControl: true}}
	flags := strings.Join(claudeFlags, " ")
	tests := []struct {
		args []string
		want string
	}{
		{nil, "claude " + flags + " --remote-control myrepo"},
		{[]string{"--rc", "x"}, "claude " + flags + " --rc x"},
		{[]string{"-p", "hi"}, "claude " + flags + " -p hi"},
		{[]string{"remote-control", "--spawn", "worktree"}, "claude remote-control --spawn worktree"},
	}
	for _, tt := range tests {
		if got := strings.Join(p.claudeCommand(tt.args), " "); got != tt.want {
			t.Errorf("%v: got %q, want %q", tt.args, got, tt.want)
		}
	}
	p.settings.RemoteControl = false
	if got := strings.Join(p.claudeCommand(nil), " "); got != "claude "+flags {
		t.Errorf("without remote_control: got %q", got)
	}
	if got := strings.Join(p.remoteCommand(nil), " "); got != "claude remote-control --name myrepo" {
		t.Errorf("remoteCommand: got %q", got)
	}
}

func TestAppJWT(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(t.TempDir(), "app.pem")
	pemData := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	if err := os.WriteFile(keyFile, pemData, 0o600); err != nil {
		t.Fatal(err)
	}

	now := time.Unix(1_800_000_000, 0)
	jwt, err := appJWT("42", keyFile, now)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Fatalf("jwt has %d parts", len(parts))
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, digest[:], signature); err != nil {
		t.Errorf("signature: %v", err)
	}
	claimsJSON, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var claims struct {
		Iat, Exp int64
		Iss      string
	}
	if err := json.Unmarshal(claimsJSON, &claims); err != nil {
		t.Fatal(err)
	}
	if claims.Iss != "42" || claims.Iat != now.Unix()-60 || claims.Exp != now.Unix()+540 {
		t.Errorf("claims = %+v", claims)
	}
}

// fakeForge serves a forge API over TLS and points httpClient at it.
func fakeForge(t *testing.T, handler http.HandlerFunc) string {
	srv := httptest.NewTLSServer(handler)
	t.Cleanup(srv.Close)
	previous := httpClient
	httpClient = srv.Client()
	t.Cleanup(func() { httpClient = previous })
	return strings.TrimPrefix(srv.URL, "https://")
}

func TestGitlabToken(t *testing.T) {
	var created []map[string]any
	var revoked []string
	host := fakeForge(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v4/personal_access_tokens/self":
			json.NewEncoder(w).Encode(map[string]any{"active": r.Header.Get("PRIVATE-TOKEN") == "glpat-2"})
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/api/v4/projects/group%2Frepo/access_tokens":
			if r.Header.Get("PRIVATE-TOKEN") != "admin" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			created = append(created, body)
			json.NewEncoder(w).Encode(map[string]any{"id": len(created), "token": "glpat-" + string(rune('0'+len(created)))})
		case r.Method == http.MethodDelete:
			revoked = append(revoked, r.URL.EscapedPath())
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, r.Method+" "+r.URL.Path, http.StatusNotFound)
		}
	})

	home := t.TempDir()
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	p := &project{workspace: "/src/repo", home: home, settings: settings{GitLab: []gitlabConfig{{Host: host, TokenCommand: "echo admin"}}}}
	f := forge{kind: gitlab, host: host, project: "group/repo"}

	// The first token is created, then found revoked and replaced.
	for i, want := range []string{"glpat-1", "glpat-2", "glpat-2"} {
		tok, err := p.repoToken(f, renewBefore[gitlab])
		if err != nil {
			t.Fatal(err)
		}
		if tok.Token != want {
			t.Errorf("call %d: token %q, want %q", i, tok.Token, want)
		}
	}
	if len(created) != 2 {
		t.Fatalf("created %d tokens, want 2", len(created))
	}
	if created[0]["access_level"] != float64(30) || !reflect.DeepEqual(created[0]["scopes"], []any{"api", "write_repository"}) {
		t.Errorf("token request = %v", created[0])
	}
	if !reflect.DeepEqual(revoked, []string{"/api/v4/projects/group%2Frepo/access_tokens/1"}) {
		t.Errorf("revoked = %v", revoked)
	}

	p.revokeTokens()
	if p.loadToken(f) != nil {
		t.Error("token still cached after revokeTokens")
	}
}

func TestGithubToken(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	keyFile := filepath.Join(home, "app.pem")
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}

	var request map[string]any
	expires := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	host := fakeForge(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ey") {
			http.Error(w, "no jwt", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/v3/repos/owner/repo/installation":
			json.NewEncoder(w).Encode(map[string]any{"id": 7})
		case "/api/v3/app/installations/7/access_tokens":
			json.NewDecoder(r.Body).Decode(&request)
			json.NewEncoder(w).Encode(map[string]any{"token": "ghs_x", "expires_at": expires})
		default:
			http.NotFound(w, r)
		}
	})

	p := &project{home: home, settings: settings{GitHub: []githubConfig{{Host: host, AppID: "1", PrivateKeyFile: keyFile}}}}
	tok, err := p.githubToken(forge{kind: github, host: host, project: "owner/repo"})
	if err != nil {
		t.Fatal(err)
	}
	if tok.Token != "ghs_x" || !tok.Expires.Equal(expires) {
		t.Errorf("token = %+v", tok)
	}
	want := map[string]any{
		"repositories": []any{"repo"},
		"permissions":  map[string]any{"contents": "write", "pull_requests": "write"},
	}
	if !reflect.DeepEqual(request, want) {
		t.Errorf("token request = %v, want %v", request, want)
	}

	// Without an App for the host no token is created.
	if tok, err := p.githubToken(forge{kind: github, host: "github.com", project: "owner/repo"}); tok != nil || err != nil {
		t.Errorf("unconfigured host: %v, %v", tok, err)
	}
}

func TestChecksumAndExtract(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range map[string]string{"gh_1_linux_amd64/LICENSE": "l", "gh_1_linux_amd64/bin/gh": "binary"} {
		tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: name, Mode: 0o755, Size: int64(len(body))})
		tw.Write([]byte(body))
	}
	tw.Close()
	gz.Close()

	got, err := extractBinary(buf.Bytes(), "bin/gh")
	if err != nil || string(got) != "binary" {
		t.Errorf("extractBinary = %q, %v", got, err)
	}
	if _, err := extractBinary(buf.Bytes(), "bin/glab"); err == nil {
		t.Error("missing binary: want an error")
	}

	sums := []byte("aaa  other.tar.gz\nbbb *gh_1_linux_amd64.tar.gz\n")
	if sum, err := checksum(sums, "gh_1_linux_amd64.tar.gz"); err != nil || sum != "bbb" {
		t.Errorf("checksum = %q, %v", sum, err)
	}
	if _, err := checksum(sums, "missing"); err == nil {
		t.Error("missing checksum: want an error")
	}
}
