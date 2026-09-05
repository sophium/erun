package mcptoken

import (
	"crypto/ed25519"
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

// TestSignRegistryRoundTrip proves a minted registry token carries the claims
// the registry's bearer auth enforces (audience = the registry's own `service`
// value, exactly the granted access — never more than what was passed in) and
// verifies against the backend's public key, the same key a hosted deploy
// injects into the registry's trusted-cert config.
func TestSignRegistryRoundTrip(t *testing.T) {
	signer := newTestSigner(t)
	now := time.Unix(1_700_000_000, 0)
	access := []RegistryAccessScope{{Type: "repository", Name: "frs/hello", Actions: []string{"pull", "push"}}}

	token, err := signer.SignRegistry("erun", "registry.erunpaas.com", access, now)
	if err != nil {
		t.Fatalf("sign registry: %v", err)
	}

	claims := decodeRegistryClaims(t, token)
	if claims.Issuer != RegistryTokenIssuer {
		t.Fatalf("iss = %q, want %q", claims.Issuer, RegistryTokenIssuer)
	}
	if claims.Audience != "registry.erunpaas.com" {
		t.Fatalf("aud = %q, want registry.erunpaas.com", claims.Audience)
	}
	if claims.Subject != "erun" {
		t.Fatalf("sub = %q, want erun", claims.Subject)
	}
	if claims.IssuedAt != now.Unix() {
		t.Fatalf("iat = %d, want %d", claims.IssuedAt, now.Unix())
	}
	if want := now.Add(registryTokenTTL).Unix(); claims.Expiration != want {
		t.Fatalf("exp = %d, want %d", claims.Expiration, want)
	}
	if len(claims.Access) != 1 || claims.Access[0].Name != "frs/hello" {
		t.Fatalf("access = %#v, want the single frs/hello grant passed in", claims.Access)
	}

	if !verifyRegistrySignature(t, token, signer.publicKey) {
		t.Fatal("registry token signature did not verify against the backend public key")
	}
}

// TestSignRegistryNeverWidensAccess proves the signer mints exactly the access
// grants it was given — an empty grant (the outcome of a scope clamped away
// entirely, e.g. a cross-tenant request) mints a valid token that grants
// nothing, never an error and never a fallback grant it invents itself.
func TestSignRegistryNeverWidensAccess(t *testing.T) {
	signer := newTestSigner(t)
	now := time.Unix(1_700_000_000, 0)
	token, err := signer.SignRegistry("erun", "registry.erunpaas.com", nil, now)
	if err != nil {
		t.Fatalf("sign registry with no granted access: %v", err)
	}
	claims := decodeRegistryClaims(t, token)
	if len(claims.Access) != 0 {
		t.Fatalf("access = %#v, want empty", claims.Access)
	}
	if !verifyRegistrySignature(t, token, signer.publicKey) {
		t.Fatal("empty-access registry token signature did not verify")
	}
}

// TestSignRegistryRequiresService proves the signer refuses to mint a token
// with no service (the registry challenge's own audience value) rather than
// minting one with an empty/ambiguous audience.
func TestSignRegistryRequiresService(t *testing.T) {
	signer := newTestSigner(t)
	if _, err := signer.SignRegistry("erun", "", nil, time.Unix(1_700_000_000, 0)); err == nil {
		t.Fatal("expected an error minting a token with no service/audience")
	}
}

func decodeRegistryClaims(t *testing.T, token string) registryTokenClaims {
	t.Helper()
	segments := strings.Split(token, ".")
	if len(segments) != 3 {
		t.Fatalf("token has %d segments, want 3", len(segments))
	}
	var claims registryTokenClaims
	if err := decodeSegment(segments[1], &claims); err != nil {
		t.Fatalf("decode claims: %v", err)
	}
	return claims
}

func verifyRegistrySignature(t *testing.T, token string, publicKey ed25519.PublicKey) bool {
	t.Helper()
	segments := strings.Split(token, ".")
	if len(segments) != 3 {
		t.Fatalf("token has %d segments, want 3", len(segments))
	}
	signature, err := base64.RawURLEncoding.DecodeString(segments[2])
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	return ed25519.Verify(publicKey, []byte(segments[0]+"."+segments[1]), signature)
}
