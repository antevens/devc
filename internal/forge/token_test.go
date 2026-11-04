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
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/antevens/devc/internal/config"
)

// fakeForge serves a forge API over TLS and returns its host and a client
// trusting it.
func fakeForge(t *testing.T, handler http.HandlerFunc) (string, *http.Client) {
	t.Helper()
	srv := httptest.NewTLSServer(handler)
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "https://"), srv.Client()
}

func writeKey(t *testing.T, path string, pkcs8 bool) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}
	if pkcs8 {
		der, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			t.Fatal(err)
		}
		block = &pem.Block{Type: "PRIVATE KEY", Bytes: der}
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	return key
}

func TestAppJWT(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "app.pem")
	key := writeKey(t, keyFile, false)

	now := time.Unix(1_800_000_000, 0)
	jwt, err := appJWT("42", keyFile, now)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Fatalf("jwt has %d parts", len(parts))
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, digest[:], signature); err != nil {
		t.Errorf("signature: %v", err)
	}
	claimsJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims struct {
		Iat, Exp int64
		Iss      string
	}
	if err := json.Unmarshal(claimsJSON, &claims); err != nil {
		t.Fatal(err)
	}
	if claims.Iss != "42" || claims.Iat != now.Unix()-60 || claims.Exp != now.Unix()+540 {
		t.Errorf("claims = %+v", claims)
	}
}

// fakeGitlab is a GitLab API serving project access tokens of group/repo,
// created with the credential "admin".
type fakeGitlab struct {
	mu       sync.Mutex
	created  []map[string]any
	inactive map[string]bool
	revoked  []string
	// selfStatus and createStatus fail the requests when set.
	selfStatus, createStatus int
}

func (g *fakeGitlab) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v4/personal_access_tokens/self":
		if g.selfStatus != 0 {
			http.Error(w, "failed", g.selfStatus)
			return
		}
		if g.inactive[r.Header.Get("PRIVATE-TOKEN")] {
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"active": true})
	case r.Method == http.MethodPost && r.URL.EscapedPath() == "/api/v4/projects/group%2Frepo/access_tokens":
		if r.Header.Get("PRIVATE-TOKEN") != "admin" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if g.createStatus != 0 {
			http.Error(w, "failed", g.createStatus)
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		g.created = append(g.created, body)
		// Two sessions renewing at once would both create a token.
		time.Sleep(20 * time.Millisecond)
		json.NewEncoder(w).Encode(map[string]any{"id": len(g.created), "token": fmt.Sprintf("glpat-%d", len(g.created)), "expires_at": body["expires_at"]})
	case r.Method == http.MethodDelete:
		g.revoked = append(g.revoked, r.URL.EscapedPath())
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, r.Method+" "+r.URL.Path, http.StatusNotFound)
	}
}

func (g *fakeGitlab) set(fn func()) {
	g.mu.Lock()
	defer g.mu.Unlock()
	fn()
}

// requests returns the create requests and the paths of the revoked tokens.
func (g *fakeGitlab) requests() ([]map[string]any, []string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.created), slices.Clone(g.revoked)
}

// gitlabClient returns a client for group/repo on a fake GitLab, with its
// tokens cached in a temporary state directory.
func gitlabClient(t *testing.T) (*Client, Forge, *fakeGitlab) {
	t.Helper()
	g := &fakeGitlab{inactive: map[string]bool{}}
	host, client := fakeForge(t, g.ServeHTTP)
	home := t.TempDir()
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	fc := NewClient(config.Settings{GitLab: []config.GitLab{{Host: host, TokenCommand: "echo admin"}}}, home, "/src/repo")
	fc.HTTP = client
	return fc, Forge{Kind: GitLab, Host: host, Project: "group/repo"}, g
}

// tokenOf returns the token tokens gets for f, and its error.
func tokenOf(t *testing.T, fc *Client, f Forge) (string, error) {
	t.Helper()
	tokens, err := fc.tokens(context.Background(), []Forge{f})
	if len(tokens) != 1 {
		t.Fatalf("got %d tokens, %v", len(tokens), err)
	}
	return tokens[0].Token, err
}

func TestGitlabToken(t *testing.T) {
	fc, f, g := gitlabClient(t)
	ctx := context.Background()

	if tok, err := tokenOf(t, fc, f); tok != "glpat-1" || err != nil {
		t.Fatalf("first token: %s, %v", tok, err)
	}
	created, _ := g.requests()
	if tok, err := tokenOf(t, fc, f); tok != "glpat-1" || err != nil || len(created) != 1 {
		t.Fatalf("cached token: %s, %v, %d created", tok, err, len(created))
	}
	body := created[0]
	expiresAt := time.Now().UTC().Add(gitlabLifetime).Format(time.DateOnly)
	if body["access_level"] != float64(30) || !reflect.DeepEqual(body["scopes"], []any{"api", "write_repository"}) || body["expires_at"] != expiresAt {
		t.Errorf("token request = %v", body)
	}
	if want, _ := time.Parse(time.DateOnly, expiresAt); !fc.Cached(f).Expires.Equal(want) {
		t.Errorf("expires %v, want %v", fc.Cached(f).Expires, want)
	}

	// A token GitLab rejects is replaced and kept until revokeRetired.
	g.set(func() { g.inactive["glpat-1"] = true })
	if tok, err := tokenOf(t, fc, f); tok != "glpat-2" || err != nil {
		t.Fatalf("replaced token: %s, %v", tok, err)
	}
	if _, revoked := g.requests(); len(revoked) != 0 {
		t.Errorf("revoked %v before the new token was installed", revoked)
	}
	if err := fc.revokeRetired(ctx, f); err != nil {
		t.Fatal(err)
	}
	if _, revoked := g.requests(); !reflect.DeepEqual(revoked, []string{"/api/v4/projects/group%2Frepo/access_tokens/1"}) {
		t.Errorf("revoked = %v", revoked)
	}

	// Failing checks keep the token.
	g.set(func() { g.selfStatus = http.StatusBadGateway })
	tok, err := tokenOf(t, fc, f)
	if created, _ := g.requests(); tok != "glpat-2" || err == nil || len(created) != 2 {
		t.Errorf("failed check: %s, %v, %d created", tok, err, len(created))
	}
	g.set(func() { g.selfStatus = 0 })

	if err := fc.RevokeAll(ctx); err != nil {
		t.Fatal(err)
	}
	if fc.Cached(f) != nil {
		t.Error("token still cached after RevokeAll")
	}
	if _, revoked := g.requests(); len(revoked) != 2 || revoked[1] != "/api/v4/projects/group%2Frepo/access_tokens/2" {
		t.Errorf("revoked = %v", revoked)
	}
}

func TestGitlabTokenRenewFails(t *testing.T) {
	fc, f, g := gitlabClient(t)
	if _, err := tokenOf(t, fc, f); err != nil {
		t.Fatal(err)
	}

	// Due for renewal but valid: kept when creating its replacement fails.
	c, err := fc.load(fc.file(f))
	if err != nil {
		t.Fatal(err)
	}
	c.Expires = time.Now().Add(renewBefore[GitLab] / 2)
	if err := fc.save(fc.file(f), c); err != nil {
		t.Fatal(err)
	}
	g.set(func() { g.createStatus = http.StatusInternalServerError })
	if tok, err := tokenOf(t, fc, f); tok != "glpat-1" || err == nil {
		t.Errorf("failed renewal: %s, %v", tok, err)
	}

	// Expired: no token.
	c.Expires = time.Now().Add(-time.Minute)
	if err := fc.save(fc.file(f), c); err != nil {
		t.Fatal(err)
	}
	if tokens, err := fc.tokens(context.Background(), []Forge{f}); len(tokens) != 0 || err == nil {
		t.Errorf("expired: %v, %v", tokens, err)
	}
	if loaded, err := fc.load(fc.file(f)); err != nil || loaded.Token.Token != "glpat-1" || len(loaded.Retired) != 0 {
		t.Errorf("cache changed by failed renewals: %+v, %v", loaded, err)
	}
}

func TestGitlabTokenConcurrent(t *testing.T) {
	fc, f, g := gitlabClient(t)
	var wg sync.WaitGroup
	got := make([]string, 4)
	for i := range got {
		wg.Go(func() {
			// Each devc process has its own client.
			c := NewClient(fc.Settings, fc.Home, "/src/repo")
			c.HTTP = fc.HTTP
			unlock, err := c.lock()
			if err != nil {
				t.Error(err)
				return
			}
			defer unlock()
			if tok, err := c.get(context.Background(), f); err != nil {
				t.Error(err)
			} else {
				got[i] = tok.Token
			}
		})
	}
	wg.Wait()
	if created, _ := g.requests(); len(created) != 1 || !reflect.DeepEqual(got, []string{"glpat-1", "glpat-1", "glpat-1", "glpat-1"}) {
		t.Errorf("created %d tokens, got %v", len(created), got)
	}
}

func TestRevokeAllCacheFormat(t *testing.T) {
	fc, f, g := gitlabClient(t)
	// Cache files without retired tokens.
	data := fmt.Sprintf(`{"kind":"gitlab","host":%q,"project":"group/repo","id":5,"token":"glpat-5","expires":%q}`,
		f.Host, time.Now().Add(gitlabLifetime).Format(time.RFC3339))
	if err := os.MkdirAll(fc.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fc.file(f), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if tok := fc.Cached(f); tok == nil || tok.Token != "glpat-5" || tok.ID != 5 {
		t.Fatalf("cached = %+v", tok)
	}
	if err := fc.RevokeAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, revoked := g.requests(); !reflect.DeepEqual(revoked, []string{"/api/v4/projects/group%2Frepo/access_tokens/5"}) {
		t.Errorf("revoked = %v", revoked)
	}
	if entries, _ := filepath.Glob(filepath.Join(fc.Dir, "*.json")); len(entries) != 0 {
		t.Errorf("left %v", entries)
	}
}

func TestGithubToken(t *testing.T) {
	home := t.TempDir()
	keyFile := filepath.Join(home, "app.pem")
	writeKey(t, keyFile, true)

	var request map[string]any
	expires := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	host, client := fakeForge(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ey") {
			http.Error(w, "no jwt", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/v3/repos/owner/repo/installation":
			json.NewEncoder(w).Encode(map[string]any{"id": 7})
		case "/api/v3/app/installations/7/access_tokens":
			json.NewDecoder(r.Body).Decode(&request)
			json.NewEncoder(w).Encode(map[string]any{"token": "ghs_x", "expires_at": expires})
		default:
			http.NotFound(w, r)
		}
	})

	fc := NewClient(config.Settings{GitHub: []config.GitHub{{Host: host, AppID: "1", PrivateKeyFile: keyFile}}}, home, "/src/repo")
	fc.HTTP = client
	tok, err := fc.githubToken(context.Background(), Forge{Kind: GitHub, Host: host, Project: "owner/repo"})
	if err != nil {
		t.Fatal(err)
	}
	if tok.Token != "ghs_x" || !tok.Expires.Equal(expires) {
		t.Errorf("token = %+v", tok)
	}
	want := map[string]any{
		"repositories": []any{"repo"},
		"permissions":  map[string]any{"contents": "write", "pull_requests": "write"},
	}
	if !reflect.DeepEqual(request, want) {
		t.Errorf("token request = %v, want %v", request, want)
	}

	if tok, err := fc.githubToken(context.Background(), Forge{Kind: GitHub, Host: "github.com", Project: "owner/repo"}); tok != nil || err != nil {
		t.Errorf("unconfigured host: %v, %v", tok, err)
	}
}
