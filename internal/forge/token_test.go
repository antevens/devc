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
	"strings"
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

func TestGitlabToken(t *testing.T) {
	var created []map[string]any
	var revoked []string
	host, client := fakeForge(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v4/personal_access_tokens/self":
			json.NewEncoder(w).Encode(map[string]any{"active": r.Header.Get("PRIVATE-TOKEN") == "glpat-2"})
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/api/v4/projects/group%2Frepo/access_tokens":
			if r.Header.Get("PRIVATE-TOKEN") != "admin" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			created = append(created, body)
			json.NewEncoder(w).Encode(map[string]any{"id": len(created), "token": fmt.Sprintf("glpat-%d", len(created))})
		case r.Method == http.MethodDelete:
			revoked = append(revoked, r.URL.EscapedPath())
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, r.Method+" "+r.URL.Path, http.StatusNotFound)
		}
	})

	home := t.TempDir()
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	fc := NewClient(config.Settings{GitLab: []config.GitLab{{Host: host, TokenCommand: "echo admin"}}}, home, "/src/repo")
	fc.HTTP = client
	f := Forge{Kind: GitLab, Host: host, Project: "group/repo"}

	// The first token is created, then replaced as it is not active.
	for i, want := range []string{"glpat-1", "glpat-2", "glpat-2"} {
		tok, err := fc.Get(context.Background(), f)
		if err != nil {
			t.Fatal(err)
		}
		if tok.Token != want {
			t.Errorf("call %d: token %q, want %q", i, tok.Token, want)
		}
	}
	if len(created) != 2 {
		t.Fatalf("created %d tokens, want 2", len(created))
	}
	if created[0]["access_level"] != float64(30) || !reflect.DeepEqual(created[0]["scopes"], []any{"api", "write_repository"}) {
		t.Errorf("token request = %v", created[0])
	}
	if !reflect.DeepEqual(revoked, []string{"/api/v4/projects/group%2Frepo/access_tokens/1"}) {
		t.Errorf("revoked = %v", revoked)
	}

	if err := fc.RevokeAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fc.Cached(f) != nil {
		t.Error("token still cached after RevokeAll")
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
