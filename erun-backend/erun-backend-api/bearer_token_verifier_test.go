package backendapi

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/sophium/erun/erun-common/infra/mcpauth"
)

type mockOIDCProvider struct {
	server *httptest.Server
	key    *rsa.PrivateKey
	keyID  string
	// tokenEndpoint is advertised in discovery; empty omits it.
	tokenEndpoint string
}

func newMockOIDCProvider(t *testing.T) *mockOIDCProvider {
	t.Helper()
	return newMockOIDCProviderWithRoutes(t, nil)
}

// newMockOIDCProviderWithRoutes registers extra routes before the provider serves.
func newMockOIDCProviderWithRoutes(t *testing.T, extraRoutes func(*mockOIDCProvider, *http.ServeMux)) *mockOIDCProvider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	p := &mockOIDCProvider{key: key, keyID: "test-key-1"}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		document := map[string]any{
			"issuer":                                p.issuer(),
			"jwks_uri":                              p.issuer() + "/keys",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		}
		if p.tokenEndpoint != "" {
			document["token_endpoint"] = p.issuer() + p.tokenEndpoint
		}
		writeOIDCJSON(w, document)
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, _ *http.Request) {
		writeOIDCJSON(w, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
			Key:       p.key.Public(),
			KeyID:     p.keyID,
			Algorithm: string(jose.RS256),
			Use:       "sig",
		}}})
	})
	if extraRoutes != nil {
		extraRoutes(p, mux)
	}
	p.server = httptest.NewServer(mux)
	t.Cleanup(p.server.Close)
	return p
}

func (p *mockOIDCProvider) issuer() string { return p.server.URL }

func (p *mockOIDCProvider) sign(t *testing.T, claims map[string]any) string {
	t.Helper()
	token, err := p.signClaims(claims)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return token
}

// signClaims returns the error for use off the test goroutine.
func (p *mockOIDCProvider) signClaims(claims map[string]any) (string, error) {
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: p.key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", p.keyID),
	)
	if err != nil {
		return "", fmt.Errorf("new signer: %w", err)
	}
	token, err := jwt.Signed(signer).Claims(claims).Serialize()
	if err != nil {
		return "", fmt.Errorf("serialize token: %w", err)
	}
	return token, nil
}

// signWithUntrustedKey keeps the provider's kid so JWKS lookup succeeds but the
// signature does not verify.
func (p *mockOIDCProvider) signWithUntrustedKey(t *testing.T, claims map[string]any) string {
	t.Helper()
	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate other key: %v", err)
	}
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: otherKey},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", p.keyID),
	)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	token, err := jwt.Signed(signer).Claims(claims).Serialize()
	if err != nil {
		t.Fatalf("serialize token: %v", err)
	}
	return token
}

func writeOIDCJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func mustVerify(t *testing.T, verifier *BearerTokenVerifier, token string) Claims {
	t.Helper()
	claims, err := verifier.VerifyBearerToken(context.Background(), token)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	return claims
}

func mustReject(t *testing.T, verifier *BearerTokenVerifier, token string, why string) {
	t.Helper()
	if _, err := verifier.VerifyBearerToken(context.Background(), token); err == nil {
		t.Fatal(why)
	}
}

func TestVerifyBearerTokenDelegatesToSharedVerifier(t *testing.T) {
	provider := newMockOIDCProvider(t)
	now := time.Now()
	verifier := NewBearerTokenVerifier(BearerTokenVerifierOptions{AllowedIssuers: []string{provider.issuer()}})

	t.Run("valid token maps username and keeps raw claims", func(t *testing.T) {
		token := provider.sign(t, map[string]any{
			"iss":                provider.issuer(),
			"sub":                "user-1",
			"preferred_username": "user@example",
			"org_id":             "org-7",
			"iat":                now.Unix(),
			"exp":                now.Add(time.Hour).Unix(),
		})
		claims := mustVerify(t, verifier, token)
		if claims.Issuer != provider.issuer() || claims.Subject != "user-1" || claims.Username != "user@example" {
			t.Fatalf("claims = %+v", claims)
		}
		if claims.Raw["org_id"] != "org-7" {
			t.Fatalf("raw org_id = %v", claims.Raw["org_id"])
		}
	})

	t.Run("AWS STS identity-store user id overrides the subject", func(t *testing.T) {
		token := provider.sign(t, map[string]any{
			"iss": provider.issuer(),
			"sub": "arn:aws:iam::020362606330:role/Whatever",
			"https://sts.amazonaws.com/": map[string]any{
				"identity_store_user_id": "265222f4-f041-7008-6e0c-2d3993b555bf",
			},
			"iat": now.Unix(),
			"exp": now.Add(time.Hour).Unix(),
		})
		claims := mustVerify(t, verifier, token)
		if claims.Subject != "265222f4-f041-7008-6e0c-2d3993b555bf" {
			t.Fatalf("subject = %q, want AWS identity-store user id", claims.Subject)
		}
	})

	t.Run("issuer outside the allow-list is rejected", func(t *testing.T) {
		other := newMockOIDCProvider(t)
		token := other.sign(t, map[string]any{
			"iss": other.issuer(),
			"sub": "user-1",
			"iat": now.Unix(),
			"exp": now.Add(time.Hour).Unix(),
		})
		mustReject(t, verifier, token, "expected a disallowed issuer to be rejected")
	})

	t.Run("bad signature is rejected", func(t *testing.T) {
		token := provider.signWithUntrustedKey(t, map[string]any{
			"iss": provider.issuer(),
			"sub": "user-1",
			"iat": now.Unix(),
			"exp": now.Add(time.Hour).Unix(),
		})
		mustReject(t, verifier, token, "expected a token signed by a key not in the JWKS to be rejected")
	})
}

func mustRejectWith(t *testing.T, verifier *BearerTokenVerifier, token string, wants ...string) {
	t.Helper()
	_, err := verifier.VerifyBearerToken(context.Background(), token)
	if err == nil {
		t.Fatal("expected the token to be rejected")
	}
	for _, want := range wants {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("rejection %q does not name %q", err.Error(), want)
		}
	}
}

func TestVerifyBearerTokenOIDCAudience(t *testing.T) {
	provider := newMockOIDCProvider(t)
	now := time.Now()
	token := func(audience any) string {
		claims := map[string]any{
			"iss": provider.issuer(),
			"sub": "user-1",
			"iat": now.Unix(),
			"exp": now.Add(time.Hour).Unix(),
		}
		if audience != nil {
			claims["aud"] = audience
		}
		return provider.sign(t, claims)
	}

	t.Run("a configured audience accepts a token minted for it", func(t *testing.T) {
		verifier := NewBearerTokenVerifier(BearerTokenVerifierOptions{
			AllowedIssuers:   []string{provider.issuer()},
			AllowedAudiences: []string{"console-client", "cli-client"},
		})
		if claims := mustVerify(t, verifier, token("cli-client")); claims.Subject != "user-1" {
			t.Fatalf("claims = %+v", claims)
		}
	})

	t.Run("an OIDC aud listing several audiences passes on any one of them", func(t *testing.T) {
		verifier := NewBearerTokenVerifier(BearerTokenVerifierOptions{
			AllowedIssuers:   []string{provider.issuer()},
			AllowedAudiences: []string{"console-client"},
		})
		mustVerify(t, verifier, token([]string{"some-other-client", "console-client"}))
	})

	t.Run("the same trusted issuer's token for another audience is rejected, naming both", func(t *testing.T) {
		verifier := NewBearerTokenVerifier(BearerTokenVerifierOptions{
			AllowedIssuers:   []string{provider.issuer()},
			AllowedAudiences: []string{"console-client", "cli-client"},
		})
		mustRejectWith(t, verifier, token("some-other-client"), "some-other-client", "console-client", "cli-client")
	})

	t.Run("a token with no aud claim is rejected once an allow-list is configured", func(t *testing.T) {
		verifier := NewBearerTokenVerifier(BearerTokenVerifierOptions{
			AllowedIssuers:   []string{provider.issuer()},
			AllowedAudiences: []string{"console-client"},
		})
		mustRejectWith(t, verifier, token(nil), "carries no audience", "console-client")
	})

	t.Run("an unconfigured allow-list accepts any audience", func(t *testing.T) {
		verifier := NewBearerTokenVerifier(BearerTokenVerifierOptions{AllowedIssuers: []string{provider.issuer()}})
		mustVerify(t, verifier, token("some-other-client"))
		mustVerify(t, verifier, token(nil))
	})
}

func TestVerifyBearerTokenFileIssuer(t *testing.T) {
	privatePEM, keyPath := newDesktopIdentity(t)
	issuer := mcpauth.FileIssuer(keyPath)
	verifier := NewBearerTokenVerifier(BearerTokenVerifierOptions{DesktopPublicKeyPath: keyPath})
	now := time.Now()

	t.Run("valid desktop token authenticates", func(t *testing.T) {
		token := signDesktopToken(t, privatePEM, mcpauth.MCPTokenClaims{
			Issuer:    issuer,
			Subject:   "dev-user",
			Audience:  mcpauth.APITokenAudience,
			ExpiresAt: now.Add(time.Hour).Unix(),
		})
		claims := mustVerify(t, verifier, token)
		if claims.Issuer != issuer || claims.Subject != "dev-user" {
			t.Fatalf("claims = %+v", claims)
		}
	})

	t.Run("a token minted for an MCP env audience is rejected against the API", func(t *testing.T) {
		token := signDesktopToken(t, privatePEM, mcpauth.MCPTokenClaims{
			Issuer:    issuer,
			Subject:   "dev-user",
			Audience:  mcpauth.MCPTokenAudience("acme", "prod"),
			ExpiresAt: now.Add(time.Hour).Unix(),
		})
		mustReject(t, verifier, token, "expected an MCP-audience token to be rejected against the API")
	})

	t.Run("a token claiming the trusted issuer but signed by a different key is rejected", func(t *testing.T) {
		attackerPrivate, _ := newDesktopIdentity(t)
		token := signDesktopToken(t, attackerPrivate, mcpauth.MCPTokenClaims{
			Issuer:    issuer,
			Subject:   "intruder",
			Audience:  mcpauth.APITokenAudience,
			ExpiresAt: now.Add(time.Hour).Unix(),
		})
		mustReject(t, verifier, token, "expected a token signed by a key other than the trusted public key to be rejected")
	})

	t.Run("expired token is rejected", func(t *testing.T) {
		token := signDesktopToken(t, privatePEM, mcpauth.MCPTokenClaims{
			Issuer:    issuer,
			Subject:   "dev-user",
			Audience:  mcpauth.APITokenAudience,
			ExpiresAt: now.Add(-time.Minute).Unix(),
		})
		mustReject(t, verifier, token, "expected an expired token to be rejected")
	})

	t.Run("an OIDC audience allow-list leaves the desktop path's own audience check alone", func(t *testing.T) {
		verifier := NewBearerTokenVerifier(BearerTokenVerifierOptions{
			DesktopPublicKeyPath: keyPath,
			AllowedAudiences:     []string{"console-client"},
		})
		accepted := signDesktopToken(t, privatePEM, mcpauth.MCPTokenClaims{
			Issuer:    issuer,
			Subject:   "dev-user",
			Audience:  mcpauth.APITokenAudience,
			ExpiresAt: now.Add(time.Hour).Unix(),
		})
		mustVerify(t, verifier, accepted)

		for _, audience := range []string{mcpauth.MCPTokenAudience("acme", "prod"), "console-client"} {
			rejected := signDesktopToken(t, privatePEM, mcpauth.MCPTokenClaims{
				Issuer:    issuer,
				Subject:   "dev-user",
				Audience:  audience,
				ExpiresAt: now.Add(time.Hour).Unix(),
			})
			mustReject(t, verifier, rejected, "expected the desktop path to keep enforcing the API audience: "+audience)
		}
	})

	t.Run("a desktop token from an untrusted file issuer is rejected", func(t *testing.T) {
		otherPrivate, otherPath := newDesktopIdentity(t)
		token := signDesktopToken(t, otherPrivate, mcpauth.MCPTokenClaims{
			Issuer:    mcpauth.FileIssuer(otherPath),
			Subject:   "intruder",
			Audience:  mcpauth.APITokenAudience,
			ExpiresAt: now.Add(time.Hour).Unix(),
		})
		mustReject(t, verifier, token, "expected a token from a non-configured file issuer to be rejected")
	})
}

func newDesktopIdentity(t *testing.T) (privatePEM []byte, keyPath string) {
	t.Helper()
	privatePEM, publicPEM, err := mcpauth.GenerateDesktopIdentity()
	if err != nil {
		t.Fatalf("generate desktop identity: %v", err)
	}
	keyPath = filepath.Join(t.TempDir(), "desktopid.pub")
	if err := os.WriteFile(keyPath, publicPEM, 0o600); err != nil {
		t.Fatalf("write public key: %v", err)
	}
	return privatePEM, keyPath
}

func signDesktopToken(t *testing.T, privateKey []byte, claims mcpauth.MCPTokenClaims) string {
	t.Helper()
	token, err := mcpauth.SignMCPToken(privateKey, claims)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return token
}

func TestClaimsFromOIDCTokenClaimsUsesAWSIdentityStoreUserID(t *testing.T) {
	claims := claimsFromOIDCTokenClaims(oidcTokenClaims{
		Issuer:  "https://a11bec5a-678d-4a6a-aa25-f3770df2ac5e.tokens.sts.global.api.aws",
		Subject: "arn:aws:iam::020362606330:role/aws-reserved/sso.amazonaws.com/eu-west-2/AWSReservedSSO_AdministratorAccess_c95738f708c1c268",
		AWS: awsSTSClaims{
			IdentityStoreUserID: "265222f4-f041-7008-6e0c-2d3993b555bf",
		},
	}, nil)

	if claims.Subject != "265222f4-f041-7008-6e0c-2d3993b555bf" {
		t.Fatalf("expected AWS identity store user id as subject, got %q", claims.Subject)
	}
}

func TestClaimsFromOIDCTokenClaimsFallsBackToSubjectWithoutAWSIdentityStoreUserID(t *testing.T) {
	claims := claimsFromOIDCTokenClaims(oidcTokenClaims{
		Issuer:  "https://a11bec5a-678d-4a6a-aa25-f3770df2ac5e.tokens.sts.global.api.aws",
		Subject: "arn:aws:iam::020362606330:role/aws-reserved/sso.amazonaws.com/eu-west-2/AWSReservedSSO_AdministratorAccess_c95738f708c1c268",
	}, nil)

	if claims.Subject != "arn:aws:iam::020362606330:role/aws-reserved/sso.amazonaws.com/eu-west-2/AWSReservedSSO_AdministratorAccess_c95738f708c1c268" {
		t.Fatalf("expected subject fallback, got %q", claims.Subject)
	}
}

func TestClaimsFromOIDCTokenClaimsKeepsNonAWSSubject(t *testing.T) {
	claims := claimsFromOIDCTokenClaims(oidcTokenClaims{
		Issuer:            "https://issuer.example",
		Subject:           "user-1",
		PreferredUsername: "user@example",
	}, nil)

	if claims.Subject != "user-1" {
		t.Fatalf("expected standard OIDC subject, got %q", claims.Subject)
	}
	if claims.Username != "user@example" {
		t.Fatalf("expected preferred username, got %q", claims.Username)
	}
}
