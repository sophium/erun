package backendapi

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// BearerTokenVerifier authenticates hosted-API bearer tokens, dispatching on the
// token's issuer. It trusts two kinds of issuer:
//
//   - the configured file:// desktop issuer, which lets a desktop-signed token
//     authenticate with no live IdP (what e2e tests rely on);
//   - any other issuer, verified as OIDC against the allow-list.
//
// Security invariant: it only ever trusts an issuer the deployment configured; it
// never loads a key from an arbitrary file:// supplied by the token. The tenant is
// resolved from the verified issuer downstream.
type BearerTokenVerifier struct {
	allowedIssuers    map[string]struct{}
	allowedAudiences  []string
	trustedFileIssuer string
	audience          string
	verifier          *eruncommon.OIDCVerifier
	now               func() time.Time
}

type oidcTokenClaims struct {
	Issuer            string       `json:"iss"`
	Subject           string       `json:"sub"`
	PreferredUsername string       `json:"preferred_username"`
	Username          string       `json:"username"`
	Email             string       `json:"email"`
	AWS               awsSTSClaims `json:"https://sts.amazonaws.com/"`
}

type awsSTSClaims struct {
	IdentityStoreUserID string `json:"identity_store_user_id"`
	SourceRegion        string `json:"source_region"`
}

// BearerTokenVerifierOptions configures the API verifier. An empty AllowedIssuers
// allows any issuer a token resolves to, and an empty AllowedAudiences likewise
// allows any audience an OIDC token carries; an empty Audience defaults to
// eruncommon.APITokenAudience, which only the desktop file:// path uses.
//
// AllowedAudiences is deliberately separate from Audience: the desktop mints its
// own tokens and can be held to erun's own audience, while a hosted IdP puts the
// registered client id in `aud`, so which clients may reach the API is a
// deployment fact rather than a constant.
type BearerTokenVerifierOptions struct {
	AllowedIssuers       []string
	AllowedAudiences     []string
	DesktopPublicKeyPath string
	Audience             string
}

func NewBearerTokenVerifier(options BearerTokenVerifierOptions) *BearerTokenVerifier {
	allowed := make(map[string]struct{}, len(options.AllowedIssuers))
	for _, issuer := range options.AllowedIssuers {
		if issuer = strings.TrimSpace(issuer); issuer != "" {
			allowed[issuer] = struct{}{}
		}
	}
	// Kept as an ordered slice rather than a set: a rejection names the expected
	// audiences, and an operator reading that message should see them in the
	// order they configured them.
	allowedAudiences := make([]string, 0, len(options.AllowedAudiences))
	for _, audience := range options.AllowedAudiences {
		if audience = strings.TrimSpace(audience); audience != "" {
			allowedAudiences = append(allowedAudiences, audience)
		}
	}
	audience := strings.TrimSpace(options.Audience)
	if audience == "" {
		audience = eruncommon.APITokenAudience
	}
	trustedFileIssuer := ""
	if path := strings.TrimSpace(options.DesktopPublicKeyPath); path != "" {
		trustedFileIssuer = eruncommon.FileIssuer(path)
	}
	return &BearerTokenVerifier{
		allowedIssuers:    allowed,
		allowedAudiences:  allowedAudiences,
		trustedFileIssuer: trustedFileIssuer,
		audience:          audience,
		verifier:          eruncommon.NewOIDCVerifier(),
		now:               time.Now,
	}
}

func (v *BearerTokenVerifier) VerifyBearerToken(ctx context.Context, token string) (Claims, error) {
	issuer, err := eruncommon.IssuerFromUnverifiedJWT(token)
	if err != nil {
		return Claims{}, err
	}

	// Enforcing the API audience here rejects a desktop token minted for a
	// different (e.g. MCP) audience, even though the same desktop key signed it.
	if v.trustedFileIssuer != "" && issuer == v.trustedFileIssuer {
		claims, fileErr := eruncommon.VerifyMCPToken(ctx, nil, token, v.trustedFileIssuer, v.audience, v.now())
		if fileErr != nil {
			return Claims{}, fileErr
		}
		return Claims{
			Issuer:  claims.Issuer,
			Subject: claims.Subject,
		}, nil
	}

	if len(v.allowedIssuers) > 0 {
		if _, ok := v.allowedIssuers[issuer]; !ok {
			return Claims{}, fmt.Errorf("oidc issuer is not allowed: %s", issuer)
		}
	}

	verified, err := v.verifier.Verify(ctx, issuer, token)
	if err != nil {
		return Claims{}, err
	}
	if err := v.checkOIDCAudience(verified.Audience); err != nil {
		return Claims{}, err
	}

	// Keep the raw claim map alongside the decoded struct: the identity resolver
	// reads a per-issuer org claim whose name is configured in the DB, not here.
	claims := oidcTokenClaimsFromRaw(verified.Raw)
	return claimsFromOIDCTokenClaims(claims, verified.Raw), nil
}

// checkOIDCAudience applies the API's audience policy to the OIDC path, the
// same policy the desktop path above already applies to its own tokens. The
// issuer allow-list cannot separate two clients of one shared IdP; `aud` is the
// claim that draws that boundary, so a token an issuer minted for another of its
// clients is not an API bearer token.
//
// An unconfigured allow-list accepts any audience, matching the issuer
// allow-list's empty-means-any rule — the startup log is what says which of the
// two is in force, so the permissive default is never silent. A rejection names
// the carried and expected audiences (never anything else from the token),
// because a generic auth failure turns a one-line config problem into a long
// debugging session.
func (v *BearerTokenVerifier) checkOIDCAudience(audiences []string) error {
	if len(v.allowedAudiences) == 0 {
		return nil
	}
	for _, audience := range audiences {
		if slices.Contains(v.allowedAudiences, strings.TrimSpace(audience)) {
			return nil
		}
	}
	// A token that names no audience cannot intersect an explicit allow-list, so
	// it is refused for the same reason a wrong one is; it gets its own message
	// because "[] is not allowed" reads as a bug rather than a diagnosis.
	if len(audiences) == 0 {
		return fmt.Errorf("oidc token carries no audience; expected one of %v", v.allowedAudiences)
	}
	return fmt.Errorf("oidc token audience %v is not allowed; expected one of %v", audiences, v.allowedAudiences)
}

func oidcTokenClaimsFromRaw(raw map[string]any) oidcTokenClaims {
	claims := oidcTokenClaims{
		Issuer:            stringClaim(raw, "iss"),
		Subject:           stringClaim(raw, "sub"),
		PreferredUsername: stringClaim(raw, "preferred_username"),
		Username:          stringClaim(raw, "username"),
		Email:             stringClaim(raw, "email"),
	}
	if aws, ok := raw["https://sts.amazonaws.com/"].(map[string]any); ok {
		claims.AWS = awsSTSClaims{
			IdentityStoreUserID: stringClaim(aws, "identity_store_user_id"),
			SourceRegion:        stringClaim(aws, "source_region"),
		}
	}
	return claims
}

func stringClaim(raw map[string]any, key string) string {
	if value, ok := raw[key].(string); ok {
		return value
	}
	return ""
}

func claimsFromOIDCTokenClaims(claims oidcTokenClaims, raw map[string]any) Claims {
	username := strings.TrimSpace(claims.PreferredUsername)
	if username == "" {
		username = strings.TrimSpace(claims.Username)
	}
	if username == "" {
		username = strings.TrimSpace(claims.Email)
	}
	subject := strings.TrimSpace(claims.Subject)
	if identityStoreUserID := strings.TrimSpace(claims.AWS.IdentityStoreUserID); identityStoreUserID != "" {
		subject = identityStoreUserID
	}
	return Claims{
		Issuer:   strings.TrimSpace(claims.Issuer),
		Subject:  subject,
		Username: username,
		Raw:      raw,
	}
}
