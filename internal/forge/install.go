package forge

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
// config using them. It returns the installed tokens.
func (fc *Client) Install(ctx context.Context, c *container.Container, forges []Forge) ([]*Token, error) {
	var tokens []*Token
	var errs []error
	for _, f := range forges {
		if f.Project == "" {
			continue
		}
		tok, err := fc.Get(ctx, f)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if tok != nil {
			tokens = append(tokens, tok)
		}
	}
	if err := install(c, forges, tokens); err != nil {
		errs = append(errs, err)
	}
	return tokens, errors.Join(errs...)
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
			if _, err := fc.Install(ctx, c, forges); err != nil {
				log.Warn("renew tokens", "err", err)
			}
		}
	}
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

// Env returns shell lines exporting GITLAB_HOST and GITLAB_TOKEN for the
// first GitLab token.
func Env(tokens []*Token) []string {
	for _, tok := range tokens {
		if tok.Kind == GitLab {
			return []string{
				"GITLAB_HOST=" + tok.Host,
				fmt.Sprintf(`GITLAB_TOKEN="$(cat %s/%s)"`, assets.TokenDir, tok.Host),
				"export GITLAB_HOST GITLAB_TOKEN",
			}
		}
	}
	return nil
}

// install writes the tokens, credential helper, git config and gh config
// into the container and includes the git config from the system git
// config.
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

	if hosts := ghHosts(tokens); hosts != "" {
		home := archive.New(0, 0)
		home.AddFile(".config/gh/hosts.yml", []byte(hosts), 0o600)
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

// gitConfig returns git config using the credential helper and rewriting
// the SSH URLs of hosts with a token to HTTPS.
func gitConfig(forges []Forge, tokens []*Token) string {
	var b strings.Builder
	b.WriteString("[credential]\n\thelper = " + assets.CredentialHelperPath + "\n")
	for _, tok := range tokens {
		for _, f := range forges {
			if f.Host != tok.Host {
				continue
			}
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
