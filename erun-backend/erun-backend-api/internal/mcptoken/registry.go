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

// Hosted-registry token minting. This is backend-local rather than in
// erun-common: its only caller is this module's own /v2/token route
// (internal/registrytoken), so it stays beside the signing key it uses,
// mirroring how dns01.go's parseEdDSAToken/eddsaHeader duplicate a little of
// erun-common's JWT parsing rather than widening that shared module for a
// single caller.
//
// registryTokenTTL bounds a minted hosted-registry access token's lifetime.
// Short-lived per the registry token spec's own intent: a long push or pull
// re-authenticates against the same Basic credentials rather than reusing a
// stale grant.
const registryTokenTTL = 5 * time.Minute

// RegistryTokenIssuer is the fixed `iss` claim on every registry token erun
// mints — an identity of its own, never an OIDC issuer or the file:// desktop
// issuer, so a registry token can never be confused for either at the
// registry.
const RegistryTokenIssuer = "erun-registry-token-service"

// RegistryAccessScope is one `access` entry in a registry v2 token: a set of
// actions on a named resource of a type. Almost every real request is
// type "repository", e.g. {"repository", "frs/hello", []string{"pull","push"}}.
// See https://distribution.github.io/distribution/spec/auth/token/.
type RegistryAccessScope struct {
	Type    string   `json:"type"`
	Name    string   `json:"name"`
	Actions []string `json:"actions"`
}

// ParseRegistryTokenScopes parses the token request's repeatable `scope`
// query parameter into access scopes. Each raw value may itself carry several
// space-separated scope strings (some clients send one `scope` param with
// several entries; others repeat the param) and each scope string is
// "type:name:action[,action...]" per the registry token spec. A malformed
// entry is dropped rather than rejected: a request naming no valid scope at
// all is simply granted nothing once ClampRegistryScopesToTenant runs, which
// is the safe outcome for a value the client did not have to get right.
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

// ClampRegistryScopesToTenant is the security boundary of the hosted
// registry: it grants only the requested scopes that name a repository under
// the resolved tenant's own namespace ("<tenant>" or "<tenant>/..."),
// resolved from the caller's verified bearer token — never from anything
// client-supplied. Every other scope — a different resource type, a
// different tenant's namespace, a name that merely starts with the tenant's
// (e.g. tenant "frs" must not match "frsking") — is dropped entirely rather
// than narrowed. A dropped scope grants nothing; it is not an error, matching
// the registry token spec's own contract that a token may grant less than it
// was asked for.
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

// registryTokenClaims is the claim set of a registry v2 access token, per the
// spec linked above.
type registryTokenClaims struct {
	Issuer     string                `json:"iss"`
	Subject    string                `json:"sub"`
	Audience   string                `json:"aud"`
	Expiration int64                 `json:"exp"`
	NotBefore  int64                 `json:"nbf"`
	IssuedAt   int64                 `json:"iat"`
	Access     []RegistryAccessScope `json:"access"`
}

// SignRegistry mints a hosted-registry access token for already-clamped access
// grants — clamping to the resolved tenant is the route's job
// (ClampRegistryScopesToTenant), never the signer's, so a signer bug can only
// under-grant, not over-grant. An empty access is a deliberate, valid outcome
// (the registry token spec allows granting less than was requested, down to
// nothing at all) and is exactly what a scope clamped away entirely produces —
// refusing to mint here would turn that same-shaped "authenticated but
// authorized for nothing" response into a distinguishable error. service is
// the registry's own `service` value from its WWW-Authenticate challenge and
// becomes the token's audience, which is what keeps this token from being
// replayable against the platform API (audience "erun-api") or an env's MCP
// edge (audience "erun-mcp:<tenant>/<env>") even though all three ride the
// same backend signing key.
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
