package mcpauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"github.com/coreos/go-oidc/v3/oidc"
)

// Shared OIDC verifier for the hosted API and MCP edge. Issuer and audience policy belong to callers.

// OIDCClaims is a verified claim set; Raw exposes claims the struct does not name.
type OIDCClaims struct {
	Issuer   string
	Subject  string
	Audience []string
	Raw      map[string]any
}

// OIDCVerifier verifies tokens against their issuer's JWKS and is safe for concurrent use.
type OIDCVerifier struct {
	mu        sync.Mutex
	providers map[string]*oidc.Provider
}

// NewOIDCVerifier returns a ready-to-use verifier.
func NewOIDCVerifier() *OIDCVerifier {
	return &OIDCVerifier{providers: make(map[string]*oidc.Provider)}
}

// Verify checks token against issuer, skipping audience, which is caller policy.
func (v *OIDCVerifier) Verify(ctx context.Context, issuer, token string) (OIDCClaims, error) {
	issuer = strings.TrimSpace(issuer)
	if issuer == "" {
		return OIDCClaims{}, errors.New("oidc issuer is required")
	}
	provider, err := v.provider(ctx, issuer)
	if err != nil {
		return OIDCClaims{}, err
	}
	idToken, err := provider.Verifier(&oidc.Config{SkipClientIDCheck: true}).Verify(ctx, token)
	if err != nil {
		return OIDCClaims{}, err
	}
	var raw map[string]any
	if err := idToken.Claims(&raw); err != nil {
		return OIDCClaims{}, err
	}
	return OIDCClaims{
		Issuer:   idToken.Issuer,
		Subject:  idToken.Subject,
		Audience: append([]string(nil), idToken.Audience...),
		Raw:      raw,
	}, nil
}

// provider caches discovered providers; discovery runs outside the lock so a slow issuer blocks no one else.
func (v *OIDCVerifier) provider(ctx context.Context, issuer string) (*oidc.Provider, error) {
	v.mu.Lock()
	if provider := v.providers[issuer]; provider != nil {
		v.mu.Unlock()
		return provider, nil
	}
	v.mu.Unlock()

	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, err
	}

	v.mu.Lock()
	// Keep an entry a concurrent discovery stored so callers share one key set per issuer.
	if existing := v.providers[issuer]; existing != nil {
		provider = existing
	} else {
		v.providers[issuer] = provider
	}
	v.mu.Unlock()
	return provider, nil
}

// IssuerFromUnverifiedJWT reads `iss` without verifying, only to select a trusted issuer; it ignores `alg`.
func IssuerFromUnverifiedJWT(token string) (string, error) {
	var claims struct {
		Issuer string `json:"iss"`
	}
	if err := decodeUnverifiedJWTPayload(token, &claims); err != nil {
		return "", err
	}
	issuer := strings.TrimSpace(claims.Issuer)
	if issuer == "" {
		return "", errors.New("token issuer is empty")
	}
	return issuer, nil
}

// SubjectFromUnverifiedJWT reads `sub` without verifying, for prefilling forms, never for authorization.
func SubjectFromUnverifiedJWT(token string) (string, error) {
	var claims struct {
		Subject string `json:"sub"`
	}
	if err := decodeUnverifiedJWTPayload(token, &claims); err != nil {
		return "", err
	}
	subject := strings.TrimSpace(claims.Subject)
	if subject == "" {
		return "", errors.New("token subject is empty")
	}
	return subject, nil
}

func decodeUnverifiedJWTPayload(token string, claims any) error {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) < 2 {
		return errors.New("token is not a jwt")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return err
	}
	return json.Unmarshal(payload, claims)
}
