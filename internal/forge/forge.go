// Package forge detects the GitHub and GitLab repositories of a workspace,
// installs their command line tools in a container and gives the container
// tokens limited to those repositories.
package forge

import (
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/antevens/devc/internal/config"
)

// Kind is the type of a forge.
type Kind string

// Forge kinds.
const (
	GitHub Kind = "github"
	GitLab Kind = "gitlab"
)

// Forge is a GitHub or GitLab host the workspace uses.
type Forge struct {
	Kind Kind
	Host string
	// Project is the repository path, empty when the forge was detected from
	// files in the workspace only.
	Project string
	// SSHURLs are the SSH URL prefixes of the host.
	SSHURLs []string
}

// Detect returns the forges of the workspace's git remotes, origin first,
// then of .github, .gitlab-ci.yml or .gitlab in the workspace. Only the first
// repository on each host is returned.
func Detect(workspace string, s config.Settings) []Forge {
	var forges []Forge
	seen := map[string]bool{}
	for _, raw := range gitRemotes(workspace) {
		r, ok := parseRemote(raw)
		if !ok || seen[r.host] {
			continue
		}
		kind := kindOf(r.host, s)
		if kind == "" {
			continue
		}
		seen[r.host] = true
		forges = append(forges, Forge{Kind: kind, Host: r.host, Project: r.path, SSHURLs: r.sshURLs()})
	}

	has := func(kind Kind) bool {
		for _, f := range forges {
			if f.Kind == kind {
				return true
			}
		}
		return false
	}
	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(workspace, name))
		return err == nil
	}
	if !has(GitHub) && exists(".github") {
		forges = append(forges, Forge{Kind: GitHub, Host: "github.com"})
	}
	if !has(GitLab) && (exists(".gitlab-ci.yml") || exists(".gitlab")) {
		forges = append(forges, Forge{Kind: GitLab, Host: "gitlab.com"})
	}
	return forges
}

// kindOf returns the forge kind of host, empty if it is not a known forge.
func kindOf(host string, s config.Settings) Kind {
	switch {
	case s.GitHubFor(host) != nil, host == "github.com":
		return GitHub
	case s.GitLabFor(host) != nil, strings.Contains(host, "gitlab"):
		return GitLab
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
	// sshPort is the port of an ssh:// URL, empty if none is given.
	sshPort string
}

// parseRemote parses HTTPS, ssh:// and scp-like [user@]host:path remote
// URLs.
func parseRemote(raw string) (remote, bool) {
	var r remote
	if !strings.Contains(raw, "://") {
		hostPath := raw[strings.LastIndex(raw, "@")+1:]
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
