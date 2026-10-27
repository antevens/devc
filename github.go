package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// GitHub has no API to create personal access tokens, so devc uses a GitHub
// App: its private key stays on the host and mints installation tokens
// limited to one repository and these permissions. They expire after an
// hour and are renewed while a session runs.
var githubPermissions = map[string]string{
	"contents":      "write",
	"pull_requests": "write",
}

// githubAPI returns the REST API URL of a GitHub host.
func githubAPI(host string) string {
	if host == "github.com" {
		return "https://api.github.com"
	}
	return "https://" + host + "/api/v3"
}

// githubConfig returns the App configured for host, nil if none is.
func (s settings) githubConfig(host string) *githubConfig {
	for i := range s.GitHub {
		if s.GitHub[i].Host == host {
			return &s.GitHub[i]
		}
	}
	return nil
}

// githubToken creates an installation token for the forge's repository.
func (p *project) githubToken(f forge) (*token, error) {
	c := p.settings.githubConfig(f.host)
	if c == nil {
		return nil, nil
	}
	_, repo, ok := strings.Cut(f.project, "/")
	if !ok || strings.Contains(repo, "/") {
		return nil, fmt.Errorf("unexpected GitHub repository %q", f.project)
	}
	jwt, err := appJWT(c.AppID, expandHome(c.PrivateKeyFile, p.home), time.Now())
	if err != nil {
		return nil, err
	}
	auth := map[string]string{"Authorization": "Bearer " + jwt, "X-GitHub-Api-Version": "2022-11-28"}
	api := githubAPI(f.host)

	var installation struct {
		ID int `json:"id"`
	}
	if err := apiCall(http.MethodGet, api+"/repos/"+f.project+"/installation", auth, nil, &installation); err != nil {
		return nil, fmt.Errorf("GitHub App %s is not installed on %s: %w", c.AppID, f.project, err)
	}

	var created struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	body := map[string]any{"repositories": []string{repo}, "permissions": githubPermissions}
	endpoint := fmt.Sprintf("%s/app/installations/%d/access_tokens", api, installation.ID)
	if err := apiCall(http.MethodPost, endpoint, auth, body, &created); err != nil {
		return nil, fmt.Errorf("create token for %s: %w", f.project, err)
	}
	return &token{Kind: github, Host: f.host, Project: f.project, Token: created.Token, Expires: created.ExpiresAt}, nil
}

// revokeGithubToken revokes an installation token with the token itself. A
// token GitHub no longer accepts is already expired or revoked.
func revokeGithubToken(t *token) error {
	if time.Now().After(t.Expires) {
		return nil
	}
	err := apiCall(http.MethodDelete, githubAPI(t.Host)+"/installation/token",
		map[string]string{"Authorization": "Bearer " + t.Token}, nil, nil)
	if err != nil && strings.Contains(err.Error(), "401") {
		return nil
	}
	return err
}

// appJWT returns the JSON Web Token authenticating as a GitHub App.
func appJWT(appID, keyFile string, now time.Time) (string, error) {
	if appID == "" {
		return "", errors.New("github app_id is not set")
	}
	data, err := os.ReadFile(keyFile)
	if err != nil {
		return "", fmt.Errorf("read GitHub App private key: %w", err)
	}
	key, err := parseRSAKey(data)
	if err != nil {
		return "", fmt.Errorf("parse GitHub App private key %s: %w", keyFile, err)
	}

	encode := func(v any) (string, error) {
		data, err := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(data), err
	}
	header, err := encode(map[string]string{"alg": "RS256", "typ": "JWT"})
	if err != nil {
		return "", err
	}
	// Issued in the past to allow for clock drift, GitHub accepts at most
	// ten minutes of validity.
	claims, err := encode(map[string]any{"iat": now.Add(-time.Minute).Unix(), "exp": now.Add(9 * time.Minute).Unix(), "iss": appID})
	if err != nil {
		return "", err
	}
	signed := header + "." + claims
	digest := sha256.Sum256([]byte(signed))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return signed + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

// parseRSAKey parses a PEM encoded PKCS #1 or PKCS #8 RSA private key.
func parseRSAKey(data []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("no PEM data")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("not an RSA key")
	}
	return key, nil
}

// expandHome expands a leading ~/ in path.
func expandHome(path, home string) string {
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		return filepath.Join(home, rest)
	}
	return path
}
