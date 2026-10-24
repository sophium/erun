package cloud

import (
	"fmt"
	"strings"
	"testing"
	"time"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
)

func startDeviceAuthorizationExpecting(t *testing.T, wantClientID string, auth ERunDeviceAuthorization) func(core.Context, OIDCDiscovery, string, string) (ERunDeviceAuthorization, error) {
	return func(_ core.Context, _ OIDCDiscovery, clientID, scope string) (ERunDeviceAuthorization, error) {
		if clientID != wantClientID {
			t.Fatalf("clientID = %q, want %q", clientID, wantClientID)
		}
		if scope == "" {
			t.Fatal("device authorization must request a scope")
		}
		return auth, nil
	}
}

func pollDeviceTokenExpecting(t *testing.T, wantDeviceCode string, tokens ERunTokens) func(core.Context, OIDCDiscovery, string, ERunDeviceAuthorization) (ERunTokens, error) {
	return func(_ core.Context, _ OIDCDiscovery, _ string, auth ERunDeviceAuthorization) (ERunTokens, error) {
		if auth.DeviceCode != wantDeviceCode {
			t.Fatalf("DeviceCode = %q, want %q", auth.DeviceCode, wantDeviceCode)
		}
		return tokens, nil
	}
}

func TestERunCloudProviderLoginDeviceFlowPersistsTokensAndCache(t *testing.T) {
	store := erunTestCloudStore{config: configpkg.ERunConfig{CloudProviders: []configpkg.CloudProviderConfig{erunTestProvider()}}}
	secrets := NewFileCloudSecretStore(t.TempDir())
	provider := erunTestProvider()

	deps := CloudDependencies{
		CloudSecretStore: secrets,
		FetchOIDCDiscovery: func(core.Context, string) (OIDCDiscovery, error) {
			return OIDCDiscovery{Issuer: provider.OIDCIssuerURL, DeviceAuthorizationEndpoint: "https://auth.example.test/device", TokenEndpoint: "https://auth.example.test/token"}, nil
		},
		StartERunDeviceAuthorization: startDeviceAuthorizationExpecting(t, "cli-client-1", ERunDeviceAuthorization{
			DeviceCode: "device-code-1", UserCode: "ABCD-EFGH", VerificationURIComplete: "https://auth.example.test/device?code=ABCD-EFGH",
		}),
		PollERunDeviceToken: pollDeviceTokenExpecting(t, "device-code-1", ERunTokens{AccessToken: "access-1", RefreshToken: "refresh-1", ExpiresIn: time.Hour}),
	}

	status, err := LoginCloudProviderAlias(core.Context{}, &store, CloudLoginParams{Alias: provider.Alias}, deps)
	if err != nil {
		t.Fatalf("LoginCloudProviderAlias: %v", err)
	}
	if status.Status != configpkg.CloudTokenStatusActive {
		t.Fatalf("Status = %q, want active", status.Status)
	}

	saved, err := ResolveCloudProvider(&store, provider.Alias)
	if err != nil {
		t.Fatalf("ResolveCloudProvider: %v", err)
	}
	if saved.ERun.RefreshTokenRef == "" {
		t.Fatal("expected a persisted refresh token ref")
	}
	refresh, refreshErr := secrets.LoadCloudSecret(saved.ERun.RefreshTokenRef)
	cached, cachedOK := loadCachedERunAccessToken(secrets, provider.Alias)
	if refreshErr != nil || refresh != "refresh-1" || !cachedOK || cached != "access-1" {
		t.Fatalf("refresh=%q(err=%v) cached=%q(ok=%v), want refresh-1 / access-1", refresh, refreshErr, cached, cachedOK)
	}
}

// TestERunCloudProviderLoginDegradesWhenIssuerRejectsOrgClaimScope: the
// org-claim scope is requested by default, but an issuer that has never heard
// of it (a dedicated/BYO IdP, the common case, not the shipped Zitadel) must
// still let the login through rather than breaking on an invalid_scope refusal.
func TestERunCloudProviderLoginDegradesWhenIssuerRejectsOrgClaimScope(t *testing.T) {
	store := erunTestCloudStore{config: configpkg.ERunConfig{CloudProviders: []configpkg.CloudProviderConfig{erunTestProvider()}}}
	secrets := NewFileCloudSecretStore(t.TempDir())
	provider := erunTestProvider()

	var gotScopes []string
	deps := CloudDependencies{
		CloudSecretStore: secrets,
		FetchOIDCDiscovery: func(core.Context, string) (OIDCDiscovery, error) {
			return OIDCDiscovery{Issuer: provider.OIDCIssuerURL, DeviceAuthorizationEndpoint: "https://auth.example.test/device", TokenEndpoint: "https://auth.example.test/token"}, nil
		},
		StartERunDeviceAuthorization: func(_ core.Context, _ OIDCDiscovery, _ string, scope string) (ERunDeviceAuthorization, error) {
			gotScopes = append(gotScopes, scope)
			if strings.Contains(scope, erunOrgClaimScope) {
				return ERunDeviceAuthorization{}, errERunInvalidScope
			}
			return ERunDeviceAuthorization{DeviceCode: "device-code-degrade", UserCode: "DGRD-1234"}, nil
		},
		PollERunDeviceToken: func(core.Context, OIDCDiscovery, string, ERunDeviceAuthorization) (ERunTokens, error) {
			return ERunTokens{AccessToken: "access-degrade", RefreshToken: "refresh-degrade", ExpiresIn: time.Hour}, nil
		},
	}

	status, err := LoginCloudProviderAlias(core.Context{}, &store, CloudLoginParams{Alias: provider.Alias}, deps)
	if err != nil {
		t.Fatalf("LoginCloudProviderAlias: %v", err)
	}
	if status.Status != configpkg.CloudTokenStatusActive {
		t.Fatalf("Status = %q, want active", status.Status)
	}
	assertDegradedScopeAttempts(t, gotScopes)
	if cached, ok := loadCachedERunAccessToken(secrets, provider.Alias); !ok || cached != "access-degrade" {
		t.Fatalf("cached = %q (ok=%v), want the fallback attempt's token persisted", cached, ok)
	}
}

// assertDegradedScopeAttempts checks the two device-authorization attempts a
// graceful scope degradation must produce: the first requests the org-claim
// scope by default, the second (after the issuer's refusal) drops it while
// keeping the baseline scopes.
func assertDegradedScopeAttempts(t *testing.T, gotScopes []string) {
	t.Helper()
	if len(gotScopes) != 2 {
		t.Fatalf("expected exactly 2 device-authorization attempts (with, then without, the org-claim scope), got %v", gotScopes)
	}
	if !strings.Contains(gotScopes[0], erunOrgClaimScope) {
		t.Fatalf("first attempt scope = %q, want the org-claim scope requested by default", gotScopes[0])
	}
	if strings.Contains(gotScopes[1], erunOrgClaimScope) {
		t.Fatalf("fallback attempt scope = %q, must not repeat the scope the issuer just rejected", gotScopes[1])
	}
	for _, baseline := range []string{"openid", "offline_access"} {
		if !strings.Contains(gotScopes[1], baseline) {
			t.Fatalf("fallback scope = %q, want the %q baseline kept", gotScopes[1], baseline)
		}
	}
}

func TestERunCloudProviderLoginFallsBackToAuthCodeWithoutDeviceEndpoint(t *testing.T) {
	store := erunTestCloudStore{config: configpkg.ERunConfig{CloudProviders: []configpkg.CloudProviderConfig{erunTestProvider()}}}
	secrets := NewFileCloudSecretStore(t.TempDir())
	provider := erunTestProvider()

	called := false
	deps := CloudDependencies{
		CloudSecretStore: secrets,
		FetchOIDCDiscovery: func(core.Context, string) (OIDCDiscovery, error) {
			// No DeviceAuthorizationEndpoint: the issuer does not support the
			// device grant, so login must fall back to the PKCE path.
			return OIDCDiscovery{Issuer: provider.OIDCIssuerURL, AuthorizationEndpoint: "https://auth.example.test/authorize", TokenEndpoint: "https://auth.example.test/token"}, nil
		},
		StartERunDeviceAuthorization: func(core.Context, OIDCDiscovery, string, string) (ERunDeviceAuthorization, error) {
			t.Fatal("device authorization must not be started when the issuer advertises no device endpoint")
			return ERunDeviceAuthorization{}, nil
		},
		RunERunAuthCodeLogin: func(_ core.Context, discovery OIDCDiscovery, clientID, scope string) (ERunTokens, error) {
			called = true
			if clientID != "cli-client-1" {
				t.Fatalf("clientID = %q", clientID)
			}
			return ERunTokens{AccessToken: "access-2", RefreshToken: "refresh-2", ExpiresIn: time.Hour}, nil
		},
	}

	if _, err := LoginCloudProviderAlias(core.Context{}, &store, CloudLoginParams{Alias: provider.Alias}, deps); err != nil {
		t.Fatalf("LoginCloudProviderAlias: %v", err)
	}
	if !called {
		t.Fatal("expected the Authorization Code + PKCE fallback to run")
	}
}

func TestERunCloudProviderLoginRequiresInit(t *testing.T) {
	store := erunTestCloudStore{}
	provider := NormalizeCloudProviderConfig(configpkg.CloudProviderConfig{Alias: "erun+x@erun", Provider: configpkg.CloudProviderERun})
	if _, err := erunCloudProviderLogin(core.Context{}, &store, provider, "", nil, CloudDependencies{}); err == nil {
		t.Fatal("expected an error for a provider with no ERun config")
	} else if !strings.Contains(err.Error(), "cloud init erun") {
		t.Fatalf("error %q does not point at `erun cloud init erun`", err.Error())
	}
}

// A device grant that cannot complete must not be a dead end: the issuer
// advertises the endpoint, so auto starts there, but the authorization-code
// path still has to run when it fails, so one broken authentication method
// cannot lock the CLI out.
func TestERunCloudProviderLoginFallsBackToAuthCodeWhenDeviceFlowFails(t *testing.T) {
	store := erunTestCloudStore{config: configpkg.ERunConfig{CloudProviders: []configpkg.CloudProviderConfig{erunTestProvider()}}}
	secrets := NewFileCloudSecretStore(t.TempDir())
	provider := erunTestProvider()

	deviceStarted, authCodeCalled := false, false
	deps := CloudDependencies{
		CloudSecretStore: secrets,
		FetchOIDCDiscovery: func(core.Context, string) (OIDCDiscovery, error) {
			return OIDCDiscovery{Issuer: provider.OIDCIssuerURL, DeviceAuthorizationEndpoint: "https://auth.example.test/device", AuthorizationEndpoint: "https://auth.example.test/authorize", TokenEndpoint: "https://auth.example.test/token"}, nil
		},
		StartERunDeviceAuthorization: func(core.Context, OIDCDiscovery, string, string) (ERunDeviceAuthorization, error) {
			deviceStarted = true
			return ERunDeviceAuthorization{DeviceCode: "device-code-3", UserCode: "WXYZ-1234"}, nil
		},
		PollERunDeviceToken: func(core.Context, OIDCDiscovery, string, ERunDeviceAuthorization) (ERunTokens, error) {
			return ERunTokens{}, fmt.Errorf("device authorization expired before sign-in completed")
		},
		RunERunAuthCodeLogin: func(core.Context, OIDCDiscovery, string, string) (ERunTokens, error) {
			authCodeCalled = true
			return ERunTokens{AccessToken: "access-3", RefreshToken: "refresh-3", ExpiresIn: time.Hour}, nil
		},
	}

	status, err := LoginCloudProviderAlias(core.Context{}, &store, CloudLoginParams{Alias: provider.Alias}, deps)
	if err != nil {
		t.Fatalf("LoginCloudProviderAlias: %v", err)
	}
	if !deviceStarted {
		t.Fatal("auto must try the device grant first when the issuer advertises one")
	}
	if !authCodeCalled {
		t.Fatal("auto must fall back to authorization code + PKCE when the device grant fails")
	}
	if status.Status != configpkg.CloudTokenStatusActive {
		t.Fatalf("Status = %q, want active", status.Status)
	}
	if cached, ok := loadCachedERunAccessToken(secrets, provider.Alias); !ok || cached != "access-3" {
		t.Fatalf("cached = %q (ok=%v), want access-3 from the fallback flow", cached, ok)
	}
}

// An explicit --flow authcode skips the device grant even where one is
// advertised: that is the whole point of the override, for an operator whose
// device-page method is broken but whose browser session already works.
func TestERunCloudProviderLoginHonoursExplicitAuthCodeFlow(t *testing.T) {
	store := erunTestCloudStore{config: configpkg.ERunConfig{CloudProviders: []configpkg.CloudProviderConfig{erunTestProvider()}}}
	provider := erunTestProvider()

	deps := CloudDependencies{
		CloudSecretStore: NewFileCloudSecretStore(t.TempDir()),
		FetchOIDCDiscovery: func(core.Context, string) (OIDCDiscovery, error) {
			return OIDCDiscovery{Issuer: provider.OIDCIssuerURL, DeviceAuthorizationEndpoint: "https://auth.example.test/device", AuthorizationEndpoint: "https://auth.example.test/authorize", TokenEndpoint: "https://auth.example.test/token"}, nil
		},
		StartERunDeviceAuthorization: func(core.Context, OIDCDiscovery, string, string) (ERunDeviceAuthorization, error) {
			t.Fatal("an explicit authcode flow must not start the device grant")
			return ERunDeviceAuthorization{}, nil
		},
		RunERunAuthCodeLogin: func(core.Context, OIDCDiscovery, string, string) (ERunTokens, error) {
			return ERunTokens{AccessToken: "access-4", RefreshToken: "refresh-4", ExpiresIn: time.Hour}, nil
		},
	}

	if _, err := LoginCloudProviderAlias(core.Context{}, &store, CloudLoginParams{Alias: provider.Alias, Flow: ERunLoginFlowAuthCode}, deps); err != nil {
		t.Fatalf("LoginCloudProviderAlias: %v", err)
	}
}

// An explicit --flow device against an issuer without the endpoint fails
// loudly, naming the flow that would work, rather than silently doing
// something the operator did not ask for.
func TestERunCloudProviderLoginExplicitDeviceFlowWithoutEndpointErrors(t *testing.T) {
	store := erunTestCloudStore{config: configpkg.ERunConfig{CloudProviders: []configpkg.CloudProviderConfig{erunTestProvider()}}}
	provider := erunTestProvider()

	deps := CloudDependencies{
		CloudSecretStore: NewFileCloudSecretStore(t.TempDir()),
		FetchOIDCDiscovery: func(core.Context, string) (OIDCDiscovery, error) {
			return OIDCDiscovery{Issuer: provider.OIDCIssuerURL, AuthorizationEndpoint: "https://auth.example.test/authorize", TokenEndpoint: "https://auth.example.test/token"}, nil
		},
		RunERunAuthCodeLogin: func(core.Context, OIDCDiscovery, string, string) (ERunTokens, error) {
			t.Fatal("an explicit device flow must not silently use authorization code")
			return ERunTokens{}, nil
		},
	}

	_, err := LoginCloudProviderAlias(core.Context{}, &store, CloudLoginParams{Alias: provider.Alias, Flow: ERunLoginFlowDevice}, deps)
	if err == nil {
		t.Fatal("expected an error for an explicit device flow with no device endpoint")
	}
	if !strings.Contains(err.Error(), ERunLoginFlowAuthCode) {
		t.Fatalf("error %q does not name the flow that would work", err.Error())
	}
}

func TestNormalizeERunLoginFlow(t *testing.T) {
	for _, tc := range []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "", want: ERunLoginFlowAuto},
		{in: "auto", want: ERunLoginFlowAuto},
		{in: " Device ", want: ERunLoginFlowDevice},
		{in: "AUTHCODE", want: ERunLoginFlowAuthCode},
		{in: "pkce", wantErr: true},
	} {
		got, err := NormalizeERunLoginFlow(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("NormalizeERunLoginFlow(%q) = %q, want an error naming the accepted values", tc.in, got)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Fatalf("NormalizeERunLoginFlow(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
}

// Reserved scopes are frequently absent from a provider's discovery document
// (Zitadel's urn:zitadel:* family is), so the operator must be able to ask for
// one by name and have it actually reach the authorization request. Without
// the org claim such a scope carries, an org-scoped issuer resolves nobody
// .
func TestERunCloudProviderLoginRequestsExtraScopes(t *testing.T) {
	store := erunTestCloudStore{config: configpkg.ERunConfig{CloudProviders: []configpkg.CloudProviderConfig{erunTestProvider()}}}
	provider := erunTestProvider()

	var gotScope string
	deps := CloudDependencies{
		CloudSecretStore: NewFileCloudSecretStore(t.TempDir()),
		FetchOIDCDiscovery: func(core.Context, string) (OIDCDiscovery, error) {
			return OIDCDiscovery{Issuer: provider.OIDCIssuerURL, AuthorizationEndpoint: "https://auth.example.test/authorize", TokenEndpoint: "https://auth.example.test/token"}, nil
		},
		RunERunAuthCodeLogin: func(_ core.Context, _ OIDCDiscovery, _ string, scope string) (ERunTokens, error) {
			gotScope = scope
			return ERunTokens{AccessToken: "access-5", RefreshToken: "refresh-5", ExpiresIn: time.Hour}, nil
		},
	}

	params := CloudLoginParams{Alias: provider.Alias, Scopes: []string{"urn:zitadel:iam:user:resourceowner"}}
	if _, err := LoginCloudProviderAlias(core.Context{}, &store, params, deps); err != nil {
		t.Fatalf("LoginCloudProviderAlias: %v", err)
	}
	if !strings.Contains(gotScope, "urn:zitadel:iam:user:resourceowner") {
		t.Fatalf("scope = %q, want the requested reserved scope to reach the request", gotScope)
	}
	for _, baseline := range []string{"openid", "offline_access"} {
		if !strings.Contains(gotScope, baseline) {
			t.Fatalf("scope = %q, want the %q baseline kept", gotScope, baseline)
		}
	}
}

func TestERunLoginScope(t *testing.T) {
	if got := erunLoginScope(nil); got != erunOAuthScope {
		t.Fatalf("erunLoginScope(nil) = %q, want the baseline %q", got, erunOAuthScope)
	}
	// A duplicate of the baseline must not be repeated, and a multi-scope
	// string must be split rather than sent as one opaque value.
	got := erunLoginScope([]string{"openid", "a b", "", "a"})
	if got != "openid offline_access a b" {
		t.Fatalf("erunLoginScope = %q, want deduped and split", got)
	}
}
