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
	"strings"
	"sync"
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

// cache is the cached token of a repository and the tokens it replaced that
// are not revoked yet.
type cache struct {
	Token
	Retired []Token `json:"retired,omitempty"`
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

	mu sync.Mutex
	// installed holds the tokens last installed in each container, by ID.
	installed map[string][]*Token
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

// get returns a token for the forge's repository: the cached token, or a new
// one when there is none, it is about to expire or it has been revoked. A
// replaced token is kept in the cache as retired until revokeRetired. It
// returns nil when no credential is configured for the host. Callers hold
// the lock.
func (fc *Client) get(ctx context.Context, f Forge) (*Token, error) {
	cached, err := fc.load(fc.file(f))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if cached != nil && time.Until(cached.Expires) > renewBefore[f.Kind] {
		active, err := fc.active(ctx, &cached.Token)
		if err != nil {
			return nil, fmt.Errorf("check %s token for %s: %w", f.Host, f.Project, err)
		}
		if active {
			return &cached.Token, nil
		}
	}

	var tok *Token
	switch f.Kind {
	case GitLab:
		tok, err = fc.gitlabToken(ctx, f)
	case GitHub:
		tok, err = fc.githubToken(ctx, f)
	}
	if err != nil || tok == nil {
		return nil, err
	}
	next := cache{Token: *tok}
	if cached != nil {
		next.Retired = append(cached.Retired, cached.Token)
	}
	if err := fc.save(fc.file(f), &next); err != nil {
		return nil, fmt.Errorf("cache %s token for %s, which stays valid until %s: %w",
			f.Host, f.Project, tok.Expires.Format(time.DateTime), err)
	}
	return tok, nil
}

// Cached returns the cached token for the forge's repository, nil if there
// is none.
func (fc *Client) Cached(f Forge) *Token {
	c, err := fc.load(fc.file(f))
	if err != nil || c.Project != f.Project {
		return nil
	}
	return &c.Token
}

// usable returns the cached token for the forge's repository if it has not
// expired, nil otherwise.
func (fc *Client) usable(f Forge) *Token {
	tok := fc.Cached(f)
	if tok == nil || !time.Now().Before(tok.Expires) {
		return nil
	}
	return tok
}

// revokeRetired revokes the retired tokens of the forge's repository and
// removes them from the cache. Callers hold the lock.
func (fc *Client) revokeRetired(ctx context.Context, f Forge) error {
	c, err := fc.load(fc.file(f))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(c.Retired) == 0 {
		return nil
	}
	var kept []Token
	var errs []error
	for _, tok := range c.Retired {
		if err := fc.revoke(ctx, &tok); err != nil {
			kept = append(kept, tok)
			errs = append(errs, err)
		}
	}
	c.Retired = kept
	errs = append(errs, fc.save(fc.file(f), c))
	return errors.Join(errs...)
}

// RevokeAll revokes and forgets every cached token of the workspace.
func (fc *Client) RevokeAll(ctx context.Context) error {
	if _, err := os.Stat(fc.Dir); os.IsNotExist(err) {
		return nil
	}
	unlock, err := fc.lock()
	if err != nil {
		return err
	}
	defer unlock()

	paths, err := filepath.Glob(filepath.Join(fc.Dir, "*.json"))
	if err != nil {
		return err
	}
	var errs []error
	for _, path := range paths {
		c, err := fc.load(path)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		revoked := true
		for _, tok := range append(c.Retired, c.Token) {
			if err := fc.revoke(ctx, &tok); err != nil {
				errs = append(errs, err)
				revoked = false
			}
		}
		if revoked {
			errs = append(errs, os.Remove(path))
		}
	}
	return errors.Join(errs...)
}

// revoke revokes a token.
func (fc *Client) revoke(ctx context.Context, tok *Token) error {
	var err error
	switch tok.Kind {
	case GitLab:
		err = fc.revokeGitlabToken(ctx, tok)
	case GitHub:
		err = fc.revokeGithubToken(ctx, tok)
	}
	if err != nil {
		return fmt.Errorf("revoke %s token for %s: %w", tok.Host, tok.Project, err)
	}
	return nil
}

// active reports whether a GitLab token is still active, false when GitLab
// rejects it. GitHub tokens are not checked.
func (fc *Client) active(ctx context.Context, tok *Token) (bool, error) {
	if tok.Kind != GitLab {
		return true, nil
	}
	var self struct {
		Active bool `json:"active"`
	}
	err := fc.call(ctx, http.MethodGet, gitlabAPI(tok.Host)+"/personal_access_tokens/self",
		map[string]string{"PRIVATE-TOKEN": tok.Token}, nil, &self)
	if hasStatus(err, http.StatusUnauthorized) {
		return false, nil
	}
	return self.Active, err
}

// lock takes the workspace's token lock, so that one devc process at a time
// creates, installs and revokes its tokens. The lock is held until the
// returned function is called.
func (fc *Client) lock() (func(), error) {
	if err := os.MkdirAll(fc.Dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(fc.Dir, "lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockFile(f); err != nil {
		f.Close()
		return nil, fmt.Errorf("lock %s: %w", f.Name(), err)
	}
	return func() { f.Close() }, nil
}

func (fc *Client) file(f Forge) string {
	return filepath.Join(fc.Dir, hash(f.Host+"\n"+f.Project)+".json")
}

func (fc *Client) load(path string) (*cache, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c cache
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &c, nil
}

// save writes the cache to path, replacing it atomically.
func (fc *Client) save(path string, c *cache) error {
	if err := os.MkdirAll(fc.Dir, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(fc.Dir, strings.TrimSuffix(filepath.Base(path), ".json")+".*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
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
