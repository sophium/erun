// Package mcptoken mints the per-env MCP bearer tokens the hosted console hands
// to callers. The backend is the hosted signer — the twin of the desktop
// signing locally — so a deployed env's erun-mcp edge, injected with the
// backend's public key at the fixed file:// issuer path, verifies these tokens
// exactly as it verifies desktop-signed ones, with no live IdP.
package mcptoken

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// tokenTTL bounds a minted MCP token's lifetime. The per-env edge is
// RCE-sensitive, so the window stays short; it is long enough for an interactive
// console session, and the console re-mints on demand.
const tokenTTL = time.Hour

// Signer holds the backend's Ed25519 MCP-signing identity. It mints per-env MCP
// tokens against the fixed in-pod file:// issuer the edge resolves its key from,
// and also mints and self-verifies per-env DNS-01 broker tokens (see dns01.go),
// which the backend both signs and checks with its own public half.
type Signer struct {
	privatePEM []byte
	publicKey  ed25519.PublicKey
}

// NewSigner validates the PEM parses as an Ed25519 private key so a misconfigured
// key fails at construction, not on the first mint, and caches the public half
// for self-verifying tokens the backend both signs and checks (DNS-01).
func NewSigner(privatePEM []byte) (*Signer, error) {
	publicPEM, err := eruncommon.DesktopPublicKeyPEM(privatePEM)
	if err != nil {
		return nil, fmt.Errorf("mcp signing key: %w", err)
	}
	publicKey, err := parseEd25519PublicKey(publicPEM)
	if err != nil {
		return nil, fmt.Errorf("mcp signing key: %w", err)
	}
	return &Signer{privatePEM: privatePEM, publicKey: publicKey}, nil
}

func parseEd25519PublicKey(publicPEM []byte) (ed25519.PublicKey, error) {
	block, _ := pem.Decode(publicPEM)
	if block == nil {
		return nil, fmt.Errorf("public key is not valid PEM")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse public key: %w", err)
	}
	publicKey, ok := parsed.(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("public key is not Ed25519")
	}
	return publicKey, nil
}

// Sign mints a token whose sub is the ERun user, aud is the per-env audience,
// and iss is the fixed in-pod file:// path the deploy injects the backend's
// public key at — so the edge verifies it against that key. Returns the token
// and the audience it was minted for.
func (s *Signer) Sign(tenant, environment, subject string, now time.Time) (string, string, error) {
	audience := eruncommon.MCPTokenAudience(tenant, environment)
	token, err := eruncommon.SignMCPToken(s.privatePEM, eruncommon.MCPTokenClaims{
		Issuer:    eruncommon.DesktopMCPIssuer(),
		Subject:   subject,
		Audience:  audience,
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(tokenTTL).Unix(),
	})
	if err != nil {
		return "", "", err
	}
	return token, audience, nil
}
