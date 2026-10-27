package forge

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Project access tokens are created with the Developer role, the api and
// write_repository scopes, and a 30 day lifetime.
const (
	gitlabAccessLevel = 30
	gitlabLifetime    = 30 * 24 * time.Hour
)

var gitlabScopes = []string{"api", "write_repository"}

// gitlabAPI returns the REST API URL of a GitLab host.
func gitlabAPI(host string) string {
	return "https://" + host + "/api/v4"
}

// gitlabCredential runs the token command configured for host and returns
// its output, empty if no credential is configured.
func (fc *Client) gitlabCredential(ctx context.Context, host string) (string, error) {
	c := fc.Settings.GitLabFor(host)
	if c == nil {
		return "", nil
	}
	if c.TokenCommand == "" {
		return "", fmt.Errorf("gitlab token_command is not set for %s", host)
	}
	cmd := exec.CommandContext(ctx, "sh", "-c", c.TokenCommand)
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

// gitlabToken creates a project access token for the forge's project and
// revokes previous, if not nil. It returns nil when no credential is
// configured for the host.
func (fc *Client) gitlabToken(ctx context.Context, f Forge, previous *Token) (*Token, error) {
	credential, err := fc.gitlabCredential(ctx, f.Host)
	if err != nil || credential == "" {
		return nil, err
	}
	if previous != nil {
		// Failures are ignored.
		_ = fc.revokeGitlabToken(ctx, previous)
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
	endpoint := gitlabAPI(f.Host) + "/projects/" + url.PathEscape(f.Project) + "/access_tokens"
	if err := fc.call(ctx, http.MethodPost, endpoint, map[string]string{"PRIVATE-TOKEN": credential}, body, &created); err != nil {
		return nil, fmt.Errorf("create project access token for %s: %w", f.Project, err)
	}
	if created.Token == "" {
		return nil, errors.New("GitLab returned no token")
	}
	return &Token{Kind: GitLab, Host: f.Host, Project: f.Project, ID: created.ID, Token: created.Token, Expires: expires}, nil
}

// revokeGitlabToken revokes a project access token using the host
// credential. Tokens GitLab no longer has are treated as revoked.
func (fc *Client) revokeGitlabToken(ctx context.Context, tok *Token) error {
	credential, err := fc.gitlabCredential(ctx, tok.Host)
	if err != nil {
		return err
	}
	if credential == "" {
		return fmt.Errorf("no gitlab credential configured for %s", tok.Host)
	}
	endpoint := fmt.Sprintf("%s/projects/%s/access_tokens/%d", gitlabAPI(tok.Host), url.PathEscape(tok.Project), tok.ID)
	err = fc.call(ctx, http.MethodDelete, endpoint, map[string]string{"PRIVATE-TOKEN": credential}, nil, nil)
	if hasStatus(err, http.StatusNotFound) {
		return nil
	}
	return err
}
