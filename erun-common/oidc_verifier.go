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

// Shared OIDC verifier. This is the single signature/JWKS verifier
// used by both the hosted backend API and the per-env MCP edge to trust OIDC
// issuers (e.g. a Zitadel or AWS STS `https://` issuer). It is transport- and
// policy-agnostic: it verifies the JWT signature against the issuer's published
// JWKS and the standard time claims (exp/iat/nbf), and returns the decoded
// claims. It does NOT decide whether an issuer is allowed or whether an audience
// is acceptable — those are caller policy (the backend keeps its issuer
// allow-list and username/AWS-STS mapping; the MCP edge enforces its per-env
// audience). Audience is therefore not checked here.

// OIDCClaims is the decoded, signature-verified claim set the shared verifier
// returns. Raw carries the full claim map so callers can read claims the small
// struct does not name (a per-issuer org claim, AWS STS fields, etc.) without
// the verifier baking in any backend- or transport-specific knowledge.
type OIDCClaims struct {
	Issuer   string
	Subject  string
	Audience []string
	Raw      map[string]any
}

// OIDCVerifier verifies OIDC ID/access tokens against their issuer's JWKS. It
// caches one *oidc.Provider per issuer (the provider holds the JWKS key set and
// refreshes it as keys rotate), discovering the issuer lazily on first use. It
// is safe for concurrent use.
type OIDCVerifier struct {
	mu        sync.Mutex
	providers map[string]*oidc.Provider
}

// NewOIDCVerifier returns a verifier with an empty provider cache. Providers are
// discovered lazily on first Verify for each issuer.
func NewOIDCVerifier() *OIDCVerifier {
	return &OIDCVerifier{providers: make(map[string]*oidc.Provider)}
}

// Verify verifies token against issuer: it fetches (and caches) the issuer's
// OIDC discovery + JWKS, checks the signature and the standard time claims
// (exp/iat/nbf), and confirms the token's `iss` matches issuer. The client-id
// (audience) check is intentionally skipped here — audience policy belongs to
// the caller — so callers that need audience enforcement read OIDCClaims.Audience
// themselves. It returns the decoded claims or a descriptive error.
func (v *OIDCVerifier) Verify(ctx context.Context, issuer, token string) (OIDCClaims, error) {
	issuer = strings.TrimSpace(issuer)
	if issuer == "" {
		return OIDCClaims{}, errors.New("oidc issuer is required")
	}
	provider, err := v.provider(ctx, issuer)
	if err != nil {
		return OIDCClaims{}, err
	}
	// SkipClientIDCheck: the audience is the caller's policy, not this verifier's.
	// go-oidc still verifies the signature against the issuer's JWKS and the
	// exp/iat/nbf time claims.
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

// provider returns the cached *oidc.Provider for issuer, discovering it lazily
// on first use. Discovery (the network fetch of the issuer's
// .well-known/openid-configuration) happens outside the lock so a slow issuer
// cannot block verification for other issuers.
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

// IssuerFromUnverifiedJWT extracts the `iss` claim from a JWT WITHOUT verifying
// its signature, alg-agnostically: it splits the JWT, base64url-decodes the
// payload, and reads `iss`. It is used only to SELECT which trusted issuer a
// token claims to come from (and therefore which key set to verify against);
// the signature is then verified against that issuer's JWKS. Because it does not
// look at the JWS header's `alg`, it works for any signing algorithm (RS256,
// ES256, EdDSA, …) — unlike an alg-locked parser.
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
