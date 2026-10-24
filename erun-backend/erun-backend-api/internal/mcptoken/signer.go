// Package mcptoken mints per-env MCP bearer tokens that a deployed edge verifies
// against the backend's public key at the fixed file:// issuer path.
package mcptoken

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"time"

	"github.com/sophium/erun/erun-common/infra/mcpauth"
)

// The per-env edge is RCE-sensitive, so tokens are short-lived; the console re-mints.
const tokenTTL = time.Hour

// Signer holds the backend's Ed25519 signing identity.
type Signer struct {
	privatePEM []byte
	publicPEM  []byte
	publicKey  ed25519.PublicKey
}

// NewSigner validates privatePEM as an Ed25519 key and caches the public half.
func NewSigner(privatePEM []byte) (*Signer, error) {
	publicPEM, err := mcpauth.DesktopPublicKeyPEM(privatePEM)
	if err != nil {
		return nil, fmt.Errorf("mcp signing key: %w", err)
	}
	publicKey, err := parseEd25519PublicKey(publicPEM)
	if err != nil {
		return nil, fmt.Errorf("mcp signing key: %w", err)
	}
	return &Signer{privatePEM: privatePEM, publicPEM: publicPEM, publicKey: publicKey}, nil
}

// PublicKeyPEM returns the public key a hosted deploy injects so the MCP edge trusts this signer.
func (s *Signer) PublicKeyPEM() string {
	return string(s.publicPEM)
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

// Sign mints a per-env MCP token, returning it and its audience. The caller must
// validate and default scope: an absent scope reads as admin at the edge.
func (s *Signer) Sign(tenant, environment, subject, scope string, now time.Time) (string, string, error) {
	audience := mcpauth.MCPTokenAudience(tenant, environment)
	token, err := mcpauth.SignMCPToken(s.privatePEM, mcpauth.MCPTokenClaims{
		Issuer:    mcpauth.DesktopMCPIssuer(),
		Subject:   subject,
		Audience:  audience,
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(tokenTTL).Unix(),
		Scope:     scope,
	})
	if err != nil {
		return "", "", err
	}
	return token, audience, nil
}
