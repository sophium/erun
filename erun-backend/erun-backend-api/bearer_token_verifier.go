package backendapi

import (
	"context"
	"fmt"
	"strings"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// BearerTokenVerifier authenticates hosted-API bearer tokens. Like the per-env
// MCP edge, it is always authenticated via the token's `iss` and
// trusts two kinds of issuer through one verification flow,
// dispatching on the issuer the token claims:
//
//   - the configured `file://<path>` desktop issuer → the Ed25519 desktop path:
//     EdDSA is hard-checked, the public key is loaded from the trusted path, and
//     the APITokenAudience is enforced. This is what e2e tests use — a desktop-
//     signed token authenticates with no live IdP, exactly as for the MCP edge.
//   - any other issuer → an OIDC issuer: signature/JWKS verification is delegated
//     to the shared eruncommon.OIDCVerifier (the same verifier the MCP edge
//     uses), constrained by the issuer allow-list.
//
// In both branches the verifier only ever trusts an issuer the deployment
// configured (the file issuer, or the allow-list / any resolvable OIDC issuer);
// it never loads a key from an arbitrary `file://` supplied by the token. The
// tenant is resolved from the verified issuer downstream by the identity layer.
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

// BearerTokenVerifierOptions configures the API verifier. AllowedIssuers is the
// OIDC issuer allow-list (empty allows any issuer a token resolves to).
// DesktopPublicKeyPath, when set, is the path the desktop public key is mounted
// at; the API derives the trusted `file://<path>` issuer from it and verifies
// desktop-signed tokens against that key. Audience is the audience enforced on
// the file:// path (defaults to eruncommon.APITokenAudience).
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

	// file:// desktop path: only when the token claims the exact issuer the
	// deployment configured to trust. Reuses the shared edge verifier (oidc=nil —
	// the file:// branch never consults JWKS) with the API audience enforced, so
	// a desktop token minted for an MCP env (a different audience) is rejected.
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
