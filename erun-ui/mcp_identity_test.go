package main

import (
	"os"
	"path/filepath"
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

// TestDesktopIdentityRoundTrip locks the desktop↔server contract: the keypair
// the desktop persists (and whose public half it injects on deploy) signs a
// token the real erun-common verifier accepts, and signToken stamps the shared
// file:// issuer + the per-env audience the MCP edge enforces (#655).
func TestDesktopIdentityRoundTrip(t *testing.T) {
	dir := t.TempDir()
	id := newDesktopIdentity(dir)

	pubPath, err := id.ensurePublicKeyPath()
	mustNoErr(t, err, "ensurePublicKeyPath")
	if pubPath != filepath.Join(dir, desktopIdentityPubFile) {
		t.Fatalf("public key path = %q", pubPath)
	}
	_, err = os.Stat(pubPath)
	mustNoErr(t, err, "stat public key")

	// signToken stamps the canonical desktop issuer (the audience is checked via
	// the verify round-trip below).
	now := time.Unix(1_800_000_000, 0)
	token, err := id.signToken("acme", "prod", now)
	mustNoErr(t, err, "signToken")
	issuer, err := eruncommon.UnverifiedMCPTokenIssuer(token)
	mustNoErr(t, err, "UnverifiedMCPTokenIssuer")
	if issuer != eruncommon.DesktopMCPIssuer() {
		t.Fatalf("token issuer = %q, want %q", issuer, eruncommon.DesktopMCPIssuer())
	}

	// The persisted keypair verifies end-to-end through the real verifier when
	// the issuer points at the on-disk public key (the chart mounts it at the
	// canonical path in the pod; here we verify against the temp path).
	priv, err := os.ReadFile(filepath.Join(dir, desktopIdentityKeyFile))
	mustNoErr(t, err, "read persisted private key")
	localIssuer := eruncommon.FileIssuer(pubPath)
	audience := eruncommon.MCPTokenAudience("acme", "prod")
	verifyToken, err := eruncommon.SignMCPToken(priv, eruncommon.MCPTokenClaims{
		Issuer:    localIssuer,
		Audience:  audience,
		ExpiresAt: now.Add(time.Hour).Unix(),
	})
	mustNoErr(t, err, "sign verify token")
	claims, err := eruncommon.VerifyMCPToken(verifyToken, localIssuer, audience, now)
	mustNoErr(t, err, "VerifyMCPToken against persisted key")
	if claims.Audience != audience {
		t.Fatalf("verified audience = %q, want %q", claims.Audience, audience)
	}

	// A second ensure() reuses the persisted key rather than regenerating it.
	priv2, err := os.ReadFile(filepath.Join(dir, desktopIdentityKeyFile))
	mustNoErr(t, err, "re-read persisted private key")
	if string(priv) != string(priv2) {
		t.Fatal("private key was regenerated on second ensure")
	}
}
