package backendapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/coreos/go-oidc/v3/oidc"
)

type OIDCTokenVerifier struct {
	allowedIssuers map[string]struct{}
	providers      map[string]*oidc.Provider
	verifiers      map[string]*oidc.IDTokenVerifier
	mu             sync.Mutex
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
		providers:      make(map[string]*oidc.Provider),
		verifiers:      make(map[string]*oidc.IDTokenVerifier),
	}
}

func (v *OIDCTokenVerifier) VerifyBearerToken(ctx context.Context, token string) (Claims, error) {
	issuer, err := issuerFromJWT(token)
	if err != nil {
		return Claims{}, err
	}
	if len(v.allowedIssuers) > 0 {
		if _, ok := v.allowedIssuers[issuer]; !ok {
			return Claims{}, fmt.Errorf("oidc issuer is not allowed: %s", issuer)
		}
	}

	verifier, err := v.verifier(ctx, issuer)
	if err != nil {
		return Claims{}, err
	}
	idToken, err := verifier.Verify(ctx, token)
	if err != nil {
		return Claims{}, err
	}

	var claims oidcTokenClaims
	if err := idToken.Claims(&claims); err != nil {
		return Claims{}, err
	}
	// Also capture the full claim set as a map so the identity resolver can read
	// a per-issuer org claim (issuers.org_field_key) whose name is not known here
	// — it is configured per issuer in the DB, not baked into the verifier.
	var raw map[string]any
	if err := idToken.Claims(&raw); err != nil {
		return Claims{}, err
	}
	return claimsFromOIDCTokenClaims(claims, raw), nil
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

func (v *OIDCTokenVerifier) verifier(ctx context.Context, issuer string) (*oidc.IDTokenVerifier, error) {
	v.mu.Lock()
	if verifier := v.verifiers[issuer]; verifier != nil {
		v.mu.Unlock()
		return verifier, nil
	}
	v.mu.Unlock()

	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, err
	}
	verifier := provider.Verifier(&oidc.Config{SkipClientIDCheck: true})

	v.mu.Lock()
	v.providers[issuer] = provider
	v.verifiers[issuer] = verifier
	v.mu.Unlock()
	return verifier, nil
}

func issuerFromJWT(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return "", errors.New("token is not a jwt")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", err
	}
	var claims oidcTokenClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", err
	}
	issuer := strings.TrimSpace(claims.Issuer)
	if issuer == "" {
		return "", errors.New("token issuer is empty")
	}
	return issuer, nil
}
