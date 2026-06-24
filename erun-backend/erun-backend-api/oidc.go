package backendapi

import (
	"context"
	"fmt"
	"strings"

	eruncommon "github.com/sophium/erun/erun-common"
)

// OIDCTokenVerifier authenticates hosted-API bearer tokens. It owns the
// backend's issuer allow-list and the backend-specific claim mapping
// (username/AWS-STS subject), and delegates signature/JWKS verification to the
// shared eruncommon.OIDCVerifier (issue #656) so the backend API and the per-env
// MCP edge trust OIDC issuers through one verifier.
type OIDCTokenVerifier struct {
	allowedIssuers map[string]struct{}
	verifier       *eruncommon.OIDCVerifier
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

type OIDCTokenVerifierOptions struct {
	AllowedIssuers []string
}

func NewOIDCTokenVerifier(allowedIssuers []string) *OIDCTokenVerifier {
	return NewOIDCTokenVerifierWithOptions(OIDCTokenVerifierOptions{AllowedIssuers: allowedIssuers})
}

func NewOIDCTokenVerifierWithOptions(options OIDCTokenVerifierOptions) *OIDCTokenVerifier {
	allowed := make(map[string]struct{}, len(options.AllowedIssuers))
	for _, issuer := range options.AllowedIssuers {
		if issuer = strings.TrimSpace(issuer); issuer != "" {
			allowed[issuer] = struct{}{}
		}
	}
	return &OIDCTokenVerifier{
		allowedIssuers: allowed,
		verifier:       eruncommon.NewOIDCVerifier(),
	}
}

func (v *OIDCTokenVerifier) VerifyBearerToken(ctx context.Context, token string) (Claims, error) {
	issuer, err := eruncommon.IssuerFromUnverifiedJWT(token)
	if err != nil {
		return Claims{}, err
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

	// Decode the verified claim map into the backend's small struct for the
	// username/AWS-STS mapping; the raw map stays so the identity resolver can
	// read a per-issuer org claim whose name is configured in the DB, not here.
	claims := oidcTokenClaimsFromRaw(verified.Raw)
	return claimsFromOIDCTokenClaims(claims, verified.Raw), nil
}

// oidcTokenClaimsFromRaw reads the backend-specific claim subset out of the
// verified raw claim map. It mirrors the shape go-oidc's idToken.Claims would
// have produced, but sources from the already-verified map so the verifier owns
// the single decode.
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
