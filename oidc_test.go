package webservice

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/labstack/echo/v4"
)

func TestOIDCBearerAuthentication(t *testing.T) {
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	loginPub, loginKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			fmt.Fprintf(w, `{"jwks_uri":"http://%s/keys"}`, r.Host)
		case "/login/.well-known/openid-configuration":
			fmt.Fprintf(w, `{"jwks_uri":"http://%s/login/keys"}`, r.Host)
		case "/keys", "/login/keys":
			publicKey := pub
			if r.URL.Path == "/login/keys" {
				publicKey = loginPub
			}
			fmt.Fprintf(w, `{"keys":[{"kid":"test","kty":"OKP","crv":"Ed25519","alg":"EdDSA","use":"sig","x":"%s"}]}`, base64.RawURLEncoding.EncodeToString(publicKey))
		default:
			http.NotFound(w, r)
		}
	}))
	defer provider.Close()
	specPath := filepath.Join(t.TempDir(), "jwt.yaml")
	err = os.WriteFile(specPath, []byte(`openapi: 3.2.1
info: {title: protected, version: '1'}
components:
  securitySchemes:
    bearer: {type: http, scheme: bearer}
    strict: {type: http, scheme: bearer, x-jwt-require-exp: true}
    optional: {type: http, scheme: bearer, x-jwt-require-exp: false}
security: [{bearer: []}]
paths:
  /me:
    get:
      responses:
        '200': {description: OK}
  /me/strict:
    get:
      security: [{strict: []}]
      responses:
        '200': {description: OK}
  /me/optional:
    get:
      security: [{optional: []}]
      responses:
        '200': {description: OK}
`), 0600)
	if err != nil {
		t.Fatal(err)
	}
	b := NewWebServer("jwt").WithAuthentication(provider.URL, provider.URL+"/login").UseOpenAPISpecs("/me", specPath, "jwt.yaml")
	for _, path := range []string{"/me", "/me/strict", "/me/optional"} {
		b.echo.GET(path, func(c echo.Context) error { return c.String(http.StatusOK, c.Get(ContextKey_UserEmail).(string)) })
	}
	if err := b.prepareServer(t.Context(), 0); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, tc := range []struct {
		name, path string
		iat        time.Time
		exp        any
		status     int
	}{
		{"valid", "/me", now, now.Add(time.Hour).Unix(), 200},
		{"expired", "/me", now.Add(-time.Hour), now.Add(-time.Minute).Unix(), 401},
		{"epoch expiration", "/me", now, int64(0), 401},
		{"future issue time", "/me", now.Add(time.Hour), now.Add(2 * time.Hour).Unix(), 401},
		{"existing spec without exp", "/me", now, nil, 200},
		{"explicitly optional without exp", "/me/optional", now, nil, 200},
		{"required without exp", "/me/strict", now, nil, 401},
		{"required with valid exp", "/me/strict", now, now.Add(time.Hour).Unix(), 200},
		{"required with expired exp", "/me/strict", now, now.Add(-time.Hour).Unix(), 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			claims := jwt.MapClaims{"iat": tc.iat.Unix(), "email": "reader@example.org"}
			if tc.exp != nil {
				claims["exp"] = tc.exp
			}
			token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
			token.Header["kid"] = "test"
			for providerName, signingKey := range map[string]ed25519.PrivateKey{"primary": key, "login": loginKey} {
				t.Run(providerName, func(t *testing.T) {
					raw, err := token.SignedString(signingKey)
					if err != nil {
						t.Fatal(err)
					}
					req := httptest.NewRequest(http.MethodGet, tc.path, nil)
					req.Header.Set("Authorization", "Bearer "+raw)
					rec := httptest.NewRecorder()
					b.echo.ServeHTTP(rec, req)
					if rec.Code != tc.status {
						t.Fatalf("got %d, want %d: %s", rec.Code, tc.status, rec.Body)
					}
					if tc.status == 200 && rec.Body.String() != "reader@example.org" {
						t.Errorf("unexpected user %q", rec.Body)
					}
				})
			}
		})
	}
}

func TestOIDCStartupErrorAndRetry(t *testing.T) {
	var stage atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			if stage.Load() == 0 {
				http.Error(w, "offline", 503)
				return
			}
			fmt.Fprintf(w, `{"jwks_uri":"http://%s/keys"}`, r.Host)
		case "/keys":
			if stage.Load() == 1 {
				http.Error(w, "offline", 503)
				return
			}
			fmt.Fprint(w, `{"keys":[]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer provider.Close()
	builder := NewWebServer("startup").WithAuthentication(provider.URL, "").UseOpenAPISpecs("/api", "testdata/openapi32.yaml", "catalog.yaml")
	if err := builder.Run(-1); err == nil || !strings.Contains(err.Error(), "OIDC discovery") {
		t.Fatalf("expected discovery error, got %v", err)
	}
	stage.Store(1)
	if err := builder.RunHttps(-1, "", ""); err == nil || !strings.Contains(err.Error(), "load JWKS") {
		t.Fatalf("expected JWKS error, got %v", err)
	}
	stage.Store(2)
	if err := builder.prepareServer(t.Context(), 0); err != nil {
		t.Fatalf("retry failed: %v", err)
	}
}

func TestOIDCDiscoveryHonorsDeadline(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer provider.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if _, err := resolveJWKSURL(ctx, provider.URL); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline, got %v", err)
	}
}
