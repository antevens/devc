// Package claude installs the user's Claude Code settings in a container and
// builds claude command lines that give them precedence over project
// settings.
package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/antevens/devc/internal/archive"
	"github.com/antevens/devc/internal/assets"
	"github.com/antevens/devc/internal/container"
)

// Flags load the user's settings at command line precedence, above project
// settings, and append the instruction precedence prompt.
var Flags = []string{"--settings", assets.ClaudeSettings, "--append-system-prompt-file", assets.ClaudePromptPath}

// Command returns the claude command line for args. remoteControl adds
// --remote-control named name to interactive sessions. Server mode
// (remote-control) does not accept Flags and gets args unchanged.
func Command(args []string, name string, remoteControl bool) []string {
	cmd := []string{"claude"}
	if len(args) > 0 && args[0] == "remote-control" {
		return append(cmd, args...)
	}
	cmd = append(cmd, Flags...)
	if remoteControl && !hasAnyArg(args, "--remote-control", "--rc", "-p", "--print") {
		cmd = append(cmd, "--remote-control", name)
	}
	return append(cmd, args...)
}

// RemoteCommand returns the command line running claude in Remote Control
// server mode, named name unless args set --name.
func RemoteCommand(args []string, name string) []string {
	cmd := []string{"claude", "remote-control"}
	if !hasAnyArg(args, "--name") {
		cmd = append(cmd, "--name", name)
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

// Install writes the settings overlay, prompt, log hook and profile script
// into the container. Invalid user settings are left out of the overlay and
// returned as an error after everything else is installed.
func Install(c *container.Container, home string) error {
	settings, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	overlay, settingsErr := Overlay(settings)
	if settingsErr != nil {
		settingsErr = fmt.Errorf("ignoring ~/.claude/settings.json: %w", settingsErr)
		if overlay, err = Overlay(nil); err != nil {
			return err
		}
	}

	a := archive.New(0, c.GID)
	a.AddDir(archive.Entry(assets.Dir), 0o755)
	a.AddFile(archive.Entry(assets.ClaudeSettings), overlay, 0o640)
	a.AddFile(archive.Entry(assets.ClaudePromptPath), assets.ClaudePrompt, 0o644)
	a.AddFile(archive.Entry(assets.LogHookPath), assets.LogHook, 0o755)
	a.AddFile(archive.Entry(assets.ProfilePath), assets.Profile, 0o644)
	data, err := a.Bytes()
	if err != nil {
		return err
	}
	if err := c.ExtractRoot(data); err != nil {
		return err
	}
	return settingsErr
}

// Overlay returns the user's settings with the log hook added to the Stop
// hooks.
func Overlay(settings []byte) ([]byte, error) {
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
		"hooks": []any{map[string]any{"type": "command", "command": assets.LogHookPath}},
	})

	out, err := json.MarshalIndent(overlay, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}
