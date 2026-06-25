// Package mcptoken mints the per-env MCP bearer tokens the hosted console
// presents to an environment's erun-mcp edge (issue #686). It is the hosted
// analog of the desktop's identity (erun-ui/mcp_identity.go): the backend holds
// an Ed25519 signing key, signs a short-lived OIDC-compatible JWT whose `iss`
// points to where its public key lives (a file:// path inside the env's MCP
// pod, which the deploy injects), and stamps the per-env audience the edge
// enforces. The edge verifies it with the same unified verifier the desktop
// path uses (eruncommon.VerifyMCPToken / issue #656) — file:// is not a separate
// scheme, just "the verification key is on local disk."
package mcptoken

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// defaultTokenTTL keeps the per-env bearer short-lived so a leaked token has a
// small window; the backend signs a fresh one per request (matching the
// desktop's 5-minute TTL).
const defaultTokenTTL = 5 * time.Minute

// Signer holds the backend's Ed25519 MCP-signing identity and mints per-env
// tokens. The private key is loaded from keyPath, generated+persisted there on
// first use (0600), exactly like the desktop persists its identity. issuer is
// the file:// URL the env's MCP edge trusts (where the public key is mounted in
// the pod); it defaults to the fixed in-pod convention so the signer and the
// chart's ERUN_MCP_TRUSTED_ISSUER always agree.
type Signer struct {
	keyPath string
	issuer  string
	ttl     time.Duration

	mu         sync.Mutex
	privatePEM []byte
}

// NewSigner builds the signer for a key persisted at keyPath. issuer defaults to
// eruncommon.DesktopMCPIssuer() (the fixed in-pod public-key path the runtime
// chart mounts and the edge loads the key from); tests override it to point at a
// temp key file.
func NewSigner(keyPath string) *Signer {
	return &Signer{
		keyPath: strings.TrimSpace(keyPath),
		issuer:  eruncommon.DesktopMCPIssuer(),
		ttl:     defaultTokenTTL,
	}
}

// ensure loads the persisted private key, generating + persisting a fresh
// keypair on first use. Safe for concurrent callers.
func (s *Signer) ensure() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.privatePEM) > 0 {
		return nil
	}
	if s.keyPath == "" {
		return fmt.Errorf("mcp signing key path is unset")
	}
	switch data, err := os.ReadFile(s.keyPath); {
	case err == nil:
		if _, derr := eruncommon.DesktopPublicKeyPEM(data); derr != nil {
			return fmt.Errorf("persisted mcp signing key is invalid: %w", derr)
		}
		s.privatePEM = data
	case os.IsNotExist(err):
		priv, _, gerr := eruncommon.GenerateDesktopIdentity()
		if gerr != nil {
			return gerr
		}
		if mkErr := os.MkdirAll(filepath.Dir(s.keyPath), 0o700); mkErr != nil {
			return fmt.Errorf("create mcp signing key dir: %w", mkErr)
		}
		if wErr := os.WriteFile(s.keyPath, priv, 0o600); wErr != nil {
			return fmt.Errorf("write mcp signing key: %w", wErr)
		}
		s.privatePEM = priv
	default:
		return fmt.Errorf("read mcp signing key: %w", err)
	}
	return nil
}

// PublicKeyPEM returns the PKIX public key the deploy injects into the env's MCP
// pod so the edge can verify tokens this signer mints.
func (s *Signer) PublicKeyPEM() ([]byte, error) {
	if err := s.ensure(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	priv := s.privatePEM
	s.mu.Unlock()
	return eruncommon.DesktopPublicKeyPEM(priv)
}

// Issuer is the file:// issuer this signer stamps (and the edge must trust).
func (s *Signer) Issuer() string { return s.issuer }

// Sign mints a short-lived OIDC-compatible MCP token for (tenant, environment),
// scoped to subject (the ERun user id). The audience is the stable per-env value
// the edge enforces, so a token for one env cannot be replayed against another.
func (s *Signer) Sign(subject, tenant, environment string, now time.Time) (string, error) {
	if err := s.ensure(); err != nil {
		return "", err
	}
	s.mu.Lock()
	priv := s.privatePEM
	s.mu.Unlock()
	return eruncommon.SignMCPToken(priv, eruncommon.MCPTokenClaims{
		Issuer:    s.issuer,
		Subject:   strings.TrimSpace(subject),
		Audience:  eruncommon.MCPTokenAudience(tenant, environment),
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(s.ttl).Unix(),
	})
}
