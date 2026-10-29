package mcptoken

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/infra/mcpauth"
)

// DNS-01 tokens are injected once and must survive cert renewals; the broker's
// per-subzone authorization bounds them, not expiry.
const dns01TokenTTL = 365 * 24 * time.Hour

const dns01AudiencePrefix = "erun-dns01:"

// DNS01Audience is the per-env broker audience, distinct from the MCP audience
// so tokens cannot be replayed across capabilities.
func DNS01Audience(tenant, environment string) string {
	return dns01AudiencePrefix + strings.TrimSpace(tenant) + "/" + strings.TrimSpace(environment)
}

// ParseDNS01Audience extracts tenant and environment from a DNS-01 audience.
func ParseDNS01Audience(audience string) (tenant, environment string, ok bool) {
	rest, found := strings.CutPrefix(strings.TrimSpace(audience), dns01AudiencePrefix)
	if !found {
		return "", "", false
	}
	tenant, environment, found = strings.Cut(rest, "/")
	if !found || tenant == "" || environment == "" {
		return "", "", false
	}
	return tenant, environment, true
}

// SignDNS01 mints a long-lived per-env DNS-01 broker token.
func (s *Signer) SignDNS01(tenant, environment string, now time.Time) (string, string, error) {
	audience := DNS01Audience(tenant, environment)
	token, err := mcpauth.SignMCPToken(s.privatePEM, mcpauth.MCPTokenClaims{
		Issuer:    mcpauth.DesktopMCPIssuer(),
		Subject:   audience,
		Audience:  audience,
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(dns01TokenTTL).Unix(),
	})
	if err != nil {
		return "", "", err
	}
	return token, audience, nil
}

// VerifyDNS01 verifies a DNS-01 token against the backend's own public key.
func (s *Signer) VerifyDNS01(token string, now time.Time) (tenant, environment string, err error) {
	signingInput, claims, signature, err := parseEdDSAToken(token)
	if err != nil {
		return "", "", err
	}
	if !ed25519.Verify(s.publicKey, []byte(signingInput), signature) {
		return "", "", fmt.Errorf("dns01 token signature is not valid")
	}
	if claims.ExpiresAt != 0 && now.After(time.Unix(claims.ExpiresAt, 0)) {
		return "", "", fmt.Errorf("dns01 token expired")
	}
	tenant, environment, ok := ParseDNS01Audience(claims.Audience)
	if !ok {
		return "", "", fmt.Errorf("token audience %q is not a dns01 audience", claims.Audience)
	}
	return tenant, environment, nil
}

type eddsaHeader struct {
	Algorithm string `json:"alg"`
	Type      string `json:"typ"`
}

// parseEdDSAToken decodes a JWT, requiring alg=EdDSA to block alg confusion.
func parseEdDSAToken(token string) (signingInput string, claims mcpauth.MCPTokenClaims, signature []byte, err error) {
	segments := strings.SplitN(strings.TrimSpace(token), ".", 4)
	if len(segments) != 3 {
		return "", mcpauth.MCPTokenClaims{}, nil, fmt.Errorf("malformed token: expected 3 JWT segments")
	}
	var header eddsaHeader
	if err := decodeSegment(segments[0], &header); err != nil {
		return "", mcpauth.MCPTokenClaims{}, nil, fmt.Errorf("decode token header: %w", err)
	}
	if header.Algorithm != "EdDSA" {
		return "", mcpauth.MCPTokenClaims{}, nil, fmt.Errorf("unsupported token algorithm %q (only EdDSA)", header.Algorithm)
	}
	if err := decodeSegment(segments[1], &claims); err != nil {
		return "", mcpauth.MCPTokenClaims{}, nil, fmt.Errorf("decode token claims: %w", err)
	}
	signature, err = base64.RawURLEncoding.DecodeString(segments[2])
	if err != nil {
		return "", mcpauth.MCPTokenClaims{}, nil, fmt.Errorf("decode token signature: %w", err)
	}
	return segments[0] + "." + segments[1], claims, signature, nil
}

func decodeSegment(segment string, target any) error {
	raw, err := base64.RawURLEncoding.DecodeString(segment)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, target)
}
