package erunmcp

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/infra/mcpauth"
)

// identityWithScopedToken mints a token with scope; empty scope resolves to admin.
func identityWithScopedToken(t *testing.T, scope string) (issuer, token string) {
	t.Helper()
	priv, pub, err := mcpauth.GenerateDesktopIdentity()
	if err != nil {
		t.Fatalf("generate identity: %v", err)
	}
	path := filepath.Join(t.TempDir(), "desktopid.pub")
	if err := os.WriteFile(path, pub, 0o600); err != nil {
		t.Fatalf("write public key: %v", err)
	}
	issuer = mcpauth.FileIssuer(path)
	token, err = mcpauth.SignMCPToken(priv, mcpauth.MCPTokenClaims{
		Issuer:    issuer,
		Audience:  "erun-mcp",
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
		Scope:     scope,
	})
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return issuer, token
}
