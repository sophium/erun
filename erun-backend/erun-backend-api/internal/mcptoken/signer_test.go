package mcptoken

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// newTestSigner builds a signer whose issuer points at a temp public-key file so
// the unified verifier (which loads a file:// issuer's key from disk) can verify
// in-process — standing in for the in-pod key the deploy injects in production.
func newTestSigner(t *testing.T) (*Signer, string) {
	t.Helper()
	dir := t.TempDir()
	s := NewSigner(filepath.Join(dir, "mcp.key"))
	pubPath := filepath.Join(dir, "mcp.pub")
	s.issuer = "file://" + pubPath
	pub, err := s.PublicKeyPEM()
	if err != nil {
		t.Fatalf("PublicKeyPEM: %v", err)
	}
	if err := os.WriteFile(pubPath, pub, 0o600); err != nil {
		t.Fatalf("write pub: %v", err)
	}
	return s, pubPath
}

func TestSignerMintsTokenTheUnifiedVerifierAccepts(t *testing.T) {
	s, _ := newTestSigner(t)
	now := time.Unix(1_700_000_000, 0)

	token, err := s.Sign("erun-user-123", "acme", "prod", now)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	// The env's MCP edge verifies exactly this way (eruncommon.VerifyMCPToken,
	// #656): load the file:// issuer's key, check signature + exp + the per-env aud.
	claims, err := eruncommon.VerifyMCPToken(
		context.Background(), nil, token, s.Issuer(),
		eruncommon.MCPTokenAudience("acme", "prod"), now.Add(time.Minute),
	)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if claims.Subject != "erun-user-123" {
		t.Errorf("subject = %q, want erun-user-123", claims.Subject)
	}
	if claims.Audience != "erun-mcp:acme/prod" {
		t.Errorf("audience = %q, want erun-mcp:acme/prod", claims.Audience)
	}
}

func TestSignerTokenIsEnvScoped(t *testing.T) {
	s, _ := newTestSigner(t)
	now := time.Unix(1_700_000_000, 0)
	token, err := s.Sign("u", "acme", "prod", now)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	// A token minted for prod must not satisfy another env's audience.
	if _, err := eruncommon.VerifyMCPToken(
		context.Background(), nil, token, s.Issuer(),
		eruncommon.MCPTokenAudience("acme", "staging"), now.Add(time.Minute),
	); err == nil {
		t.Fatal("expected verification to fail for a different env's audience")
	}
}

func TestSignerPersistsAndReusesKey(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "mcp.key")

	first := NewSigner(keyPath)
	pub1, err := first.PublicKeyPEM()
	if err != nil {
		t.Fatalf("first PublicKeyPEM: %v", err)
	}
	// A second signer over the same path must load the persisted key, not mint a
	// new one — else tokens would stop verifying against the injected public key
	// across API restarts.
	second := NewSigner(keyPath)
	pub2, err := second.PublicKeyPEM()
	if err != nil {
		t.Fatalf("second PublicKeyPEM: %v", err)
	}
	if string(pub1) != string(pub2) {
		t.Error("expected the persisted key to be reused across signer instances")
	}
}

func TestSignerRequiresKeyPath(t *testing.T) {
	if _, err := NewSigner("").Sign("u", "acme", "prod", time.Unix(1, 0)); err == nil {
		t.Fatal("expected an error when no key path is configured")
	}
}
