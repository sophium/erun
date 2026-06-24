package eruncommon

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeTrustedPublicKey writes a generated public key to a temp file and returns
// the key pair PEMs plus the file:// issuer the MCP server would trust.
func writeTrustedPublicKey(t *testing.T) (privatePEM []byte, issuer string) {
	t.Helper()
	priv, pub, err := GenerateDesktopIdentity()
	if err != nil {
		t.Fatalf("generate identity: %v", err)
	}
	path := filepath.Join(t.TempDir(), "desktopid.pub")
	if err := os.WriteFile(path, pub, 0o600); err != nil {
		t.Fatalf("write public key: %v", err)
	}
	return priv, FileIssuer(path)
}

func signTestToken(t *testing.T, privatePEM []byte, claims MCPTokenClaims) string {
	t.Helper()
	token, err := SignMCPToken(privatePEM, claims)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return token
}

func TestMCPTokenSignVerifyRoundTrip(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	priv, issuer := writeTrustedPublicKey(t)
	token := signTestToken(t, priv, MCPTokenClaims{
		Issuer:    issuer,
		Subject:   "desktop",
		Audience:  "erun-mcp",
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(time.Hour).Unix(),
	})

	claims, err := VerifyMCPToken(token, issuer, "erun-mcp", now)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if claims.Subject != "desktop" || claims.Issuer != issuer {
		t.Fatalf("claims = %+v", claims)
	}
}

func TestMCPTokenVerifyRejections(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	priv, issuer := writeTrustedPublicKey(t)
	good := MCPTokenClaims{Issuer: issuer, Audience: "erun-mcp", ExpiresAt: now.Add(time.Hour).Unix()}

	t.Run("tampered signature", func(t *testing.T) {
		token := signTestToken(t, priv, good)
		segments := strings.Split(token, ".")
		// Decode → flip a byte → re-encode so the signature is deterministically
		// different (flipping a base64url char alone can change only padding bits).
		sig, err := base64.RawURLEncoding.DecodeString(segments[2])
		if err != nil {
			t.Fatalf("decode signature: %v", err)
		}
		sig[0] ^= 0xFF
		segments[2] = base64.RawURLEncoding.EncodeToString(sig)
		if _, err := VerifyMCPToken(strings.Join(segments, "."), issuer, "erun-mcp", now); err == nil {
			t.Fatal("expected a tampered signature to be rejected")
		}
	})

	t.Run("wrong signing key", func(t *testing.T) {
		// Trusted issuer holds identity A's public key; sign with identity B.
		otherPriv, _, err := GenerateDesktopIdentity()
		if err != nil {
			t.Fatalf("generate other identity: %v", err)
		}
		token := signTestToken(t, otherPriv, good)
		if _, err := VerifyMCPToken(token, issuer, "erun-mcp", now); err == nil {
			t.Fatal("expected a token signed by a different key to be rejected")
		}
	})

	t.Run("expired", func(t *testing.T) {
		token := signTestToken(t, priv, MCPTokenClaims{Issuer: issuer, ExpiresAt: now.Add(-time.Minute).Unix()})
		if _, err := VerifyMCPToken(token, issuer, "", now); err == nil {
			t.Fatal("expected an expired token to be rejected")
		}
	})

	t.Run("issuer not the trusted issuer", func(t *testing.T) {
		token := signTestToken(t, priv, MCPTokenClaims{Issuer: "file:///some/other/key.pub", ExpiresAt: now.Add(time.Hour).Unix()})
		if _, err := VerifyMCPToken(token, issuer, "", now); err == nil {
			t.Fatal("expected a token whose issuer differs from the trusted issuer to be rejected")
		}
	})

	t.Run("wrong audience", func(t *testing.T) {
		token := signTestToken(t, priv, good)
		if _, err := VerifyMCPToken(token, issuer, "some-other-audience", now); err == nil {
			t.Fatal("expected an audience mismatch to be rejected")
		}
	})

	t.Run("alg confusion rejected", func(t *testing.T) {
		// Forge a token with alg=none and an empty signature: a verifier that
		// honoured the header's alg would accept it. Ours must not.
		header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
		payload, _ := json.Marshal(good)
		forged := header + "." + base64.RawURLEncoding.EncodeToString(payload) + "."
		if _, err := VerifyMCPToken(forged, issuer, "", now); err == nil {
			t.Fatal("expected an alg=none token to be rejected")
		}
	})

	t.Run("no trusted issuer configured", func(t *testing.T) {
		token := signTestToken(t, priv, good)
		if _, err := VerifyMCPToken(token, "", "", now); err == nil {
			t.Fatal("expected verification with no trusted issuer to fail closed")
		}
	})
}
