package cloud

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/platformclient"
)

// erunTestCloudStore is a mutable CloudStore stub: unlike
// stubCloudContextStore (whose SaveERunConfig is a no-op), tests here need to
// observe what a real save persisted.
type erunTestCloudStore struct {
	config configpkg.ERunConfig
}

func (s *erunTestCloudStore) LoadERunConfig() (configpkg.ERunConfig, string, error) {
	return s.config, "", nil
}

func (s *erunTestCloudStore) SaveERunConfig(config configpkg.ERunConfig) error {
	s.config = config
	return nil
}

func fetchPlatformInfoExpecting(t *testing.T, wantAPIURL string, info platformclient.PlatformInfo) func(core.Context, string) (platformclient.PlatformInfo, error) {
	return func(_ core.Context, apiURL string) (platformclient.PlatformInfo, error) {
		if apiURL != wantAPIURL {
			t.Fatalf("apiURL = %q, want %q", apiURL, wantAPIURL)
		}
		return info, nil
	}
}

func fetchOIDCDiscoveryExpecting(t *testing.T, wantIssuer string) func(core.Context, string) (OIDCDiscovery, error) {
	return func(_ core.Context, issuer string) (OIDCDiscovery, error) {
		if issuer != wantIssuer {
			t.Fatalf("issuer = %q, want %q", issuer, wantIssuer)
		}
		return OIDCDiscovery{Issuer: issuer}, nil
	}
}

func TestInitERunCloudProviderRecordsAliasIssuerAndClientID(t *testing.T) {
	store := erunTestCloudStore{}
	deps := CloudDependencies{
		FetchPlatformInfo:  fetchPlatformInfoExpecting(t, "https://api.example.test", platformclient.PlatformInfo{Issuer: "https://auth.example.test", CLIClientID: "cli-client-1"}),
		FetchOIDCDiscovery: fetchOIDCDiscoveryExpecting(t, "https://auth.example.test"),
	}

	provider, err := InitERunCloudProvider(core.Context{}, &store, InitERunCloudProviderParams{APIURL: "https://api.example.test/"}, deps)
	if err != nil {
		t.Fatalf("InitERunCloudProvider: %v", err)
	}
	wantERun := &configpkg.ERunProviderConfig{APIURL: "https://api.example.test", ClientID: "cli-client-1"}
	if provider.Provider != configpkg.CloudProviderERun || provider.OIDCIssuerURL != "https://auth.example.test" || *provider.ERun != *wantERun {
		t.Fatalf("provider = %+v (ERun=%+v), want Provider=%s OIDCIssuerURL=https://auth.example.test ERun=%+v",
			provider, provider.ERun, configpkg.CloudProviderERun, wantERun)
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
		FetchPlatformInfo: func(core.Context, string) (platformclient.PlatformInfo, error) {
			t.Fatal("dry run must not fetch platform info for real")
			return platformclient.PlatformInfo{}, nil
		},
	}
	provider, err := InitERunCloudProvider(core.Context{DryRun: true}, &store, InitERunCloudProviderParams{APIURL: "https://api.example.test"}, deps)
	if err != nil {
		t.Fatalf("InitERunCloudProvider dry run: %v", err)
	}
	if provider.Provider != configpkg.CloudProviderERun {
		t.Fatalf("Provider = %q", provider.Provider)
	}
	if len(store.config.CloudProviders) != 0 {
		t.Fatalf("dry run must not persist a provider, got %d", len(store.config.CloudProviders))
	}
}

func TestInitERunCloudProviderRequiresAPIURL(t *testing.T) {
	store := erunTestCloudStore{}
	if _, err := InitERunCloudProvider(core.Context{}, &store, InitERunCloudProviderParams{}, CloudDependencies{}); err == nil {
		t.Fatal("expected an error for a missing api url")
	}
}

func TestInitERunCloudProviderRejectsMissingIssuerOrClientID(t *testing.T) {
	cases := map[string]platformclient.PlatformInfo{
		"missing issuer":    {CLIClientID: "cli-1"},
		"missing client id": {Issuer: "https://auth.example.test"},
	}
	for name, info := range cases {
		t.Run(name, func(t *testing.T) {
			store := erunTestCloudStore{}
			deps := CloudDependencies{FetchPlatformInfo: func(core.Context, string) (platformclient.PlatformInfo, error) { return info, nil }}
			if _, err := InitERunCloudProvider(core.Context{}, &store, InitERunCloudProviderParams{APIURL: "https://api.example.test"}, deps); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func erunTestProvider() configpkg.CloudProviderConfig {
	return NormalizeCloudProviderConfig(configpkg.CloudProviderConfig{
		Alias:         "erun+api.example.test@erun",
		Provider:      configpkg.CloudProviderERun,
		OIDCIssuerURL: "https://auth.example.test",
		ERun:          &configpkg.ERunProviderConfig{APIURL: "https://api.example.test", ClientID: "cli-client-1"},
	})
}

func TestERunCloudProviderLogoutDeletesRefreshAndCachedAccessToken(t *testing.T) {
	secrets := NewFileCloudSecretStore(t.TempDir())
	provider := erunTestProvider()
	provider.ERun.RefreshTokenRef = ErunRefreshTokenRef(provider.Alias)
	if err := secrets.SaveCloudSecret(provider.ERun.RefreshTokenRef, "refresh-1"); err != nil {
		t.Fatalf("seed refresh token: %v", err)
	}
	if err := SaveCachedERunAccessToken(secrets, provider.Alias, ERunTokens{AccessToken: "access-1", ExpiresIn: time.Hour}); err != nil {
		t.Fatalf("seed cached access token: %v", err)
	}

	if err := erunCloudProviderLogout(core.Context{}, provider, CloudDependencies{CloudSecretStore: secrets}); err != nil {
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
	if err := SaveCachedERunAccessToken(secrets, provider.Alias, ERunTokens{AccessToken: "cached-token", ExpiresIn: time.Hour}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	deps := CloudDependencies{
		CloudSecretStore: secrets,
		RefreshERunTokens: func(core.Context, OIDCDiscovery, string, string) (ERunTokens, error) {
			t.Fatal("must not refresh when a fresh cached token exists")
			return ERunTokens{}, nil
		},
	}
	token, err := ResolveERunAccessToken(core.Context{}, provider, deps)
	if err != nil || token != "cached-token" {
		t.Fatalf("resolveERunAccessToken = %q, %v; want cached-token, nil", token, err)
	}
}

func TestResolveERunAccessTokenRefreshesWhenCacheExpiredOrMissing(t *testing.T) {
	secrets := NewFileCloudSecretStore(t.TempDir())
	provider := erunTestProvider()
	provider.ERun.RefreshTokenRef = ErunRefreshTokenRef(provider.Alias)
	if err := secrets.SaveCloudSecret(provider.ERun.RefreshTokenRef, "refresh-1"); err != nil {
		t.Fatalf("seed refresh token: %v", err)
	}
	refreshCalls := 0
	deps := CloudDependencies{
		CloudSecretStore: secrets,
		FetchOIDCDiscovery: func(core.Context, string) (OIDCDiscovery, error) {
			return OIDCDiscovery{TokenEndpoint: "https://auth.example.test/token"}, nil
		},
		RefreshERunTokens: func(_ core.Context, _ OIDCDiscovery, _ string, refreshToken string) (ERunTokens, error) {
			refreshCalls++
			if refreshToken != "refresh-1" {
				t.Fatalf("refreshToken = %q", refreshToken)
			}
			return ERunTokens{AccessToken: "fresh-token", ExpiresIn: time.Hour}, nil
		},
	}
	token, err := ResolveERunAccessToken(core.Context{}, provider, deps)
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
	if _, err := ResolveERunAccessToken(core.Context{}, provider, deps); err == nil {
		t.Fatal("expected an error when neither a cached nor a refresh token is available")
	} else if !strings.Contains(err.Error(), "erun cloud login") {
		t.Fatalf("error %q does not point at `erun cloud login`", err.Error())
	}
}

func TestERunCloudProviderTokenStatusReflectsResolution(t *testing.T) {
	t.Run("not configured without a refresh token", func(t *testing.T) {
		secrets := NewFileCloudSecretStore(t.TempDir())
		status := erunCloudProviderTokenStatus(erunTestProvider(), CloudDependencies{CloudSecretStore: secrets})
		if status.Status != configpkg.CloudTokenStatusNotConfigured {
			t.Fatalf("Status = %q, want not_configured", status.Status)
		}
	})

	t.Run("active with a fresh cached token", func(t *testing.T) {
		secrets := NewFileCloudSecretStore(t.TempDir())
		provider := erunTestProvider()
		if err := SaveCachedERunAccessToken(secrets, provider.Alias, ERunTokens{AccessToken: "access-1", ExpiresIn: time.Hour}); err != nil {
			t.Fatalf("seed cache: %v", err)
		}
		status := erunCloudProviderTokenStatus(provider, CloudDependencies{CloudSecretStore: secrets})
		if status.Status != configpkg.CloudTokenStatusActive {
			t.Fatalf("Status = %q, want active", status.Status)
		}
	})

	t.Run("unknown rather than expired when the secret store is absent", func(t *testing.T) {
		provider := erunTestProvider()
		provider.ERun.RefreshTokenRef = ErunRefreshTokenRef(provider.Alias)
		status := erunCloudProviderTokenStatus(provider, CloudDependencies{})
		if status.Status != configpkg.CloudTokenStatusUnknown {
			t.Fatalf("Status = %q, want unknown: a provider with a refresh token but no store to check it against was never actually verified", status.Status)
		}
	})

	t.Run("expired when refresh fails", func(t *testing.T) {
		secrets := NewFileCloudSecretStore(t.TempDir())
		provider := erunTestProvider()
		provider.ERun.RefreshTokenRef = ErunRefreshTokenRef(provider.Alias)
		if err := secrets.SaveCloudSecret(provider.ERun.RefreshTokenRef, "refresh-1"); err != nil {
			t.Fatalf("seed refresh token: %v", err)
		}
		deps := CloudDependencies{
			CloudSecretStore:   secrets,
			FetchOIDCDiscovery: func(core.Context, string) (OIDCDiscovery, error) { return OIDCDiscovery{}, nil },
			RefreshERunTokens: func(core.Context, OIDCDiscovery, string, string) (ERunTokens, error) {
				return ERunTokens{}, errors.New("refresh_token expired")
			},
		}
		status := erunCloudProviderTokenStatus(provider, deps)
		if status.Status != configpkg.CloudTokenStatusExpired {
			t.Fatalf("Status = %q, want expired", status.Status)
		}
	})
}

// TestERunCloudProviderTokenStatusRefreshTraceStaysOffStdout guards the erun
// side of the same corruption already fixed for Cloudflare: a refresh
// (triggered here by a cached-token miss with a refresh token on file) makes
// resolveERunAccessToken call FetchOIDCDiscovery/RefreshERunTokens, and each
// traces a request line via Context.Trace. A zero-value Context leaves that
// trace's writer unset, which Logger falls back to the real os.Stdout, so the
// only way to see the regression is to capture the real process stdout (a
// caller-supplied Context, even one with a captured Stdout field, is not what
// erunCloudProviderTokenStatus threads into resolveERunAccessToken).
func TestERunCloudProviderTokenStatusRefreshTraceStaysOffStdout(t *testing.T) {
	secrets := NewFileCloudSecretStore(t.TempDir())
	provider := erunTestProvider()
	provider.ERun.RefreshTokenRef = ErunRefreshTokenRef(provider.Alias)
	if err := secrets.SaveCloudSecret(provider.ERun.RefreshTokenRef, "refresh-1"); err != nil {
		t.Fatalf("seed refresh token: %v", err)
	}
	deps := CloudDependencies{
		CloudSecretStore: secrets,
		FetchOIDCDiscovery: func(ctx core.Context, issuer string) (OIDCDiscovery, error) {
			ctx.Trace("GET https://example.invalid/.well-known/openid-configuration")
			return OIDCDiscovery{TokenEndpoint: "https://example.invalid/token"}, nil
		},
		RefreshERunTokens: func(ctx core.Context, discovery OIDCDiscovery, clientID, refreshToken string) (ERunTokens, error) {
			ctx.Trace("POST " + discovery.TokenEndpoint)
			return ERunTokens{AccessToken: "fresh-token"}, nil
		},
	}

	realStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	status := erunCloudProviderTokenStatus(provider, deps)
	// Mirrors ResolveListResult -> WriteResult: the command's JSON payload is
	// written to stdout after the status check runs.
	_, _ = io.WriteString(w, `{"tenant":"erun"}`+"\n")
	_ = w.Close()
	os.Stdout = realStdout

	var captured bytes.Buffer
	if _, err := io.Copy(&captured, r); err != nil {
		t.Fatalf("read captured stdout: %v", err)
	}

	if status.Status != configpkg.CloudTokenStatusActive {
		t.Fatalf("status = %+v", status)
	}
	var decoded map[string]string
	if err := json.Unmarshal(captured.Bytes(), &decoded); err != nil {
		t.Fatalf("erun-type token refresh corrupted stdout: %v\nstdout was:\n%s", err, captured.String())
	}
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
	store := erunTestCloudStore{config: configpkg.ERunConfig{CloudProviders: []configpkg.CloudProviderConfig{erunTestProvider()}}}
	provider := erunTestProvider()
	deps := CloudDependencies{
		RefreshERunTokens: func(core.Context, OIDCDiscovery, string, string) (ERunTokens, error) {
			t.Fatal("dry run must not refresh")
			return ERunTokens{}, nil
		},
	}
	token, err := CloudProviderBearerToken(core.Context{DryRun: true}, &store, CloudBearerParams{Alias: provider.Alias}, deps)
	if err != nil {
		t.Fatalf("CloudProviderBearerToken dry run: %v", err)
	}
	if token.Provider != configpkg.CloudProviderERun {
		t.Fatalf("Provider = %q", token.Provider)
	}
}

func TestERunProviderSupportsOIDCIssuerAggregation(t *testing.T) {
	// erun genuinely participates in OIDC (unlike Cloudflare's scoped API
	// token), so it must not be exempted from issuer aggregation.
	if !cloudProviderSupportsOIDC(configpkg.CloudProviderERun) {
		t.Fatal("cloudProviderSupportsOIDC(erun) = false, want true")
	}
}

func TestERunCloudProviderBearerTokenRequiresConfiguration(t *testing.T) {
	provider := NormalizeCloudProviderConfig(configpkg.CloudProviderConfig{Alias: "erun+x@erun", Provider: configpkg.CloudProviderERun})
	if _, err := erunCloudProviderBearerToken(core.Context{}, provider, CloudDependencies{CloudSecretStore: NewFileCloudSecretStore(t.TempDir())}); err == nil {
		t.Fatal("expected an error for a provider missing its ERun client id")
	}
}
