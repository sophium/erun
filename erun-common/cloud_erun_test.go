package eruncommon

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// erunTestCloudStore is a mutable CloudStore stub: unlike
// stubCloudContextStore (whose SaveERunConfig is a no-op), tests here need to
// observe what a real save persisted.
type erunTestCloudStore struct {
	config ERunConfig
}

func (s *erunTestCloudStore) LoadERunConfig() (ERunConfig, string, error) { return s.config, "", nil }

func (s *erunTestCloudStore) SaveERunConfig(config ERunConfig) error {
	s.config = config
	return nil
}

func fetchPlatformInfoExpecting(t *testing.T, wantAPIURL string, info PlatformInfo) func(Context, string) (PlatformInfo, error) {
	return func(_ Context, apiURL string) (PlatformInfo, error) {
		if apiURL != wantAPIURL {
			t.Fatalf("apiURL = %q, want %q", apiURL, wantAPIURL)
		}
		return info, nil
	}
}

func fetchOIDCDiscoveryExpecting(t *testing.T, wantIssuer string) func(Context, string) (OIDCDiscovery, error) {
	return func(_ Context, issuer string) (OIDCDiscovery, error) {
		if issuer != wantIssuer {
			t.Fatalf("issuer = %q, want %q", issuer, wantIssuer)
		}
		return OIDCDiscovery{Issuer: issuer}, nil
	}
}

func TestInitERunCloudProviderRecordsAliasIssuerAndClientID(t *testing.T) {
	store := erunTestCloudStore{}
	deps := CloudDependencies{
		FetchPlatformInfo:  fetchPlatformInfoExpecting(t, "https://api.example.test", PlatformInfo{Issuer: "https://auth.example.test", CLIClientID: "cli-client-1"}),
		FetchOIDCDiscovery: fetchOIDCDiscoveryExpecting(t, "https://auth.example.test"),
	}

	provider, err := InitERunCloudProvider(Context{}, &store, InitERunCloudProviderParams{APIURL: "https://api.example.test/"}, deps)
	if err != nil {
		t.Fatalf("InitERunCloudProvider: %v", err)
	}
	wantERun := &ERunProviderConfig{APIURL: "https://api.example.test", ClientID: "cli-client-1"}
	if provider.Provider != CloudProviderERun || provider.OIDCIssuerURL != "https://auth.example.test" || *provider.ERun != *wantERun {
		t.Fatalf("provider = %+v (ERun=%+v), want Provider=%s OIDCIssuerURL=https://auth.example.test ERun=%+v",
			provider, provider.ERun, CloudProviderERun, wantERun)
	}
	if !strings.HasSuffix(provider.Alias, "@erun") {
		t.Fatalf("Alias = %q, want an @erun suffix", provider.Alias)
	}
	if len(store.config.CloudProviders) != 1 {
		t.Fatalf("saved %d providers, want 1", len(store.config.CloudProviders))
	}
}

func TestInitERunCloudProviderDryRunPerformsNoWrite(t *testing.T) {
	store := erunTestCloudStore{}
	deps := CloudDependencies{
		FetchPlatformInfo: func(Context, string) (PlatformInfo, error) {
			t.Fatal("dry run must not fetch platform info for real")
			return PlatformInfo{}, nil
		},
	}
	provider, err := InitERunCloudProvider(Context{DryRun: true}, &store, InitERunCloudProviderParams{APIURL: "https://api.example.test"}, deps)
	if err != nil {
		t.Fatalf("InitERunCloudProvider dry run: %v", err)
	}
	if provider.Provider != CloudProviderERun {
		t.Fatalf("Provider = %q", provider.Provider)
	}
	if len(store.config.CloudProviders) != 0 {
		t.Fatalf("dry run must not persist a provider, got %d", len(store.config.CloudProviders))
	}
}

func TestInitERunCloudProviderRequiresAPIURL(t *testing.T) {
	store := erunTestCloudStore{}
	if _, err := InitERunCloudProvider(Context{}, &store, InitERunCloudProviderParams{}, CloudDependencies{}); err == nil {
		t.Fatal("expected an error for a missing api url")
	}
}

func TestInitERunCloudProviderRejectsMissingIssuerOrClientID(t *testing.T) {
	cases := map[string]PlatformInfo{
		"missing issuer":    {CLIClientID: "cli-1"},
		"missing client id": {Issuer: "https://auth.example.test"},
	}
	for name, info := range cases {
		t.Run(name, func(t *testing.T) {
			store := erunTestCloudStore{}
			deps := CloudDependencies{FetchPlatformInfo: func(Context, string) (PlatformInfo, error) { return info, nil }}
			if _, err := InitERunCloudProvider(Context{}, &store, InitERunCloudProviderParams{APIURL: "https://api.example.test"}, deps); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func erunTestProvider() CloudProviderConfig {
	return NormalizeCloudProviderConfig(CloudProviderConfig{
		Alias:         "erun+api.example.test@erun",
		Provider:      CloudProviderERun,
		OIDCIssuerURL: "https://auth.example.test",
		ERun:          &ERunProviderConfig{APIURL: "https://api.example.test", ClientID: "cli-client-1"},
	})
}

func startDeviceAuthorizationExpecting(t *testing.T, wantClientID string, auth ERunDeviceAuthorization) func(Context, OIDCDiscovery, string, string) (ERunDeviceAuthorization, error) {
	return func(_ Context, _ OIDCDiscovery, clientID, scope string) (ERunDeviceAuthorization, error) {
		if clientID != wantClientID {
			t.Fatalf("clientID = %q, want %q", clientID, wantClientID)
		}
		if scope == "" {
			t.Fatal("device authorization must request a scope")
		}
		return auth, nil
	}
}

func pollDeviceTokenExpecting(t *testing.T, wantDeviceCode string, tokens ERunTokens) func(Context, OIDCDiscovery, string, ERunDeviceAuthorization) (ERunTokens, error) {
	return func(_ Context, _ OIDCDiscovery, _ string, auth ERunDeviceAuthorization) (ERunTokens, error) {
		if auth.DeviceCode != wantDeviceCode {
			t.Fatalf("DeviceCode = %q, want %q", auth.DeviceCode, wantDeviceCode)
		}
		return tokens, nil
	}
}

func TestERunCloudProviderLoginDeviceFlowPersistsTokensAndCache(t *testing.T) {
	store := erunTestCloudStore{config: ERunConfig{CloudProviders: []CloudProviderConfig{erunTestProvider()}}}
	secrets := NewFileCloudSecretStore(t.TempDir())
	provider := erunTestProvider()

	deps := CloudDependencies{
		CloudSecretStore: secrets,
		FetchOIDCDiscovery: func(Context, string) (OIDCDiscovery, error) {
			return OIDCDiscovery{Issuer: provider.OIDCIssuerURL, DeviceAuthorizationEndpoint: "https://auth.example.test/device", TokenEndpoint: "https://auth.example.test/token"}, nil
		},
		StartERunDeviceAuthorization: startDeviceAuthorizationExpecting(t, "cli-client-1", ERunDeviceAuthorization{
			DeviceCode: "device-code-1", UserCode: "ABCD-EFGH", VerificationURIComplete: "https://auth.example.test/device?code=ABCD-EFGH",
		}),
		PollERunDeviceToken: pollDeviceTokenExpecting(t, "device-code-1", ERunTokens{AccessToken: "access-1", RefreshToken: "refresh-1", ExpiresIn: time.Hour}),
	}

	status, err := LoginCloudProviderAlias(Context{}, &store, CloudLoginParams{Alias: provider.Alias}, deps)
	if err != nil {
		t.Fatalf("LoginCloudProviderAlias: %v", err)
	}
	if status.Status != CloudTokenStatusActive {
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

// TestERunCloudProviderLoginDegradesWhenIssuerRejectsOrgClaimScope proves
// erun#1605's third item degrades gracefully: the org-claim scope is
// requested by default, but an issuer that has never heard of it (a
// dedicated/BYO IdP, the common case, not the shipped Zitadel) must still let
// the login through — exactly as it did before this default was added —
// rather than breaking on an invalid_scope refusal.
func TestERunCloudProviderLoginDegradesWhenIssuerRejectsOrgClaimScope(t *testing.T) {
	store := erunTestCloudStore{config: ERunConfig{CloudProviders: []CloudProviderConfig{erunTestProvider()}}}
	secrets := NewFileCloudSecretStore(t.TempDir())
	provider := erunTestProvider()

	var gotScopes []string
	deps := CloudDependencies{
		CloudSecretStore: secrets,
		FetchOIDCDiscovery: func(Context, string) (OIDCDiscovery, error) {
			return OIDCDiscovery{Issuer: provider.OIDCIssuerURL, DeviceAuthorizationEndpoint: "https://auth.example.test/device", TokenEndpoint: "https://auth.example.test/token"}, nil
		},
		StartERunDeviceAuthorization: func(_ Context, _ OIDCDiscovery, _ string, scope string) (ERunDeviceAuthorization, error) {
			gotScopes = append(gotScopes, scope)
			if strings.Contains(scope, erunOrgClaimScope) {
				return ERunDeviceAuthorization{}, errERunInvalidScope
			}
			return ERunDeviceAuthorization{DeviceCode: "device-code-degrade", UserCode: "DGRD-1234"}, nil
		},
		PollERunDeviceToken: func(Context, OIDCDiscovery, string, ERunDeviceAuthorization) (ERunTokens, error) {
			return ERunTokens{AccessToken: "access-degrade", RefreshToken: "refresh-degrade", ExpiresIn: time.Hour}, nil
		},
	}

	status, err := LoginCloudProviderAlias(Context{}, &store, CloudLoginParams{Alias: provider.Alias}, deps)
	if err != nil {
		t.Fatalf("LoginCloudProviderAlias: %v", err)
	}
	if status.Status != CloudTokenStatusActive {
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
	store := erunTestCloudStore{config: ERunConfig{CloudProviders: []CloudProviderConfig{erunTestProvider()}}}
	secrets := NewFileCloudSecretStore(t.TempDir())
	provider := erunTestProvider()

	called := false
	deps := CloudDependencies{
		CloudSecretStore: secrets,
		FetchOIDCDiscovery: func(Context, string) (OIDCDiscovery, error) {
			// No DeviceAuthorizationEndpoint: the issuer does not support the
			// device grant, so login must fall back to the PKCE path.
			return OIDCDiscovery{Issuer: provider.OIDCIssuerURL, AuthorizationEndpoint: "https://auth.example.test/authorize", TokenEndpoint: "https://auth.example.test/token"}, nil
		},
		StartERunDeviceAuthorization: func(Context, OIDCDiscovery, string, string) (ERunDeviceAuthorization, error) {
			t.Fatal("device authorization must not be started when the issuer advertises no device endpoint")
			return ERunDeviceAuthorization{}, nil
		},
		RunERunAuthCodeLogin: func(_ Context, discovery OIDCDiscovery, clientID, scope string) (ERunTokens, error) {
			called = true
			if clientID != "cli-client-1" {
				t.Fatalf("clientID = %q", clientID)
			}
			return ERunTokens{AccessToken: "access-2", RefreshToken: "refresh-2", ExpiresIn: time.Hour}, nil
		},
	}

	if _, err := LoginCloudProviderAlias(Context{}, &store, CloudLoginParams{Alias: provider.Alias}, deps); err != nil {
		t.Fatalf("LoginCloudProviderAlias: %v", err)
	}
	if !called {
		t.Fatal("expected the Authorization Code + PKCE fallback to run")
	}
}

func TestERunCloudProviderLogoutDeletesRefreshAndCachedAccessToken(t *testing.T) {
	secrets := NewFileCloudSecretStore(t.TempDir())
	provider := erunTestProvider()
	provider.ERun.RefreshTokenRef = erunRefreshTokenRef(provider.Alias)
	if err := secrets.SaveCloudSecret(provider.ERun.RefreshTokenRef, "refresh-1"); err != nil {
		t.Fatalf("seed refresh token: %v", err)
	}
	if err := saveCachedERunAccessToken(secrets, provider.Alias, ERunTokens{AccessToken: "access-1", ExpiresIn: time.Hour}); err != nil {
		t.Fatalf("seed cached access token: %v", err)
	}

	if err := erunCloudProviderLogout(Context{}, provider, CloudDependencies{CloudSecretStore: secrets}); err != nil {
		t.Fatalf("erunCloudProviderLogout: %v", err)
	}
	if _, err := secrets.LoadCloudSecret(provider.ERun.RefreshTokenRef); err == nil {
		t.Fatal("expected the refresh token to be deleted")
	}
	if _, ok := loadCachedERunAccessToken(secrets, provider.Alias); ok {
		t.Fatal("expected the cached access token to be deleted")
	}
}

func TestResolveERunAccessTokenReturnsCachedTokenWithoutRefreshing(t *testing.T) {
	secrets := NewFileCloudSecretStore(t.TempDir())
	provider := erunTestProvider()
	if err := saveCachedERunAccessToken(secrets, provider.Alias, ERunTokens{AccessToken: "cached-token", ExpiresIn: time.Hour}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	deps := CloudDependencies{
		CloudSecretStore: secrets,
		RefreshERunTokens: func(Context, OIDCDiscovery, string, string) (ERunTokens, error) {
			t.Fatal("must not refresh when a fresh cached token exists")
			return ERunTokens{}, nil
		},
	}
	token, err := resolveERunAccessToken(Context{}, provider, deps)
	if err != nil || token != "cached-token" {
		t.Fatalf("resolveERunAccessToken = %q, %v; want cached-token, nil", token, err)
	}
}

func TestResolveERunAccessTokenRefreshesWhenCacheExpiredOrMissing(t *testing.T) {
	secrets := NewFileCloudSecretStore(t.TempDir())
	provider := erunTestProvider()
	provider.ERun.RefreshTokenRef = erunRefreshTokenRef(provider.Alias)
	if err := secrets.SaveCloudSecret(provider.ERun.RefreshTokenRef, "refresh-1"); err != nil {
		t.Fatalf("seed refresh token: %v", err)
	}
	refreshCalls := 0
	deps := CloudDependencies{
		CloudSecretStore: secrets,
		FetchOIDCDiscovery: func(Context, string) (OIDCDiscovery, error) {
			return OIDCDiscovery{TokenEndpoint: "https://auth.example.test/token"}, nil
		},
		RefreshERunTokens: func(_ Context, _ OIDCDiscovery, _ string, refreshToken string) (ERunTokens, error) {
			refreshCalls++
			if refreshToken != "refresh-1" {
				t.Fatalf("refreshToken = %q", refreshToken)
			}
			return ERunTokens{AccessToken: "fresh-token", ExpiresIn: time.Hour}, nil
		},
	}
	token, err := resolveERunAccessToken(Context{}, provider, deps)
	if err != nil || token != "fresh-token" {
		t.Fatalf("resolveERunAccessToken = %q, %v; want fresh-token, nil", token, err)
	}
	if refreshCalls != 1 {
		t.Fatalf("refresh called %d times, want 1", refreshCalls)
	}
	if cached, ok := loadCachedERunAccessToken(secrets, provider.Alias); !ok || cached != "fresh-token" {
		t.Fatalf("refreshed token was not cached: %q, %v", cached, ok)
	}
}

func TestResolveERunAccessTokenFailsClearlyWithoutRefreshToken(t *testing.T) {
	secrets := NewFileCloudSecretStore(t.TempDir())
	provider := erunTestProvider()
	deps := CloudDependencies{CloudSecretStore: secrets}
	if _, err := resolveERunAccessToken(Context{}, provider, deps); err == nil {
		t.Fatal("expected an error when neither a cached nor a refresh token is available")
	} else if !strings.Contains(err.Error(), "erun cloud login") {
		t.Fatalf("error %q does not point at `erun cloud login`", err.Error())
	}
}

func TestERunCloudProviderTokenStatusReflectsResolution(t *testing.T) {
	t.Run("not configured without a refresh token", func(t *testing.T) {
		secrets := NewFileCloudSecretStore(t.TempDir())
		status := erunCloudProviderTokenStatus(erunTestProvider(), CloudDependencies{CloudSecretStore: secrets})
		if status.Status != CloudTokenStatusNotConfigured {
			t.Fatalf("Status = %q, want not_configured", status.Status)
		}
	})

	t.Run("active with a fresh cached token", func(t *testing.T) {
		secrets := NewFileCloudSecretStore(t.TempDir())
		provider := erunTestProvider()
		if err := saveCachedERunAccessToken(secrets, provider.Alias, ERunTokens{AccessToken: "access-1", ExpiresIn: time.Hour}); err != nil {
			t.Fatalf("seed cache: %v", err)
		}
		status := erunCloudProviderTokenStatus(provider, CloudDependencies{CloudSecretStore: secrets})
		if status.Status != CloudTokenStatusActive {
			t.Fatalf("Status = %q, want active", status.Status)
		}
	})

	t.Run("unknown rather than expired when the secret store is absent", func(t *testing.T) {
		provider := erunTestProvider()
		provider.ERun.RefreshTokenRef = erunRefreshTokenRef(provider.Alias)
		status := erunCloudProviderTokenStatus(provider, CloudDependencies{})
		if status.Status != CloudTokenStatusUnknown {
			t.Fatalf("Status = %q, want unknown: a provider with a refresh token but no store to check it against was never actually verified", status.Status)
		}
	})

	t.Run("expired when refresh fails", func(t *testing.T) {
		secrets := NewFileCloudSecretStore(t.TempDir())
		provider := erunTestProvider()
		provider.ERun.RefreshTokenRef = erunRefreshTokenRef(provider.Alias)
		if err := secrets.SaveCloudSecret(provider.ERun.RefreshTokenRef, "refresh-1"); err != nil {
			t.Fatalf("seed refresh token: %v", err)
		}
		deps := CloudDependencies{
			CloudSecretStore:   secrets,
			FetchOIDCDiscovery: func(Context, string) (OIDCDiscovery, error) { return OIDCDiscovery{}, nil },
			RefreshERunTokens: func(Context, OIDCDiscovery, string, string) (ERunTokens, error) {
				return ERunTokens{}, errors.New("refresh_token expired")
			},
		}
		status := erunCloudProviderTokenStatus(provider, deps)
		if status.Status != CloudTokenStatusExpired {
			t.Fatalf("Status = %q, want expired", status.Status)
		}
	})
}

func TestERunPlatformHost(t *testing.T) {
	cases := map[string]string{
		"https://api.frs-prod.services.erunpaas.com": "api.frs-prod.services.erunpaas.com",
		"http://127.0.0.1:8080":                      "127.0.0.1:8080",
	}
	for input, want := range cases {
		if got := erunPlatformHost(input); got != want {
			t.Fatalf("erunPlatformHost(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestCloudProviderBearerTokenERunDryRunPerformsNoNetworkCall(t *testing.T) {
	store := erunTestCloudStore{config: ERunConfig{CloudProviders: []CloudProviderConfig{erunTestProvider()}}}
	provider := erunTestProvider()
	deps := CloudDependencies{
		RefreshERunTokens: func(Context, OIDCDiscovery, string, string) (ERunTokens, error) {
			t.Fatal("dry run must not refresh")
			return ERunTokens{}, nil
		},
	}
	token, err := CloudProviderBearerToken(Context{DryRun: true}, &store, CloudBearerParams{Alias: provider.Alias}, deps)
	if err != nil {
		t.Fatalf("CloudProviderBearerToken dry run: %v", err)
	}
	if token.Provider != CloudProviderERun {
		t.Fatalf("Provider = %q", token.Provider)
	}
}

func TestERunProviderSupportsOIDCIssuerAggregation(t *testing.T) {
	// erun genuinely participates in OIDC (unlike Cloudflare's scoped API
	// token), so it must not be exempted from issuer aggregation.
	if !cloudProviderSupportsOIDC(CloudProviderERun) {
		t.Fatal("cloudProviderSupportsOIDC(erun) = false, want true")
	}
}

func TestERunCloudProviderBearerTokenRequiresConfiguration(t *testing.T) {
	provider := NormalizeCloudProviderConfig(CloudProviderConfig{Alias: "erun+x@erun", Provider: CloudProviderERun})
	if _, err := erunCloudProviderBearerToken(Context{}, provider, CloudDependencies{CloudSecretStore: NewFileCloudSecretStore(t.TempDir())}); err == nil {
		t.Fatal("expected an error for a provider missing its ERun client id")
	}
}

func TestERunCloudProviderLoginRequiresInit(t *testing.T) {
	store := erunTestCloudStore{}
	provider := NormalizeCloudProviderConfig(CloudProviderConfig{Alias: "erun+x@erun", Provider: CloudProviderERun})
	if _, err := erunCloudProviderLogin(Context{}, &store, provider, "", nil, CloudDependencies{}); err == nil {
		t.Fatal("expected an error for a provider with no ERun config")
	} else if !strings.Contains(err.Error(), "cloud init erun") {
		t.Fatalf("error %q does not point at `erun cloud init erun`", err.Error())
	}
}

// A device grant that cannot complete must not be a dead end: the issuer
// advertises the endpoint, so auto starts there, but the authorization-code
// path still has to run when it fails. Regression for issue #1603, where one
// broken authentication method locked the CLI out entirely.
func TestERunCloudProviderLoginFallsBackToAuthCodeWhenDeviceFlowFails(t *testing.T) {
	store := erunTestCloudStore{config: ERunConfig{CloudProviders: []CloudProviderConfig{erunTestProvider()}}}
	secrets := NewFileCloudSecretStore(t.TempDir())
	provider := erunTestProvider()

	deviceStarted, authCodeCalled := false, false
	deps := CloudDependencies{
		CloudSecretStore: secrets,
		FetchOIDCDiscovery: func(Context, string) (OIDCDiscovery, error) {
			return OIDCDiscovery{Issuer: provider.OIDCIssuerURL, DeviceAuthorizationEndpoint: "https://auth.example.test/device", AuthorizationEndpoint: "https://auth.example.test/authorize", TokenEndpoint: "https://auth.example.test/token"}, nil
		},
		StartERunDeviceAuthorization: func(Context, OIDCDiscovery, string, string) (ERunDeviceAuthorization, error) {
			deviceStarted = true
			return ERunDeviceAuthorization{DeviceCode: "device-code-3", UserCode: "WXYZ-1234"}, nil
		},
		PollERunDeviceToken: func(Context, OIDCDiscovery, string, ERunDeviceAuthorization) (ERunTokens, error) {
			return ERunTokens{}, fmt.Errorf("device authorization expired before sign-in completed")
		},
		RunERunAuthCodeLogin: func(Context, OIDCDiscovery, string, string) (ERunTokens, error) {
			authCodeCalled = true
			return ERunTokens{AccessToken: "access-3", RefreshToken: "refresh-3", ExpiresIn: time.Hour}, nil
		},
	}

	status, err := LoginCloudProviderAlias(Context{}, &store, CloudLoginParams{Alias: provider.Alias}, deps)
	if err != nil {
		t.Fatalf("LoginCloudProviderAlias: %v", err)
	}
	if !deviceStarted {
		t.Fatal("auto must try the device grant first when the issuer advertises one")
	}
	if !authCodeCalled {
		t.Fatal("auto must fall back to authorization code + PKCE when the device grant fails")
	}
	if status.Status != CloudTokenStatusActive {
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
	store := erunTestCloudStore{config: ERunConfig{CloudProviders: []CloudProviderConfig{erunTestProvider()}}}
	provider := erunTestProvider()

	deps := CloudDependencies{
		CloudSecretStore: NewFileCloudSecretStore(t.TempDir()),
		FetchOIDCDiscovery: func(Context, string) (OIDCDiscovery, error) {
			return OIDCDiscovery{Issuer: provider.OIDCIssuerURL, DeviceAuthorizationEndpoint: "https://auth.example.test/device", AuthorizationEndpoint: "https://auth.example.test/authorize", TokenEndpoint: "https://auth.example.test/token"}, nil
		},
		StartERunDeviceAuthorization: func(Context, OIDCDiscovery, string, string) (ERunDeviceAuthorization, error) {
			t.Fatal("an explicit authcode flow must not start the device grant")
			return ERunDeviceAuthorization{}, nil
		},
		RunERunAuthCodeLogin: func(Context, OIDCDiscovery, string, string) (ERunTokens, error) {
			return ERunTokens{AccessToken: "access-4", RefreshToken: "refresh-4", ExpiresIn: time.Hour}, nil
		},
	}

	if _, err := LoginCloudProviderAlias(Context{}, &store, CloudLoginParams{Alias: provider.Alias, Flow: ERunLoginFlowAuthCode}, deps); err != nil {
		t.Fatalf("LoginCloudProviderAlias: %v", err)
	}
}

// An explicit --flow device against an issuer without the endpoint fails
// loudly, naming the flow that would work, rather than silently doing
// something the operator did not ask for.
func TestERunCloudProviderLoginExplicitDeviceFlowWithoutEndpointErrors(t *testing.T) {
	store := erunTestCloudStore{config: ERunConfig{CloudProviders: []CloudProviderConfig{erunTestProvider()}}}
	provider := erunTestProvider()

	deps := CloudDependencies{
		CloudSecretStore: NewFileCloudSecretStore(t.TempDir()),
		FetchOIDCDiscovery: func(Context, string) (OIDCDiscovery, error) {
			return OIDCDiscovery{Issuer: provider.OIDCIssuerURL, AuthorizationEndpoint: "https://auth.example.test/authorize", TokenEndpoint: "https://auth.example.test/token"}, nil
		},
		RunERunAuthCodeLogin: func(Context, OIDCDiscovery, string, string) (ERunTokens, error) {
			t.Fatal("an explicit device flow must not silently use authorization code")
			return ERunTokens{}, nil
		},
	}

	_, err := LoginCloudProviderAlias(Context{}, &store, CloudLoginParams{Alias: provider.Alias, Flow: ERunLoginFlowDevice}, deps)
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
	store := erunTestCloudStore{config: ERunConfig{CloudProviders: []CloudProviderConfig{erunTestProvider()}}}
	provider := erunTestProvider()

	var gotScope string
	deps := CloudDependencies{
		CloudSecretStore: NewFileCloudSecretStore(t.TempDir()),
		FetchOIDCDiscovery: func(Context, string) (OIDCDiscovery, error) {
			return OIDCDiscovery{Issuer: provider.OIDCIssuerURL, AuthorizationEndpoint: "https://auth.example.test/authorize", TokenEndpoint: "https://auth.example.test/token"}, nil
		},
		RunERunAuthCodeLogin: func(_ Context, _ OIDCDiscovery, _ string, scope string) (ERunTokens, error) {
			gotScope = scope
			return ERunTokens{AccessToken: "access-5", RefreshToken: "refresh-5", ExpiresIn: time.Hour}, nil
		},
	}

	params := CloudLoginParams{Alias: provider.Alias, Scopes: []string{"urn:zitadel:iam:user:resourceowner"}}
	if _, err := LoginCloudProviderAlias(Context{}, &store, params, deps); err != nil {
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
