package main

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/infra/mcpauth"
)

func mustNoErr(t *testing.T, err error, what string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

func TestDesktopIdentityRoundTrip(t *testing.T) {
	dir := t.TempDir()
	id := newDesktopIdentity(dir)

	pubPath, err := id.ensurePublicKeyPath()
	mustNoErr(t, err, "ensurePublicKeyPath")
	if pubPath != mcpauth.DesktopIdentityPublicKeyPath(dir) {
		t.Fatalf("public key path = %q", pubPath)
	}
	_, err = os.Stat(pubPath)
	mustNoErr(t, err, "stat public key")

	now := time.Unix(1_800_000_000, 0)
	token, err := id.signToken("acme", "prod", now)
	mustNoErr(t, err, "signToken")
	issuer, err := mcpauth.UnverifiedMCPTokenIssuer(token)
	mustNoErr(t, err, "UnverifiedMCPTokenIssuer")
	if issuer != mcpauth.DesktopMCPIssuer() {
		t.Fatalf("token issuer = %q, want %q", issuer, mcpauth.DesktopMCPIssuer())
	}

	priv, err := os.ReadFile(mcpauth.DesktopIdentityKeyPath(dir))
	mustNoErr(t, err, "read persisted private key")
	localIssuer := mcpauth.FileIssuer(pubPath)
	audience := mcpauth.MCPTokenAudience("acme", "prod")
	verifyToken, err := mcpauth.SignMCPToken(priv, mcpauth.MCPTokenClaims{
		Issuer:    localIssuer,
		Audience:  audience,
		ExpiresAt: now.Add(time.Hour).Unix(),
	})
	mustNoErr(t, err, "sign verify token")
	claims, err := mcpauth.VerifyMCPToken(context.Background(), nil, verifyToken, localIssuer, audience, now)
	mustNoErr(t, err, "VerifyMCPToken against persisted key")
	if claims.Audience != audience {
		t.Fatalf("verified audience = %q, want %q", claims.Audience, audience)
	}

	priv2, err := os.ReadFile(mcpauth.DesktopIdentityKeyPath(dir))
	mustNoErr(t, err, "re-read persisted private key")
	if string(priv) != string(priv2) {
		t.Fatal("private key was regenerated on second ensure")
	}
}
