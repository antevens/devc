// Package config loads the devc settings and manages the shared devcontainer
// config used by repositories without their own.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/antevens/devc/internal/xdg"
)

// Settings is the optional devc config, ~/.config/devc/config.json.
type Settings struct {
	// RemoteControl makes sessions started with devc claude available in
	// the Claude apps.
	RemoteControl bool     `json:"remote_control"`
	GitLab        []GitLab `json:"gitlab"`
	GitHub        []GitHub `json:"github"`
}

// GitLab holds the credential used on the host to create project access
// tokens on a GitLab instance.
type GitLab struct {
	Host string `json:"host"`
	// TokenCommand prints a token with the api scope and at least the
	// Maintainer role on the projects, e.g. "pass show gitlab/devc".
	TokenCommand string `json:"token_command"`
}

// GitHub holds the GitHub App used on the host to create installation
// tokens.
type GitHub struct {
	Host           string `json:"host"`
	AppID          string `json:"app_id"`
	PrivateKeyFile string `json:"private_key_file"`
}

// Load reads the devc settings, which are optional.
func Load(home string) (Settings, error) {
	var s Settings
	path := filepath.Join(xdg.ConfigDir(home), "config.json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return s, fmt.Errorf("parse %s: %w", path, err)
	}
	return s, nil
}

// GitLabFor returns the GitLab credential configured for host, nil if none
// is.
func (s Settings) GitLabFor(host string) *GitLab {
	for i := range s.GitLab {
		if s.GitLab[i].Host == host {
			return &s.GitLab[i]
		}
	}
	return nil
}

// GitHubFor returns the GitHub App configured for host, nil if none is.
func (s Settings) GitHubFor(host string) *GitHub {
	for i := range s.GitHub {
		if s.GitHub[i].Host == host {
			return &s.GitHub[i]
		}
	}
	return nil
}
