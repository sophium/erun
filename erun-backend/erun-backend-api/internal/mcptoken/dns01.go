package mcptoken

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// dns01TokenTTL is long-lived on purpose: a DNS-01 token is injected once at edge
// setup and must keep working across cert renewals (~60-day cadence). Its blast
// radius is bounded not by expiry but by the broker, which authorizes every
// write against the token's own tenant subzone.
const dns01TokenTTL = 365 * 24 * time.Hour

const dns01AudiencePrefix = "erun-dns01:"

// DNS01Audience is the per-env audience a DNS-01 broker token carries and the
// broker authorizes against. Deliberately distinct from the MCP audience so a
// token minted for one capability cannot be replayed against the other.
func DNS01Audience(tenant, environment string) string {
	return dns01AudiencePrefix + strings.TrimSpace(tenant) + "/" + strings.TrimSpace(environment)
}

// ParseDNS01Audience extracts (tenant, environment) from a DNS-01 audience,
// reporting ok=false for anything that is not a well-formed erun-dns01 audience —
// including an MCP-audience token presented to the broker.
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

// SignDNS01 mints a long-lived per-env DNS-01 token the webhook shim presents to
// the broker. The broker self-verifies it (VerifyDNS01) and authorizes writes
// against the (tenant, environment) encoded in its audience.
func (s *Signer) SignDNS01(tenant, environment string, now time.Time) (string, string, error) {
	audience := DNS01Audience(tenant, environment)
	// M2M token: the subject is the env identity, not an ERun user.
	token, err := eruncommon.SignMCPToken(s.privatePEM, eruncommon.MCPTokenClaims{
		Issuer:    eruncommon.DesktopMCPIssuer(),
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

// VerifyDNS01 verifies a DNS-01 token against the backend's own public key — the
// backend both signs and verifies these, so there is no file:// or JWKS
// indirection — enforcing the EdDSA alg lock and expiry, and returns the
// (tenant, environment) the token authorizes. A malformed, expired, wrong-key,
// or non-erun-dns01 token is rejected.
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

// parseEdDSAToken splits and decodes an EdDSA JWT, hard-requiring alg=EdDSA
// (closing the alg-confusion class) and returning the signing input, claims, and
// raw signature for the caller to verify against a known key. It duplicates a
// little of erun-common's file:// parse deliberately: the DNS-01 path is
// backend-only and lives here rather than widening erun-common (whose coverage
// is gated by the CLI integration suite, which never reaches this code).
func parseEdDSAToken(token string) (signingInput string, claims eruncommon.MCPTokenClaims, signature []byte, err error) {
	segments := strings.Split(strings.TrimSpace(token), ".")
	if len(segments) != 3 {
		return "", eruncommon.MCPTokenClaims{}, nil, fmt.Errorf("malformed token: expected 3 JWT segments")
	}
	var header eddsaHeader
	if err := decodeSegment(segments[0], &header); err != nil {
		return "", eruncommon.MCPTokenClaims{}, nil, fmt.Errorf("decode token header: %w", err)
	}
	if header.Algorithm != "EdDSA" {
		return "", eruncommon.MCPTokenClaims{}, nil, fmt.Errorf("unsupported token algorithm %q (only EdDSA)", header.Algorithm)
	}
	if err := decodeSegment(segments[1], &claims); err != nil {
		return "", eruncommon.MCPTokenClaims{}, nil, fmt.Errorf("decode token claims: %w", err)
	}
	signature, err = base64.RawURLEncoding.DecodeString(segments[2])
	if err != nil {
		return "", eruncommon.MCPTokenClaims{}, nil, fmt.Errorf("decode token signature: %w", err)
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
