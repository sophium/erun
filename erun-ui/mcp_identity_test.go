package main

import (
	"context"
	"os"
	"testing"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

func mustNoErr(t *testing.T, err error, what string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

// TestDesktopIdentityRoundTrip locks the desktop↔server contract: the keypair the
// desktop persists signs a token the real erun-common verifier accepts, under the
// shared issuer and per-env audience the MCP edge enforces.
func TestDesktopIdentityRoundTrip(t *testing.T) {
	dir := t.TempDir()
	id := newDesktopIdentity(dir)

	pubPath, err := id.ensurePublicKeyPath()
	mustNoErr(t, err, "ensurePublicKeyPath")
	if pubPath != eruncommon.DesktopIdentityPublicKeyPath(dir) {
		t.Fatalf("public key path = %q", pubPath)
	}
	_, err = os.Stat(pubPath)
	mustNoErr(t, err, "stat public key")

	now := time.Unix(1_800_000_000, 0)
	token, err := id.signToken("acme", "prod", now)
	mustNoErr(t, err, "signToken")
	issuer, err := eruncommon.UnverifiedMCPTokenIssuer(token)
	mustNoErr(t, err, "UnverifiedMCPTokenIssuer")
	if issuer != eruncommon.DesktopMCPIssuer() {
		t.Fatalf("token issuer = %q, want %q", issuer, eruncommon.DesktopMCPIssuer())
	}

	priv, err := os.ReadFile(eruncommon.DesktopIdentityKeyPath(dir))
	mustNoErr(t, err, "read persisted private key")
	localIssuer := eruncommon.FileIssuer(pubPath)
	audience := eruncommon.MCPTokenAudience("acme", "prod")
	verifyToken, err := eruncommon.SignMCPToken(priv, eruncommon.MCPTokenClaims{
		Issuer:    localIssuer,
		Audience:  audience,
		ExpiresAt: now.Add(time.Hour).Unix(),
	})
	mustNoErr(t, err, "sign verify token")
	claims, err := eruncommon.VerifyMCPToken(context.Background(), nil, verifyToken, localIssuer, audience, now)
	mustNoErr(t, err, "VerifyMCPToken against persisted key")
	if claims.Audience != audience {
		t.Fatalf("verified audience = %q, want %q", claims.Audience, audience)
	}

	priv2, err := os.ReadFile(eruncommon.DesktopIdentityKeyPath(dir))
	mustNoErr(t, err, "re-read persisted private key")
	if string(priv) != string(priv2) {
		t.Fatal("private key was regenerated on second ensure")
	}
}
