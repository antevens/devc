// Package assets holds the files devc installs in containers and the paths
// they are installed at.
package assets

import _ "embed"

// Paths in the container. Dir is owned by root.
const (
	Dir = "/opt/devc"
	// ClaudeSettings is the user's Claude settings with the devc hooks added.
	ClaudeSettings = Dir + "/claude-settings.json"
	// ClaudePromptPath is appended to claude's system prompt.
	ClaudePromptPath = Dir + "/claude-prompt.md"
	// LogHookPath appends every claude response to .claude.log.
	LogHookPath = Dir + "/log-response.sh"
	// LauncherPath runs session commands with the environment from EnvPath.
	LauncherPath = Dir + "/run"
	// EnvPath is the session environment, sourced when a session or login
	// shell starts.
	EnvPath = Dir + "/env"
	// TokenDir holds the per-repository tokens, one file per host.
	TokenDir = Dir + "/tokens"
	// CredentialHelperPath gives git the per-repository tokens.
	CredentialHelperPath = Dir + "/git-credential-devc"
	// GitConfigPath is included from the system git config.
	GitConfigPath = Dir + "/gitconfig"
	// ProfilePath sources the environment and defines the claude function
	// for login shells.
	ProfilePath = "/etc/profile.d/devc.sh"
)

// File contents.
var (
	//go:embed files/claude-prompt.md
	ClaudePrompt []byte
	//go:embed files/log-response.sh
	LogHook []byte
	//go:embed files/run.sh
	Launcher []byte
	//go:embed files/git-credential-devc.sh
	CredentialHelper []byte
	//go:embed files/devc-profile.sh
	Profile []byte
)
