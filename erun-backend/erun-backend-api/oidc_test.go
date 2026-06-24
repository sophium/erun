package backendapi

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// mockOIDCProvider is a self-contained OIDC issuer for the backend verifier
// tests: an httptest server publishing discovery + JWKS, plus the RSA key used
// to mint RS256 tokens. It proves VerifyBearerToken now delegates signature/JWKS
// verification to the shared eruncommon verifier (issue #656) while keeping the
// backend's allow-list and username/AWS-STS mapping.
type mockOIDCProvider struct {
	server *httptest.Server
	key    *rsa.PrivateKey
	keyID  string
}

func newMockOIDCProvider(t *testing.T) *mockOIDCProvider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	p := &mockOIDCProvider{key: key, keyID: "test-key-1"}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		writeOIDCJSON(w, map[string]any{
			"issuer":                                p.issuer(),
			"jwks_uri":                              p.issuer() + "/keys",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, _ *http.Request) {
		writeOIDCJSON(w, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
			Key:       p.key.Public(),
			KeyID:     p.keyID,
			Algorithm: string(jose.RS256),
			Use:       "sig",
		}}})
	})
	p.server = httptest.NewServer(mux)
	t.Cleanup(p.server.Close)
	return p
}

func (p *mockOIDCProvider) issuer() string { return p.server.URL }

func (p *mockOIDCProvider) sign(t *testing.T, claims map[string]any) string {
	t.Helper()
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: p.key},
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
	_ = json.NewEncoder(w).Encode(value)
}

func TestVerifyBearerTokenDelegatesToSharedVerifier(t *testing.T) {
	provider := newMockOIDCProvider(t)
	now := time.Now()
	verifier := NewOIDCTokenVerifier([]string{provider.issuer()})

	t.Run("valid token maps username and keeps raw claims", func(t *testing.T) {
		token := provider.sign(t, map[string]any{
			"iss":                provider.issuer(),
			"sub":                "user-1",
			"preferred_username": "user@example",
			"org_id":             "org-7",
			"iat":                now.Unix(),
			"exp":                now.Add(time.Hour).Unix(),
		})
		claims, err := verifier.VerifyBearerToken(context.Background(), token)
		if err != nil {
			t.Fatalf("verify: %v", err)
		}
		if claims.Issuer != provider.issuer() || claims.Subject != "user-1" || claims.Username != "user@example" {
			t.Fatalf("claims = %+v", claims)
		}
		// The raw map is preserved so the identity resolver can read a per-issuer
		// org claim whose name is not known to the verifier.
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
		claims, err := verifier.VerifyBearerToken(context.Background(), token)
		if err != nil {
			t.Fatalf("verify: %v", err)
		}
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
		if _, err := verifier.VerifyBearerToken(context.Background(), token); err == nil {
			t.Fatal("expected a disallowed issuer to be rejected")
		}
	})

	t.Run("bad signature is rejected", func(t *testing.T) {
		otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatalf("generate other key: %v", err)
		}
		signer, err := jose.NewSigner(
			jose.SigningKey{Algorithm: jose.RS256, Key: otherKey},
			(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", provider.keyID),
		)
		if err != nil {
			t.Fatalf("new signer: %v", err)
		}
		token, err := jwt.Signed(signer).Claims(map[string]any{
			"iss": provider.issuer(),
			"sub": "user-1",
			"iat": now.Unix(),
			"exp": now.Add(time.Hour).Unix(),
		}).Serialize()
		if err != nil {
			t.Fatalf("serialize token: %v", err)
		}
		if _, err := verifier.VerifyBearerToken(context.Background(), token); err == nil {
			t.Fatal("expected a token signed by a key not in the JWKS to be rejected")
		}
	})
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
