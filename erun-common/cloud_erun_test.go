package eruncommon

import (
	"errors"
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

func startDeviceAuthorizationExpecting(t *testing.T, wantClientID string, auth ERunDeviceAuthorization) func(Context, OIDCDiscovery, string) (ERunDeviceAuthorization, error) {
	return func(_ Context, _ OIDCDiscovery, clientID string) (ERunDeviceAuthorization, error) {
		if clientID != wantClientID {
			t.Fatalf("clientID = %q, want %q", clientID, wantClientID)
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
		StartERunDeviceAuthorization: func(Context, OIDCDiscovery, string) (ERunDeviceAuthorization, error) {
			t.Fatal("device authorization must not be started when the issuer advertises no device endpoint")
			return ERunDeviceAuthorization{}, nil
		},
		RunERunAuthCodeLogin: func(_ Context, discovery OIDCDiscovery, clientID string) (ERunTokens, error) {
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

	t.Run("unknown rather than expired when the secret store is absent (#1109)", func(t *testing.T) {
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
	if _, err := erunCloudProviderLogin(Context{}, &store, provider, CloudDependencies{}); err == nil {
		t.Fatal("expected an error for a provider with no ERun config")
	} else if !strings.Contains(err.Error(), "cloud init erun") {
		t.Fatalf("error %q does not point at `erun cloud init erun`", err.Error())
	}
}
