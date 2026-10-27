package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Claude Code reads settings from, highest precedence first: managed
// settings, command line arguments, local project, shared project and user
// settings. Copying the user's settings into the container's home directory
// would rank them below the project's, so devc passes them again with
// --settings, which ranks above both project scopes. Scalar settings from the
// user win, while lists such as permission rules and hooks are merged across
// every scope, so the project's own additions still apply.
//
// CLAUDE.md files have no precedence order of their own, the prompt appended
// with --append-system-prompt-file tells claude to prefer the user's.
const (
	// devcDir holds the files devc installs in the container. It is owned by
	// root so claude in the container cannot change them.
	devcDir = "/opt/devc"
	// overlayPath is the user's settings with the devc hooks added.
	overlayPath = devcDir + "/claude-settings.json"
	// promptPath is appended to the system prompt.
	promptPath = devcDir + "/claude-prompt.md"
	// logHookPath appends every response to .claude.log in the workspace.
	logHookPath = devcDir + "/log-response.sh"
	// profilePath adds the flags to claude started from a login shell.
	profilePath = "/etc/profile.d/devc-claude.sh"
)

// claudeFlags are passed to every claude session started by devc.
var claudeFlags = []string{"--settings", overlayPath, "--append-system-prompt-file", promptPath}

// claudeCommand returns the command running claude with args. With
// remote_control set in the devc config, interactive sessions are made
// available in the Claude apps, named after the workspace.
func (p *project) claudeCommand(args []string) []string {
	cmd := []string{"claude"}
	if len(args) > 0 && args[0] == "remote-control" {
		// Server mode refuses --settings, it uses the usual precedence.
		return append(cmd, args...)
	}
	cmd = append(cmd, claudeFlags...)
	if p.settings.RemoteControl && !hasAnyArg(args, "--remote-control", "--rc", "-p", "--print") {
		cmd = append(cmd, "--remote-control", p.name())
	}
	return append(cmd, args...)
}

// remoteCommand returns the command running claude in Remote Control server
// mode, so new sessions in the workspace can be started from the Claude apps.
func (p *project) remoteCommand(args []string) []string {
	cmd := []string{"claude", "remote-control"}
	if !hasAnyArg(args, "--name") {
		cmd = append(cmd, "--name", p.name())
	}
	return append(cmd, args...)
}

// hasAnyArg reports whether args contain any of the flags, alone or with an
// =value.
func hasAnyArg(args []string, flags ...string) bool {
	for _, a := range args {
		name, _, _ := strings.Cut(a, "=")
		for _, f := range flags {
			if name == f {
				return true
			}
		}
	}
	return false
}

const precedencePrompt = `# Instruction precedence

The user's personal instructions in ~/.claude/CLAUDE.md, and the files it
imports, take precedence over the project's instructions in CLAUDE.md,
CLAUDE.local.md and .claude/rules wherever they conflict. Follow the
project's instructions for everything the user's instructions do not cover.
`

const logHookScript = `#!/bin/bash
#
# Append the last response of a Claude Code session to .claude.log in its
# working directory. Installed by devc as a Stop hook.

set -o errexit
set -o nounset
set -o pipefail

command -v jq >/dev/null || exit 0

input="$(cat)"
cwd="$(jq -r '.cwd // empty' <<<"${input}")"
response="$(jq -r '.last_assistant_message // empty' <<<"${input}")"
if [[ -z "${response}" ]]; then
  transcript="$(jq -r '.transcript_path // empty' <<<"${input}")"
  [[ -f "${transcript}" ]] || exit 0
  response="$(jq -rs '[.[] | select(.type == "assistant")] | last
    | .message.content[]? | select(.type == "text") | .text' \
    "${transcript}" 2>/dev/null)" || exit 0
fi
[[ -n "${cwd}" && -n "${response}" ]] || exit 0

printf '=== %s ===\n%s\n\n' "$(date -Iseconds)" "${response}" >>"${cwd}/.claude.log"
`

const profileScript = `# shellcheck shell=bash
#
# Installed by devc: export the per-repository tokens and run claude with the
# user's settings and instructions taking precedence over the project's.

if [ -r ` + envPath + ` ]; then
  # shellcheck source=/dev/null
  . ` + envPath + `
fi

[ -n "${BASH_VERSION:-}" ] || return 0

function claude() {
  # Remote Control server mode refuses --settings.
  if [[ "${1:-}" == "remote-control" ]]; then
    command claude "$@"
    return
  fi
  command claude --settings ` + overlayPath + ` \
    --append-system-prompt-file ` + promptPath + ` "$@"
}
`

// installClaude installs the settings overlay, prompt, hook and shell
// function in the container. Invalid user settings are left out of the
// overlay and reported after the rest is installed.
func (c *container) installClaude(home string) error {
	if err := c.lookupUser(); err != nil {
		return err
	}
	settings, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	overlay, settingsErr := claudeOverlay(settings)
	if settingsErr != nil {
		settingsErr = fmt.Errorf("ignoring ~/.claude/settings.json: %w", settingsErr)
		if overlay, err = claudeOverlay(nil); err != nil {
			return err
		}
	}

	// Readable by the remote user's group only, the settings can hold
	// secrets such as environment variables.
	a := newArchive(0, c.gid)
	a.addDir(relative(devcDir), 0o755, time.Now())
	a.addFile(relative(overlayPath), overlay, 0o640)
	a.addFile(relative(promptPath), []byte(precedencePrompt), 0o644)
	a.addFile(relative(logHookPath), []byte(logHookScript), 0o755)
	a.addFile(relative(profilePath), []byte(profileScript), 0o644)
	data, err := a.close()
	if err != nil {
		return err
	}
	if err := c.extract("root", "/", data); err != nil {
		return err
	}
	return settingsErr
}

// relative returns an absolute container path as an archive entry name.
func relative(path string) string {
	return strings.TrimPrefix(path, "/")
}

// claudeOverlay returns the user's settings with the devc Stop hook added.
// Claude runs identical hook commands from several scopes only once.
func claudeOverlay(settings []byte) ([]byte, error) {
	overlay := map[string]any{}
	if len(settings) > 0 {
		if err := json.Unmarshal(settings, &overlay); err != nil {
			return nil, fmt.Errorf("parse claude settings: %w", err)
		}
	}

	hooks, ok := overlay["hooks"].(map[string]any)
	if !ok {
		hooks = map[string]any{}
		overlay["hooks"] = hooks
	}
	stop, _ := hooks["Stop"].([]any)
	hooks["Stop"] = append(stop, map[string]any{
		"hooks": []any{map[string]any{"type": "command", "command": logHookPath}},
	})

	out, err := json.MarshalIndent(overlay, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}
