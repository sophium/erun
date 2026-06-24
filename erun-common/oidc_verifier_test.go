package eruncommon

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// mockOIDCProvider is a self-contained OIDC issuer for tests: an httptest server
// that publishes a discovery document and a JWKS, plus the RSA signing key used
// to mint tokens. It is the shared harness for both the erun-common verifier
// tests and (mirrored) the erun-mcp dispatch tests, standing in for a real
// Zitadel/AWS issuer without a network dependency.
type mockOIDCProvider struct {
	server *httptest.Server
	key    *rsa.PrivateKey
	keyID  string
}

func newMockOIDCProvider(t *testing.T) *mockOIDCProvider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	p := &mockOIDCProvider{key: key, keyID: "test-key-1"}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{
			"issuer":                                p.issuer(),
			"jwks_uri":                              p.issuer() + "/keys",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
			Key:       p.key.Public(),
			KeyID:     p.keyID,
			Algorithm: string(jose.RS256),
			Use:       "sig",
		}}})
	})
	p.server = httptest.NewServer(mux)
	t.Cleanup(p.server.Close)
	return p
}

func (p *mockOIDCProvider) issuer() string { return p.server.URL }

// sign mints an RS256 JWT for the given claims, signed by this provider's key.
func (p *mockOIDCProvider) sign(t *testing.T, claims map[string]any) string {
	t.Helper()
	return signWithKey(t, p.key, p.keyID, claims)
}

// signWithKey signs claims with an arbitrary RSA key + kid, so a test can forge a
// token whose signature does NOT match the provider's published JWKS.
func signWithKey(t *testing.T, key *rsa.PrivateKey, keyID string, claims map[string]any) string {
	t.Helper()
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", keyID),
	)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	token, err := jwt.Signed(signer).Claims(claims).Serialize()
	if err != nil {
		t.Fatalf("serialize token: %v", err)
	}
	return token
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

// standardClaims builds a claim map valid at the given time for the issuer.
func standardClaims(issuer string, now time.Time) map[string]any {
	return map[string]any{
		"iss": issuer,
		"sub": "user-1",
		"aud": "erun-mcp:acme/prod",
		"iat": now.Unix(),
		"exp": now.Add(time.Hour).Unix(),
	}
}

func TestOIDCVerifierVerifiesSignedToken(t *testing.T) {
	provider := newMockOIDCProvider(t)
	now := time.Now()
	token := provider.sign(t, standardClaims(provider.issuer(), now))

	verifier := NewOIDCVerifier()
	claims, err := verifier.Verify(context.Background(), provider.issuer(), token)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if claims.Issuer != provider.issuer() {
		t.Fatalf("issuer = %q, want %q", claims.Issuer, provider.issuer())
	}
	if claims.Subject != "user-1" {
		t.Fatalf("subject = %q", claims.Subject)
	}
	if len(claims.Audience) != 1 || claims.Audience[0] != "erun-mcp:acme/prod" {
		t.Fatalf("audience = %v", claims.Audience)
	}
	if claims.Raw["sub"] != "user-1" {
		t.Fatalf("raw sub = %v", claims.Raw["sub"])
	}
}

func TestOIDCVerifierRejectsWrongIssuer(t *testing.T) {
	provider := newMockOIDCProvider(t)
	// The token's iss is a different value than the issuer we ask go-oidc to
	// verify against, so verification must fail (go-oidc checks iss).
	token := provider.sign(t, standardClaims("https://attacker.example", time.Now()))

	verifier := NewOIDCVerifier()
	if _, err := verifier.Verify(context.Background(), provider.issuer(), token); err == nil {
		t.Fatal("expected a token whose iss differs from the issuer to be rejected")
	}
}

func TestOIDCVerifierRejectsBadSignature(t *testing.T) {
	provider := newMockOIDCProvider(t)
	// Sign with a different key than the provider publishes in its JWKS.
	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate other key: %v", err)
	}
	token := signWithKey(t, otherKey, provider.keyID, standardClaims(provider.issuer(), time.Now()))

	verifier := NewOIDCVerifier()
	if _, err := verifier.Verify(context.Background(), provider.issuer(), token); err == nil {
		t.Fatal("expected a token signed by a key not in the JWKS to be rejected")
	}
}

func TestOIDCVerifierRejectsExpiredToken(t *testing.T) {
	provider := newMockOIDCProvider(t)
	claims := standardClaims(provider.issuer(), time.Now())
	claims["exp"] = time.Now().Add(-time.Hour).Unix()
	token := provider.sign(t, claims)

	verifier := NewOIDCVerifier()
	if _, err := verifier.Verify(context.Background(), provider.issuer(), token); err == nil {
		t.Fatal("expected an expired token to be rejected")
	}
}

func TestIssuerFromUnverifiedJWT(t *testing.T) {
	provider := newMockOIDCProvider(t)
	token := provider.sign(t, standardClaims(provider.issuer(), time.Now()))

	issuer, err := IssuerFromUnverifiedJWT(token)
	if err != nil {
		t.Fatalf("issuer: %v", err)
	}
	if issuer != provider.issuer() {
		t.Fatalf("issuer = %q, want %q", issuer, provider.issuer())
	}

	if _, err := IssuerFromUnverifiedJWT("not-a-jwt"); err == nil {
		t.Fatal("expected a non-JWT to be rejected")
	}
}
