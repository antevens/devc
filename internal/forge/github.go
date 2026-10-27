package forge

import (
	"context"
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

// githubPermissions are granted to installation tokens.
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

// githubToken creates an installation token of the configured GitHub App,
// limited to the forge's repository and githubPermissions. It returns nil
// when no App is configured for the host.
func (fc *Client) githubToken(ctx context.Context, f Forge) (*Token, error) {
	app := fc.Settings.GitHubFor(f.Host)
	if app == nil {
		return nil, nil
	}
	_, repo, ok := strings.Cut(f.Project, "/")
	if !ok || strings.Contains(repo, "/") {
		return nil, fmt.Errorf("unexpected GitHub repository %q", f.Project)
	}
	jwt, err := appJWT(app.AppID, expandHome(app.PrivateKeyFile, fc.Home), time.Now())
	if err != nil {
		return nil, err
	}
	auth := map[string]string{"Authorization": "Bearer " + jwt, "X-GitHub-Api-Version": "2022-11-28"}
	api := githubAPI(f.Host)

	var installation struct {
		ID int `json:"id"`
	}
	if err := fc.call(ctx, http.MethodGet, api+"/repos/"+f.Project+"/installation", auth, nil, &installation); err != nil {
		return nil, fmt.Errorf("find GitHub App %s installation on %s: %w", app.AppID, f.Project, err)
	}

	var created struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	body := map[string]any{"repositories": []string{repo}, "permissions": githubPermissions}
	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", api, installation.ID)
	if err := fc.call(ctx, http.MethodPost, url, auth, body, &created); err != nil {
		return nil, fmt.Errorf("create token for %s: %w", f.Project, err)
	}
	return &Token{Kind: GitHub, Host: f.Host, Project: f.Project, Token: created.Token, Expires: created.ExpiresAt}, nil
}

// revokeGithubToken revokes an installation token using the token. Expired
// tokens and tokens GitHub rejects are treated as revoked.
func (fc *Client) revokeGithubToken(ctx context.Context, tok *Token) error {
	if time.Now().After(tok.Expires) {
		return nil
	}
	err := fc.call(ctx, http.MethodDelete, githubAPI(tok.Host)+"/installation/token",
		map[string]string{"Authorization": "Bearer " + tok.Token}, nil, nil)
	if hasStatus(err, http.StatusUnauthorized) {
		return nil
	}
	return err
}

// appJWT returns a JSON Web Token authenticating as a GitHub App, valid from
// a minute before now for ten minutes.
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
