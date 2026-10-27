package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Forge kinds.
const (
	github = "github"
	gitlab = "gitlab"
)

// tokenDir holds the per-repository tokens in the container.
const tokenDir = devcDir + "/tokens"

// settings is the devc config, ~/.config/devc/config.json.
type settings struct {
	// RemoteControl makes sessions started with devc claude available in the
	// Claude apps.
	RemoteControl bool           `json:"remote_control"`
	GitLab        []gitlabConfig `json:"gitlab"`
	GitHub        []githubConfig `json:"github"`
}

// gitlabConfig holds the credential used on the host to create per-project
// access tokens on a GitLab instance.
type gitlabConfig struct {
	Host string `json:"host"`
	// TokenCommand prints a token with the api scope and at least the
	// Maintainer role on the projects, e.g. "pass show gitlab/devc".
	TokenCommand string `json:"token_command"`
}

// githubConfig holds the GitHub App used on the host to create per-repository
// installation tokens.
type githubConfig struct {
	Host           string `json:"host"`
	AppID          string `json:"app_id"`
	PrivateKeyFile string `json:"private_key_file"`
}

// loadSettings reads the devc config, which is optional.
func loadSettings(home string) (settings, error) {
	var s settings
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		dir = filepath.Join(home, ".config")
	}
	path := filepath.Join(dir, "devc", "config.json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return s, fmt.Errorf("parse %s: %w", path, err)
	}
	return s, nil
}

// forge is a GitHub or GitLab host the workspace uses.
type forge struct {
	kind string
	host string
	// project is the repository path, empty when the forge was only detected
	// from files in the workspace.
	project string
	// sshURLs are the SSH forms of the host's URLs, rewritten to HTTPS so git
	// uses the token.
	sshURLs []string
}

// detectForges returns the forges of the workspace's git remotes, origin
// first, and of forge files such as .github or .gitlab-ci.yml. Only the first
// repository on each host is used.
func detectForges(workspace string, s settings) []forge {
	var forges []forge
	seen := map[string]bool{}
	for _, raw := range gitRemotes(workspace) {
		r, ok := parseRemote(raw)
		if !ok || seen[r.host] {
			continue
		}
		kind := s.kind(r.host)
		if kind == "" {
			continue
		}
		seen[r.host] = true
		forges = append(forges, forge{kind: kind, host: r.host, project: r.path, sshURLs: r.sshURLs()})
	}

	has := func(kind string) bool {
		for _, f := range forges {
			if f.kind == kind {
				return true
			}
		}
		return false
	}
	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(workspace, name))
		return err == nil
	}
	if !has(github) && exists(".github") {
		forges = append(forges, forge{kind: github, host: "github.com"})
	}
	if !has(gitlab) && (exists(".gitlab-ci.yml") || exists(".gitlab")) {
		forges = append(forges, forge{kind: gitlab, host: "gitlab.com"})
	}
	return forges
}

// kind returns the forge kind of host, empty if it is not a known forge.
func (s settings) kind(host string) string {
	for _, c := range s.GitHub {
		if c.Host == host {
			return github
		}
	}
	for _, c := range s.GitLab {
		if c.Host == host {
			return gitlab
		}
	}
	switch {
	case host == "github.com":
		return github
	case strings.Contains(host, "gitlab"):
		return gitlab
	}
	return ""
}

// gitRemotes returns the URLs of the workspace's git remotes, origin first.
func gitRemotes(workspace string) []string {
	out, err := exec.Command("git", "-C", workspace, "remote").Output()
	if err != nil {
		return nil
	}
	names := strings.Fields(string(out))
	for i, name := range names {
		if name == "origin" {
			names[0], names[i] = names[i], names[0]
		}
	}
	var urls []string
	for _, name := range names {
		out, err := exec.Command("git", "-C", workspace, "remote", "get-url", name).Output()
		if err == nil {
			urls = append(urls, strings.TrimSpace(string(out)))
		}
	}
	return urls
}

// remote is a parsed git remote URL.
type remote struct {
	host, path string
	// sshPort is the port of an ssh:// URL, empty if none was given.
	sshPort string
}

// parseRemote parses HTTPS, ssh:// and scp-like git@host:path remote URLs.
func parseRemote(raw string) (remote, bool) {
	var r remote
	if !strings.Contains(raw, "://") {
		// scp-like syntax: [user@]host:path
		at := strings.LastIndex(raw, "@")
		hostPath := raw[at+1:]
		host, path, ok := strings.Cut(hostPath, ":")
		if !ok || strings.Contains(host, "/") {
			return r, false
		}
		r.host, r.path = host, path
	} else {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			return r, false
		}
		switch u.Scheme {
		case "https", "http":
		case "ssh", "git+ssh":
			r.sshPort = u.Port()
		default:
			return r, false
		}
		r.host, r.path = u.Hostname(), u.Path
	}
	r.path = strings.TrimSuffix(strings.Trim(r.path, "/"), ".git")
	if r.path == "" {
		return r, false
	}
	return r, true
}

// sshURLs returns the SSH URL prefixes of the remote's host.
func (r remote) sshURLs() []string {
	urls := []string{"git@" + r.host + ":", "ssh://git@" + r.host + "/"}
	if r.sshPort != "" {
		urls = append(urls, "ssh://git@"+r.host+":"+r.sshPort+"/")
	}
	return urls
}

// token is a per-repository token, cached on the host so it is reused
// across sessions and can be revoked by devc rm.
type token struct {
	Kind    string    `json:"kind"`
	Host    string    `json:"host"`
	Project string    `json:"project"`
	ID      int       `json:"id,omitempty"`
	Token   string    `json:"token"`
	Expires time.Time `json:"expires"`
}

// tokenDir returns the host directory caching the workspace's tokens.
func (p *project) tokenDir() string {
	return filepath.Join(stateDir(p.home), "tokens", hash([]byte(p.workspace)))
}

// tokenFile returns the host file caching the token for a forge.
func (p *project) tokenFile(f forge) string {
	return filepath.Join(p.tokenDir(), hash([]byte(f.host+"\n"+f.project))+".json")
}

func (p *project) loadToken(f forge) *token {
	data, err := os.ReadFile(p.tokenFile(f))
	if err != nil {
		return nil
	}
	var t token
	if json.Unmarshal(data, &t) != nil || t.Project != f.project {
		return nil
	}
	return &t
}

func (p *project) saveToken(f forge, t *token) error {
	path := p.tokenFile(f)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(t)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// repoToken returns a valid token for the forge's repository, creating one
// with the host credential when there is no cached token or it expires
// within renewBefore. It returns nil when no credential is configured.
func (p *project) repoToken(f forge, renewBefore time.Duration) (*token, error) {
	cached := p.loadToken(f)
	if cached != nil && time.Until(cached.Expires) > renewBefore && p.valid(cached) {
		return cached, nil
	}
	var t *token
	var err error
	switch f.kind {
	case gitlab:
		t, err = p.gitlabToken(f, cached)
	case github:
		t, err = p.githubToken(f)
	}
	if err != nil || t == nil {
		return nil, err
	}
	if err := p.saveToken(f, t); err != nil {
		return nil, err
	}
	return t, nil
}

// valid reports whether a GitLab token is still active, it may have been
// revoked in the web UI. GitHub tokens are short lived and not checked.
func (p *project) valid(t *token) bool {
	if t.Kind != gitlab {
		return true
	}
	var self struct {
		Active bool `json:"active"`
	}
	err := apiCall(http.MethodGet, gitlabAPI(t.Host)+"/personal_access_tokens/self",
		map[string]string{"PRIVATE-TOKEN": t.Token}, nil, &self)
	return err == nil && self.Active
}

// revokeTokens revokes and forgets all of the workspace's tokens, including
// those of remotes removed since they were created.
func (p *project) revokeTokens() {
	entries, _ := os.ReadDir(p.tokenDir())
	for _, e := range entries {
		path := filepath.Join(p.tokenDir(), e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		t := &token{}
		if json.Unmarshal(data, t) != nil {
			continue
		}
		switch t.Kind {
		case gitlab:
			err = p.revokeGitlabToken(t)
		case github:
			err = revokeGithubToken(t)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "devc: revoke %s token for %s: %v\n", t.Host, t.Project, err)
			continue
		}
		os.Remove(path)
	}
}

// httpClient is used for all forge API calls.
var httpClient = &http.Client{Timeout: 30 * time.Second}

// apiCall sends a JSON request and decodes the JSON response into out, if
// not nil.
func apiCall(method, endpoint string, header map[string]string, body, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, endpoint, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s: %s: %s", method, endpoint, resp.Status, bytes.TrimSpace(data))
	}
	if out == nil || len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, out)
}
