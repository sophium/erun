package backendapi

import (
	"context"
	"fmt"
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
// allows any issuer a token resolves to; an empty Audience defaults to
// eruncommon.APITokenAudience.
type BearerTokenVerifierOptions struct {
	AllowedIssuers       []string
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

	// Keep the raw claim map alongside the decoded struct: the identity resolver
	// reads a per-issuer org claim whose name is configured in the DB, not here.
	claims := oidcTokenClaimsFromRaw(verified.Raw)
	return claimsFromOIDCTokenClaims(claims, verified.Raw), nil
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
