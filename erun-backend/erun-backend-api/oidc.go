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

func NewOIDCTokenVerifier(allowedIssuers []string) *OIDCTokenVerifier {
	allowed := make(map[string]struct{}, len(allowedIssuers))
	for _, issuer := range allowedIssuers {
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

	var claims struct {
		Issuer            string `json:"iss"`
		Subject           string `json:"sub"`
		PreferredUsername string `json:"preferred_username"`
		Username          string `json:"username"`
		Email             string `json:"email"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return Claims{}, err
	}
	username := strings.TrimSpace(claims.PreferredUsername)
	if username == "" {
		username = strings.TrimSpace(claims.Username)
	}
	if username == "" {
		username = strings.TrimSpace(claims.Email)
	}
	return Claims{
		Issuer:   strings.TrimSpace(claims.Issuer),
		Subject:  strings.TrimSpace(claims.Subject),
		Username: username,
	}, nil
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
	var claims struct {
		Issuer string `json:"iss"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", err
	}
	issuer := strings.TrimSpace(claims.Issuer)
	if issuer == "" {
		return "", errors.New("token issuer is required")
	}
	return issuer, nil
}
