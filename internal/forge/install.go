package forge

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/antevens/devc/internal/archive"
	"github.com/antevens/devc/internal/assets"
	"github.com/antevens/devc/internal/container"
)

// renewInterval is how often Renew checks the tokens.
const renewInterval = 5 * time.Minute

// Install gets a token for each forge with a project and installs them in
// the container with the git credential helper, the git config and the gh
// and glab configs using them, then revokes the tokens they replace.
func (fc *Client) Install(ctx context.Context, c *container.Container, forges []Forge) error {
	return fc.update(ctx, c, forges, true)
}

// Renew installs renewed tokens every renewInterval until ctx is done. It
// returns at once when no forge has a project and a configured credential.
// Errors are logged.
func (fc *Client) Renew(ctx context.Context, c *container.Container, forges []Forge, log *slog.Logger) {
	if !fc.configured(forges) {
		return
	}
	ticker := time.NewTicker(renewInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := fc.update(ctx, c, forges, false); err != nil {
				log.Warn("renew tokens", "err", err)
			}
		}
	}
}

// update gets the tokens and installs them in the container, unless force is
// false and they are the tokens last installed there. Once the container
// has the tokens, the tokens they replaced are revoked.
func (fc *Client) update(ctx context.Context, c *container.Container, forges []Forge, force bool) error {
	unlock, err := fc.lock()
	if err != nil {
		return err
	}
	defer unlock()

	tokens, err := fc.tokens(ctx, forges)
	errs := []error{err}
	fc.mu.Lock()
	defer fc.mu.Unlock()
	if force || !slices.EqualFunc(tokens, fc.installed[c.ID], sameToken) {
		delete(fc.installed, c.ID)
		if err := install(c, forges, tokens); err != nil {
			return errors.Join(append(errs, err)...)
		}
		if fc.installed == nil {
			fc.installed = map[string][]*Token{}
		}
		fc.installed[c.ID] = tokens
	}
	for _, f := range forges {
		if f.Project != "" {
			errs = append(errs, fc.revokeRetired(ctx, f))
		}
	}
	return errors.Join(errs...)
}

func sameToken(a, b *Token) bool {
	return a.Kind == b.Kind && a.Host == b.Host && a.Token == b.Token
}

// tokens returns a token for each forge with a project and a credential for
// its host. When getting a token fails, the cached token is used until it
// expires.
func (fc *Client) tokens(ctx context.Context, forges []Forge) ([]*Token, error) {
	var tokens []*Token
	var errs []error
	for _, f := range forges {
		if f.Project == "" {
			continue
		}
		tok, err := fc.get(ctx, f)
		if err != nil {
			errs = append(errs, err)
			tok = fc.usable(f)
		}
		if tok != nil {
			tokens = append(tokens, tok)
		}
	}
	return tokens, errors.Join(errs...)
}

// configured reports whether any forge has a project and a credential for
// its host.
func (fc *Client) configured(forges []Forge) bool {
	for _, f := range forges {
		if f.Project != "" && (fc.Settings.GitHubFor(f.Host) != nil || fc.Settings.GitLabFor(f.Host) != nil) {
			return true
		}
	}
	return false
}

// install writes the tokens, credential helper, git config, gh config and
// glab config into the container and includes the git config from the
// system git config.
func install(c *container.Container, forges []Forge, tokens []*Token) error {
	root := archive.New(0, c.GID)
	root.AddDir(archive.Entry(assets.Dir), 0o755)
	root.AddFile(archive.Entry(assets.CredentialHelperPath), assets.CredentialHelper, 0o755)
	root.AddFile(archive.Entry(assets.GitConfigPath), []byte(gitConfig(forges, tokens)), 0o644)
	if len(tokens) > 0 {
		root.AddDir(archive.Entry(assets.TokenDir), 0o750)
		for _, tok := range tokens {
			root.AddFile(archive.Entry(assets.TokenDir+"/"+tok.Host), []byte(tok.Token), 0o640)
		}
	}
	data, err := root.Bytes()
	if err != nil {
		return err
	}
	if err := c.ExtractRoot(data); err != nil {
		return err
	}

	if hosts, glab := ghHosts(tokens), glabConfig(tokens); hosts != "" || glab != "" {
		home := archive.New(0, 0)
		if hosts != "" {
			home.AddFile(".config/gh/hosts.yml", []byte(hosts), 0o600)
		}
		if glab != "" {
			home.AddFile(".config/glab-cli/config.yml", []byte(glab), 0o600)
		}
		data, err := home.Bytes()
		if err != nil {
			return err
		}
		if err := c.ExtractHome(data); err != nil {
			return err
		}
	}
	return includeGitConfig(c)
}

// includeGitConfig adds the devc git config to the system git config's
// include.path, unless it is there or git is not installed.
func includeGitConfig(c *container.Container) error {
	script := `command -v git >/dev/null || exit 0
git config --system --get-all include.path | grep -qxF "$1" || git config --system --add include.path "$1"`
	if _, err := c.Output("root", "sh", "-c", script, "sh", assets.GitConfigPath); err != nil {
		return fmt.Errorf("include devc git config: %w", err)
	}
	return nil
}

// gitConfig returns git config using the credential helper for the hosts
// with a token and rewriting their SSH URLs to HTTPS.
func gitConfig(forges []Forge, tokens []*Token) string {
	var b strings.Builder
	for _, tok := range tokens {
		for _, f := range forges {
			if f.Host != tok.Host {
				continue
			}
			fmt.Fprintf(&b, "[credential \"https://%s\"]\n\thelper = %s\n", f.Host, assets.CredentialHelperPath)
			fmt.Fprintf(&b, "[url \"https://%s/\"]\n", f.Host)
			for _, ssh := range f.SSHURLs {
				fmt.Fprintf(&b, "\tinsteadOf = %s\n", ssh)
			}
		}
	}
	return b.String()
}

// ghHosts returns the gh hosts.yml for the GitHub tokens, empty if there
// are none.
func ghHosts(tokens []*Token) string {
	var b strings.Builder
	for _, tok := range tokens {
		if tok.Kind == GitHub {
			fmt.Fprintf(&b, "%s:\n    oauth_token: %s\n    git_protocol: https\n    user: x-access-token\n", tok.Host, tok.Token)
		}
	}
	return b.String()
}

// glabConfig returns the glab config.yml for the GitLab tokens, with the
// first token's host as default host, empty if there are none.
func glabConfig(tokens []*Token) string {
	var b strings.Builder
	for _, tok := range tokens {
		if tok.Kind != GitLab {
			continue
		}
		if b.Len() == 0 {
			fmt.Fprintf(&b, "git_protocol: https\nhost: %s\nhosts:\n", tok.Host)
		}
		fmt.Fprintf(&b, "    %s:\n        api_host: %s\n        api_protocol: https\n        token: %s\n", tok.Host, tok.Host, tok.Token)
	}
	return b.String()
}
