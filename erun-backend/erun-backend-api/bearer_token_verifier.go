package backendapi

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/infra/mcpauth"
)

// BearerTokenVerifier authenticates API bearer tokens by issuer: the configured
// file:// desktop issuer or an allow-listed OIDC issuer. It never trusts a file://
// key path supplied by the token.
type BearerTokenVerifier struct {
	allowedIssuers    map[string]struct{}
	allowedAudiences  []string
	trustedFileIssuer string
	audience          string
	verifier          *mcpauth.OIDCVerifier
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

// BearerTokenVerifierOptions configures the API verifier; empty allow-lists accept any value.
// AllowedAudiences is separate from Audience because a hosted IdP puts its client id in aud.
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
	// Ordered so a rejection lists audiences in configured order.
	allowedAudiences := make([]string, 0, len(options.AllowedAudiences))
	for _, audience := range options.AllowedAudiences {
		if audience = strings.TrimSpace(audience); audience != "" {
			allowedAudiences = append(allowedAudiences, audience)
		}
	}
	audience := strings.TrimSpace(options.Audience)
	if audience == "" {
		audience = mcpauth.APITokenAudience
	}
	trustedFileIssuer := ""
	if path := strings.TrimSpace(options.DesktopPublicKeyPath); path != "" {
		trustedFileIssuer = mcpauth.FileIssuer(path)
	}
	return &BearerTokenVerifier{
		allowedIssuers:    allowed,
		allowedAudiences:  allowedAudiences,
		trustedFileIssuer: trustedFileIssuer,
		audience:          audience,
		verifier:          mcpauth.NewOIDCVerifier(),
		now:               time.Now,
	}
}

func (v *BearerTokenVerifier) VerifyBearerToken(ctx context.Context, token string) (Claims, error) {
	issuer, err := mcpauth.IssuerFromUnverifiedJWT(token)
	if err != nil {
		return Claims{}, err
	}

	// Rejects a desktop token minted for another (e.g. MCP) audience.
	if v.trustedFileIssuer != "" && issuer == v.trustedFileIssuer {
		claims, fileErr := mcpauth.VerifyMCPToken(ctx, nil, token, v.trustedFileIssuer, v.audience, v.now())
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

	// The identity resolver reads a per-issuer org claim configured in the DB.
	claims := oidcTokenClaimsFromRaw(verified.Raw)
	return claimsFromOIDCTokenClaims(claims, verified.Raw), nil
}

// checkOIDCAudience separates clients of one shared IdP, which the issuer
// allow-list cannot. Rejections name only the audiences, to make config errors obvious.
func (v *BearerTokenVerifier) checkOIDCAudience(audiences []string) error {
	if len(v.allowedAudiences) == 0 {
		return nil
	}
	for _, audience := range audiences {
		if slices.Contains(v.allowedAudiences, strings.TrimSpace(audience)) {
			return nil
		}
	}
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
