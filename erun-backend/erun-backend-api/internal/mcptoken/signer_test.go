package mcptoken

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// TestSignerMintsPerEnvToken proves a minted token carries the claims the
// deployed edge enforces (the fixed in-pod file:// issuer, the per-env
// audience, the ERun-user sub, a bounded lifetime) and that its signature
// verifies against the backend's public key — the key the deploy injects at
// the issuer's path so the edge accepts it with no live IdP.
func TestSignerMintsPerEnvToken(t *testing.T) {
	privatePEM, publicPEM, err := eruncommon.GenerateDesktopIdentity()
	if err != nil {
		t.Fatalf("generate identity: %v", err)
	}
	signer, err := NewSigner(privatePEM)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	now := time.Unix(1_700_000_000, 0)
	token, audience, err := signer.Sign("acme", "prod", "user-1", string(eruncommon.MCPCapabilityAttach), now)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if want := "erun-mcp:acme/prod"; audience != want {
		t.Fatalf("audience = %q, want %q", audience, want)
	}

	claims := decodeClaims(t, token)
	assertEnforcedClaims(t, claims, audience, now)
	if claims.Scope != string(eruncommon.MCPCapabilityAttach) {
		t.Fatalf("scope claim = %q, want %q", claims.Scope, eruncommon.MCPCapabilityAttach)
	}

	if !verifySignature(t, token, publicPEM) {
		t.Fatal("token signature did not verify against the backend public key")
	}
}

// assertEnforcedClaims checks every claim the deployed edge enforces on a minted
// token: the fixed in-pod file:// issuer, the per-env audience, the ERun-user
// sub, and a bounded lifetime.
func assertEnforcedClaims(t *testing.T, claims eruncommon.MCPTokenClaims, audience string, now time.Time) {
	t.Helper()
	if claims.Issuer != eruncommon.DesktopMCPIssuer() {
		t.Fatalf("iss = %q, want %q", claims.Issuer, eruncommon.DesktopMCPIssuer())
	}
	if claims.Audience != audience {
		t.Fatalf("aud claim = %q, want %q", claims.Audience, audience)
	}
	if claims.Subject != "user-1" {
		t.Fatalf("sub = %q, want user-1", claims.Subject)
	}
	if claims.IssuedAt != now.Unix() {
		t.Fatalf("iat = %d, want %d", claims.IssuedAt, now.Unix())
	}
	if got := claims.ExpiresAt - claims.IssuedAt; got != int64(tokenTTL/time.Second) {
		t.Fatalf("ttl = %ds, want %ds", got, int64(tokenTTL/time.Second))
	}
}

func TestNewSignerRejectsInvalidKey(t *testing.T) {
	if _, err := NewSigner([]byte("-----BEGIN PRIVATE KEY-----\nnope\n-----END PRIVATE KEY-----\n")); err == nil {
		t.Fatal("expected an error for a non-Ed25519 key")
	}
}

func decodeClaims(t *testing.T, token string) eruncommon.MCPTokenClaims {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token has %d segments, want 3", len(parts))
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode claims segment: %v", err)
	}
	var claims eruncommon.MCPTokenClaims
	if err := json.Unmarshal(raw, &claims); err != nil {
		t.Fatalf("unmarshal claims: %v", err)
	}
	return claims
}

func verifySignature(t *testing.T, token string, publicPEM []byte) bool {
	t.Helper()
	block, _ := pem.Decode(publicPEM)
	if block == nil {
		t.Fatal("public key is not valid PEM")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		t.Fatalf("parse public key: %v", err)
	}
	publicKey, ok := parsed.(ed25519.PublicKey)
	if !ok {
		t.Fatal("public key is not Ed25519")
	}
	parts := strings.Split(token, ".")
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	return ed25519.Verify(publicKey, []byte(parts[0]+"."+parts[1]), signature)
}
