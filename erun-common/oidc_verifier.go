package eruncommon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"github.com/coreos/go-oidc/v3/oidc"
)

// Shared OIDC verifier used by both the hosted backend API and the per-env MCP
// edge. It is policy-agnostic: it does not decide whether an issuer is allowed
// or an audience is acceptable — those are caller policy, so audience is
// deliberately not checked here.

// OIDCClaims is the decoded, signature-verified claim set the shared verifier
// returns. Raw carries the full claim map so callers can read claims the struct
// does not name without the verifier baking in backend- or transport-specific
// knowledge.
type OIDCClaims struct {
	Issuer   string
	Subject  string
	Audience []string
	Raw      map[string]any
}

// OIDCVerifier verifies OIDC ID/access tokens against their issuer's JWKS. It is
// safe for concurrent use.
type OIDCVerifier struct {
	mu        sync.Mutex
	providers map[string]*oidc.Provider
}

// NewOIDCVerifier returns a ready-to-use verifier.
func NewOIDCVerifier() *OIDCVerifier {
	return &OIDCVerifier{providers: make(map[string]*oidc.Provider)}
}

// Verify verifies token against issuer and returns its decoded claims. The
// audience check is intentionally skipped — audience policy belongs to the
// caller, which reads OIDCClaims.Audience itself.
func (v *OIDCVerifier) Verify(ctx context.Context, issuer, token string) (OIDCClaims, error) {
	issuer = strings.TrimSpace(issuer)
	if issuer == "" {
		return OIDCClaims{}, errors.New("oidc issuer is required")
	}
	provider, err := v.provider(ctx, issuer)
	if err != nil {
		return OIDCClaims{}, err
	}
	// SkipClientIDCheck skips only the audience; signature and exp/iat/nbf are still verified.
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

// provider discovers each issuer's provider lazily and caches it. Discovery runs
// outside the lock so a slow issuer cannot block verification for others.
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
	// Another goroutine may have populated the cache while we were discovering;
	// keep whichever is already there so callers share one key set per issuer.
	if existing := v.providers[issuer]; existing != nil {
		provider = existing
	} else {
		v.providers[issuer] = provider
	}
	v.mu.Unlock()
	return provider, nil
}

// IssuerFromUnverifiedJWT reads the `iss` claim from a JWT WITHOUT verifying its
// signature. It is used only to SELECT which trusted issuer a token claims to
// come from; the signature is then verified against that issuer's JWKS. It stays
// alg-agnostic (never inspecting the JWS `alg` header) so issuer selection works
// for any signing algorithm.
func IssuerFromUnverifiedJWT(token string) (string, error) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) < 2 {
		return "", errors.New("token is not a jwt")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", err
	}
	var claims struct {
		Issuer string `json:"iss"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", err
	}
	issuer := strings.TrimSpace(claims.Issuer)
	if issuer == "" {
		return "", errors.New("token issuer is empty")
	}
	return issuer, nil
}
