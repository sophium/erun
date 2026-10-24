package mcptoken

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"strings"
	"time"
)

// A long push or pull re-authenticates rather than reusing a stale grant.
const registryTokenTTL = 5 * time.Minute

// RegistryTokenIssuer is distinct from any OIDC or file:// issuer.
const RegistryTokenIssuer = "erun-registry-token-service"

// RegistryAccessScope is one `access` entry in a registry v2 token.
// See https://distribution.github.io/distribution/spec/auth/token/.
type RegistryAccessScope struct {
	Type    string   `json:"type"`
	Name    string   `json:"name"`
	Actions []string `json:"actions"`
}

// ParseRegistryTokenScopes parses `scope` values ("type:name:action[,action]",
// space-separated). Malformed entries are dropped, which grants nothing.
func ParseRegistryTokenScopes(rawScopes []string) []RegistryAccessScope {
	var scopes []RegistryAccessScope
	for _, raw := range rawScopes {
		for _, entry := range strings.Fields(raw) {
			parts := strings.SplitN(entry, ":", 3)
			if len(parts) != 3 {
				continue
			}
			resourceType, name := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
			if resourceType == "" || name == "" {
				continue
			}
			var actions []string
			for _, action := range strings.Split(parts[2], ",") {
				if action = strings.TrimSpace(action); action != "" {
					actions = append(actions, action)
				}
			}
			if len(actions) == 0 {
				continue
			}
			scopes = append(scopes, RegistryAccessScope{Type: resourceType, Name: name, Actions: actions})
		}
	}
	return scopes
}

// ClampRegistryScopesToTenant is the registry's security boundary: it keeps only
// repository scopes in the tenant's own namespace and drops the rest entirely.
func ClampRegistryScopesToTenant(tenant string, requested []RegistryAccessScope) []RegistryAccessScope {
	tenant = strings.TrimSpace(tenant)
	if tenant == "" {
		return nil
	}
	var granted []RegistryAccessScope
	for _, scope := range requested {
		if scope.Type != "repository" {
			continue
		}
		if scope.Name != tenant && !strings.HasPrefix(scope.Name, tenant+"/") {
			continue
		}
		granted = append(granted, scope)
	}
	return granted
}

type registryTokenClaims struct {
	Issuer     string                `json:"iss"`
	Subject    string                `json:"sub"`
	Audience   string                `json:"aud"`
	Expiration int64                 `json:"exp"`
	NotBefore  int64                 `json:"nbf"`
	IssuedAt   int64                 `json:"iat"`
	Access     []RegistryAccessScope `json:"access"`
}

// SignRegistry mints a token for already-clamped access; empty access is valid.
// service becomes the audience, keeping the token from replaying against the
// API or an MCP edge that share the signing key.
func (s *Signer) SignRegistry(subject, service string, access []RegistryAccessScope, now time.Time) (string, error) {
	if strings.TrimSpace(service) == "" {
		return "", fmt.Errorf("registry token service (audience) is required")
	}
	privateKey, err := parseEd25519PrivateKeyPEM(s.privatePEM)
	if err != nil {
		return "", fmt.Errorf("registry signing key: %w", err)
	}
	claims := registryTokenClaims{
		Issuer:     RegistryTokenIssuer,
		Subject:    subject,
		Audience:   service,
		IssuedAt:   now.Unix(),
		NotBefore:  now.Unix(),
		Expiration: now.Add(registryTokenTTL).Unix(),
		Access:     access,
	}
	headerSegment, err := encodeSegment(eddsaHeader{Algorithm: "EdDSA", Type: "JWT"})
	if err != nil {
		return "", err
	}
	claimsSegment, err := encodeSegment(claims)
	if err != nil {
		return "", err
	}
	signingInput := headerSegment + "." + claimsSegment
	signature := ed25519.Sign(privateKey, []byte(signingInput))
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func parseEd25519PrivateKeyPEM(privatePEM []byte) (ed25519.PrivateKey, error) {
	block, _ := pem.Decode(privatePEM)
	if block == nil {
		return nil, fmt.Errorf("private key is not valid PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("private key is not Ed25519")
	}
	return key, nil
}

func encodeSegment(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}
