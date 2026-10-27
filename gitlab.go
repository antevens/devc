package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

// GitLab project access tokens belong to a bot user that is a member of one
// project only. The Developer role with the api and write_repository scopes
// allows pushing to unprotected branches and creating, reviewing and
// commenting on merge requests. GitLab has no narrower scope for merge
// requests, so the api scope also allows the project's other Developer level
// API calls, such as issues and pipelines.
const (
	gitlabAccessLevel = 30
	gitlabLifetime    = 30 * 24 * time.Hour
)

var gitlabScopes = []string{"api", "write_repository"}

// gitlabAPI returns the REST API URL of a GitLab host.
func gitlabAPI(host string) string {
	return "https://" + host + "/api/v4"
}

// gitlabCredential runs the token command configured for host, returning an
// empty token if none is.
func (s settings) gitlabCredential(host string) (string, error) {
	for _, c := range s.GitLab {
		if c.Host != host {
			continue
		}
		if c.TokenCommand == "" {
			return "", fmt.Errorf("gitlab token_command is not set for %s", host)
		}
		cmd := exec.Command("sh", "-c", c.TokenCommand)
		cmd.Stderr = os.Stderr
		out, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("gitlab token_command for %s: %w", host, err)
		}
		credential := strings.TrimSpace(string(out))
		if credential == "" {
			return "", fmt.Errorf("gitlab token_command for %s printed nothing", host)
		}
		return credential, nil
	}
	return "", nil
}

// gitlabToken creates a project access token for the forge's project and
// revokes the previous one, if any.
func (p *project) gitlabToken(f forge, previous *token) (*token, error) {
	credential, err := p.settings.gitlabCredential(f.host)
	if err != nil || credential == "" {
		return nil, err
	}
	if previous != nil {
		// The previous token is expiring or already revoked, ignore failures.
		_ = p.revokeGitlabToken(previous)
	}

	hostname, _ := os.Hostname()
	expires := time.Now().Add(gitlabLifetime)
	body := map[string]any{
		"name":         "devc " + hostname,
		"scopes":       gitlabScopes,
		"access_level": gitlabAccessLevel,
		"expires_at":   expires.Format(time.DateOnly),
	}
	var created struct {
		ID    int    `json:"id"`
		Token string `json:"token"`
	}
	endpoint := gitlabAPI(f.host) + "/projects/" + url.PathEscape(f.project) + "/access_tokens"
	if err := apiCall(http.MethodPost, endpoint, map[string]string{"PRIVATE-TOKEN": credential}, body, &created); err != nil {
		return nil, fmt.Errorf("create project access token for %s: %w", f.project, err)
	}
	if created.Token == "" {
		return nil, errors.New("GitLab returned no token")
	}
	return &token{Kind: gitlab, Host: f.host, Project: f.project, ID: created.ID, Token: created.Token, Expires: expires}, nil
}

// revokeGitlabToken revokes a project access token with the host credential.
func (p *project) revokeGitlabToken(t *token) error {
	credential, err := p.settings.gitlabCredential(t.Host)
	if err != nil {
		return err
	}
	if credential == "" {
		return fmt.Errorf("no gitlab credential configured for %s", t.Host)
	}
	endpoint := fmt.Sprintf("%s/projects/%s/access_tokens/%d", gitlabAPI(t.Host), url.PathEscape(t.Project), t.ID)
	err = apiCall(http.MethodDelete, endpoint, map[string]string{"PRIVATE-TOKEN": credential}, nil, nil)
	if err != nil && strings.Contains(err.Error(), "404") {
		return nil
	}
	return err
}
