package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	// launcherPath runs session commands with the environment from envPath.
	launcherPath = devcDir + "/run"
	// envPath exports the per-repository tokens, read when a session or
	// login shell starts.
	envPath = devcDir + "/env"
	// credentialHelperPath gives git the per-repository tokens.
	credentialHelperPath = devcDir + "/git-credential-devc"
	// gitConfigPath is included from the container's system git config.
	gitConfigPath = devcDir + "/gitconfig"
)

const launcherScript = `#!/bin/sh
#
# Installed by devc: run a command with the per-repository tokens exported.

if [ -r ` + envPath + ` ]; then
  # shellcheck source=/dev/null
  . ` + envPath + `
fi
exec "$@"
`

const credentialHelperScript = `#!/bin/sh
#
# Installed by devc: git credential helper returning the per-repository token
# for a host, nothing for hosts without one.

[ "$1" = "get" ] || exit 0

host=""
while IFS= read -r line; do
  case "${line}" in
    host=*) host="${line#host=}" ;;
    "") break ;;
    *) ;;
  esac
done

token_file="` + tokenDir + `/${host}"
[ -n "${host}" ] && [ -r "${token_file}" ] || exit 0

printf 'username=x-access-token\npassword=%s\n' "$(cat "${token_file}")"
`

// renewBefore is how long before expiry tokens are replaced. GitHub tokens
// last an hour, so they are renewed during a session, GitLab tokens a month.
var renewBefore = map[string]time.Duration{
	github: 15 * time.Minute,
	gitlab: 7 * 24 * time.Hour,
}

// provision copies the shared files into the container, installs the tools
// for the workspace's forges and gives it per-repository tokens. Failures to
// set up the forges are reported without stopping the session.
func (p *project) provision(c *container) error {
	if err := c.sync(p.home); err != nil {
		return err
	}
	forges := detectForges(p.workspace, p.settings)
	if err := c.installForges(p, forges, true); err != nil {
		fmt.Fprintf(os.Stderr, "devc: %v\n", err)
	}
	return nil
}

// installForges installs the forges' tools, when tools is set, and the
// per-repository tokens with the git and CLI config using them.
func (c *container) installForges(p *project, forges []forge, tools bool) error {
	if err := c.lookupUser(); err != nil {
		return err
	}
	var errs []string
	installed := map[string]bool{}
	var tokens []*token
	for _, f := range forges {
		if tools && !installed[f.kind] {
			installed[f.kind] = true
			if err := c.installTool(cliTools[f.kind], p.home); err != nil {
				errs = append(errs, fmt.Sprintf("install %s: %v", cliTools[f.kind].name, err))
			}
		}
		if f.project == "" {
			continue
		}
		t, err := p.repoToken(f, renewBefore[f.kind])
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		if t != nil {
			tokens = append(tokens, t)
		}
	}

	if err := c.installTokens(p.name(), forges, tokens); err != nil {
		errs = append(errs, err.Error())
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "\ndevc: "))
	}
	return nil
}

// installTokens writes the tokens, the environment exporting them, the git
// credential helper and the gh config into the container. The launcher is
// installed even without tokens, as sessions always start through it.
func (c *container) installTokens(name string, forges []forge, tokens []*token) error {
	root := newArchive(0, c.gid)
	root.addDir(relative(devcDir), 0o755, time.Now())
	root.addFile(relative(launcherPath), []byte(launcherScript), 0o755)
	root.addFile(relative(credentialHelperPath), []byte(credentialHelperScript), 0o755)
	root.addFile(relative(envPath), []byte(envScript(name, tokens)), 0o644)
	root.addFile(relative(gitConfigPath), []byte(gitConfig(forges, tokens)), 0o644)
	if len(tokens) > 0 {
		// Readable by the remote user's group only.
		root.addDir(relative(tokenDir), 0o750, time.Now())
		for _, t := range tokens {
			root.addFile(relative(tokenDir+"/"+t.Host), []byte(t.Token), 0o640)
		}
	}
	data, err := root.close()
	if err != nil {
		return err
	}
	if err := c.extract("root", "/", data); err != nil {
		return err
	}

	if hosts := ghHosts(tokens); hosts != "" {
		user := newArchive(0, 0)
		user.addFile(".config/gh/hosts.yml", []byte(hosts), 0o600)
		data, err := user.close()
		if err != nil {
			return err
		}
		if err := c.extract(c.user, c.home, data); err != nil {
			return err
		}
	}
	return c.includeGitConfig()
}

// includeGitConfig includes the devc git config from the container's system
// git config, which git reads before the user's, so the credential helper
// runs first. Containers without git are left alone.
func (c *container) includeGitConfig() error {
	script := `command -v git >/dev/null || exit 0
git config --system --get-all include.path | grep -qxF "$1" || git config --system --add include.path "$1"`
	out, err := exec.Command("docker", "exec", "--user", "root", c.id, "sh", "-c", script, "sh", gitConfigPath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("include devc git config: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// envScript names Remote Control sessions after the workspace, rather than
// the container's random hostname, and exports the GitLab token for glab.
// GitHub tokens expire within the hour, so gh reads them from its hosts.yml,
// which devc renews, rather than from a variable that would go stale.
func envScript(name string, tokens []*token) string {
	var b strings.Builder
	b.WriteString("# Installed by devc.\n")
	fmt.Fprintf(&b, "CLAUDE_REMOTE_CONTROL_SESSION_NAME_PREFIX=%s\nexport CLAUDE_REMOTE_CONTROL_SESSION_NAME_PREFIX\n", shellQuote(name))
	for _, t := range tokens {
		if t.Kind != gitlab {
			continue
		}
		fmt.Fprintf(&b, "GITLAB_HOST=%s\nGITLAB_TOKEN=\"$(cat %s/%s)\"\nexport GITLAB_HOST GITLAB_TOKEN\n", t.Host, tokenDir, t.Host)
		break
	}
	return b.String()
}

// gitConfig uses the credential helper and rewrites SSH remotes of hosts
// with a token to HTTPS, as SSH keys are not shared with the container.
func gitConfig(forges []forge, tokens []*token) string {
	var b strings.Builder
	b.WriteString("# Installed by devc.\n[credential]\n\thelper = " + credentialHelperPath + "\n")
	for _, t := range tokens {
		for _, f := range forges {
			if f.host != t.Host {
				continue
			}
			fmt.Fprintf(&b, "[url \"https://%s/\"]\n", f.host)
			for _, ssh := range f.sshURLs {
				fmt.Fprintf(&b, "\tinsteadOf = %s\n", ssh)
			}
		}
	}
	return b.String()
}

// ghHosts returns the gh hosts.yml for the GitHub tokens, empty if there
// are none.
func ghHosts(tokens []*token) string {
	var b strings.Builder
	for _, t := range tokens {
		if t.Kind == github {
			fmt.Fprintf(&b, "%s:\n    oauth_token: %s\n    git_protocol: https\n    user: x-access-token\n", t.Host, t.Token)
		}
	}
	return b.String()
}

// renewTokens renews expiring tokens while a session runs, every few minutes
// until stop is closed. Errors are logged, as the session owns the terminal.
func (p *project) renewTokens(c *container, stop <-chan struct{}) {
	forges := detectForges(p.workspace, p.settings)
	hasToken := false
	for _, f := range forges {
		hasToken = hasToken || p.loadToken(f) != nil
	}
	if !hasToken {
		return
	}
	logFile, err := openLog(p.home)
	if err != nil {
		return
	}
	defer logFile.Close()

	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			if err := c.installForges(p, forges, false); err != nil {
				fmt.Fprintf(logFile, "%s renew tokens: %v\n", time.Now().Format(time.RFC3339), err)
			}
		}
	}
}

// shellQuote quotes s for a POSIX shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
