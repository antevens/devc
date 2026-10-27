package forge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/antevens/devc/internal/config"
	"github.com/antevens/devc/internal/xdg"
)

// renewBefore is how long before expiry a token is replaced.
var renewBefore = map[Kind]time.Duration{
	GitHub: 15 * time.Minute,
	GitLab: 7 * 24 * time.Hour,
}

// Token is a token limited to one repository.
type Token struct {
	Kind    Kind      `json:"kind"`
	Host    string    `json:"host"`
	Project string    `json:"project"`
	ID      int       `json:"id,omitempty"`
	Token   string    `json:"token"`
	Expires time.Time `json:"expires"`
}

// Client installs forge tools in containers and creates, caches and revokes
// the per-repository tokens of a workspace, using the credentials in the
// devc settings.
type Client struct {
	Settings config.Settings
	// Home expands ~/ in configured paths.
	Home string
	// Dir caches the workspace's tokens, one file per repository.
	Dir  string
	HTTP *http.Client
}

// NewClient returns the forge client of a workspace.
func NewClient(s config.Settings, home, workspace string) *Client {
	return &Client{
		Settings: s,
		Home:     home,
		Dir:      filepath.Join(xdg.StateDir(home), "tokens", hash(workspace)),
		HTTP:     &http.Client{Timeout: 30 * time.Second},
	}
}

// Get returns a token for the forge's repository: the cached token, or a new
// one when there is none, it is about to expire or it has been revoked. It
// returns nil when no credential is configured for the host.
func (fc *Client) Get(ctx context.Context, f Forge) (*Token, error) {
	cached := fc.Cached(f)
	if cached != nil && time.Until(cached.Expires) > renewBefore[f.Kind] && fc.active(ctx, cached) {
		return cached, nil
	}
	var tok *Token
	var err error
	switch f.Kind {
	case GitLab:
		tok, err = fc.gitlabToken(ctx, f, cached)
	case GitHub:
		tok, err = fc.githubToken(ctx, f)
	}
	if err != nil || tok == nil {
		return nil, err
	}
	if err := fc.save(f, tok); err != nil {
		return nil, err
	}
	return tok, nil
}

// Cached returns the cached token for the forge's repository, nil if there
// is none.
func (fc *Client) Cached(f Forge) *Token {
	data, err := os.ReadFile(fc.file(f))
	if err != nil {
		return nil
	}
	var tok Token
	if json.Unmarshal(data, &tok) != nil || tok.Project != f.Project {
		return nil
	}
	return &tok
}

// RevokeAll revokes and forgets every cached token of the workspace.
func (fc *Client) RevokeAll(ctx context.Context) error {
	entries, err := os.ReadDir(fc.Dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var errs []error
	for _, e := range entries {
		path := filepath.Join(fc.Dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		var tok Token
		if err := json.Unmarshal(data, &tok); err != nil {
			errs = append(errs, fmt.Errorf("parse %s: %w", path, err))
			continue
		}
		switch tok.Kind {
		case GitLab:
			err = fc.revokeGitlabToken(ctx, &tok)
		case GitHub:
			err = fc.revokeGithubToken(ctx, &tok)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("revoke %s token for %s: %w", tok.Host, tok.Project, err))
			continue
		}
		if err := os.Remove(path); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// active reports whether a GitLab token is still active. GitHub tokens are
// not checked.
func (fc *Client) active(ctx context.Context, tok *Token) bool {
	if tok.Kind != GitLab {
		return true
	}
	var self struct {
		Active bool `json:"active"`
	}
	err := fc.call(ctx, http.MethodGet, gitlabAPI(tok.Host)+"/personal_access_tokens/self",
		map[string]string{"PRIVATE-TOKEN": tok.Token}, nil, &self)
	return err == nil && self.Active
}

func (fc *Client) file(f Forge) string {
	return filepath.Join(fc.Dir, hash(f.Host+"\n"+f.Project)+".json")
}

func (fc *Client) save(f Forge, tok *Token) error {
	if err := os.MkdirAll(fc.Dir, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(tok)
	if err != nil {
		return err
	}
	return os.WriteFile(fc.file(f), data, 0o600)
}

// statusError is an HTTP error response from a forge API.
type statusError struct {
	method, url string
	status      int
	body        []byte
}

func (e *statusError) Error() string {
	return fmt.Sprintf("%s %s: %d %s: %s", e.method, e.url, e.status, http.StatusText(e.status), e.body)
}

// hasStatus reports whether err is an HTTP error response with the status.
func hasStatus(err error, status int) bool {
	var se *statusError
	return errors.As(err, &se) && se.status == status
}

// call sends a JSON request and decodes the JSON response into out, if not
// nil.
func (fc *Client) call(ctx context.Context, method, url string, header map[string]string, body, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
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
	resp, err := fc.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return &statusError{method: method, url: url, status: resp.StatusCode, body: bytes.TrimSpace(data)}
	}
	if out == nil || len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, out)
}

func hash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
