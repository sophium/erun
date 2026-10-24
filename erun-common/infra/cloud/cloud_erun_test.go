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
	"github.com/sophium/erun/erun-common/base/model"
)

// erunTestCloudStore records saves, unlike stubCloudContextStore's no-op SaveERunConfig.
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

func TestInitERunCloudProviderRequiresAPIURL(t *testing.T) {
	store := erunTestCloudStore{}
	if _, err := InitERunCloudProvider(core.Context{}, &store, model.InitERunCloudProviderParams{}, CloudDependencies{}); err == nil {
		t.Fatal("expected an error for a missing api url")
	}
}

func TestInitERunCloudProviderRejectsMissingIssuerOrClientID(t *testing.T) {
	cases := map[string]model.PlatformInfo{
		"missing issuer":    {CLIClientID: "cli-1"},
		"missing client id": {Issuer: "https://auth.example.test"},
	}
	for name, info := range cases {
		t.Run(name, func(t *testing.T) {
			store := erunTestCloudStore{}
			deps := CloudDependencies{FetchPlatformInfo: func(core.Context, string) (model.PlatformInfo, error) { return info, nil }}
			if _, err := InitERunCloudProvider(core.Context{}, &store, model.InitERunCloudProviderParams{APIURL: "https://api.example.test"}, deps); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func erunTestProvider() model.CloudProviderConfig {
	return NormalizeCloudProviderConfig(model.CloudProviderConfig{
		Alias:         "erun+api.example.test@erun",
		Provider:      configpkg.CloudProviderERun,
		OIDCIssuerURL: "https://auth.example.test",
		ERun:          &model.ERunProviderConfig{APIURL: "https://api.example.test", ClientID: "cli-client-1"},
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

// A zero-value Context traces to the real os.Stdout, so only capturing the process
// stdout shows a refresh trace leaking into JSON output.
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

func TestERunProviderSupportsOIDCIssuerAggregation(t *testing.T) {
	if !cloudProviderSupportsOIDC(configpkg.CloudProviderERun) {
		t.Fatal("cloudProviderSupportsOIDC(erun) = false, want true")
	}
}

func TestERunCloudProviderBearerTokenRequiresConfiguration(t *testing.T) {
	provider := NormalizeCloudProviderConfig(model.CloudProviderConfig{Alias: "erun+x@erun", Provider: configpkg.CloudProviderERun})
	if _, err := erunCloudProviderBearerToken(core.Context{}, provider, CloudDependencies{CloudSecretStore: NewFileCloudSecretStore(t.TempDir())}); err == nil {
		t.Fatal("expected an error for a provider missing its ERun client id")
	}
}
