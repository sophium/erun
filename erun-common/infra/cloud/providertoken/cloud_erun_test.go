package providertoken

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/cloud/providerconfig"
	"github.com/sophium/erun/erun-common/infra/cloud/secretstore"
)

func erunTestProvider() model.CloudProviderConfig {
	return providerconfig.NormalizeCloudProviderConfig(model.CloudProviderConfig{
		Alias:         "erun+api.example.test@erun",
		Provider:      configpkg.CloudProviderERun,
		OIDCIssuerURL: "https://auth.example.test",
		ERun:          &model.ERunProviderConfig{APIURL: "https://api.example.test", ClientID: "cli-client-1"},
	})
}

func TestResolveERunAccessTokenFailsClearlyWithoutRefreshToken(t *testing.T) {
	secrets := secretstore.NewFileCloudSecretStore(t.TempDir())
	provider := erunTestProvider()
	deps := cloud.CloudDependencies{CloudSecretStore: secrets}
	if _, err := ResolveERunAccessToken(core.Context{}, provider, deps); err == nil {
		t.Fatal("expected an error when neither a cached nor a refresh token is available")
	} else if !strings.Contains(err.Error(), "erun cloud login") {
		t.Fatalf("error %q does not point at `erun cloud login`", err.Error())
	}
}

func TestERunCloudProviderTokenStatusReflectsResolution(t *testing.T) {
	t.Run("not configured without a refresh token", func(t *testing.T) {
		secrets := secretstore.NewFileCloudSecretStore(t.TempDir())
		status := erunCloudProviderTokenStatus(erunTestProvider(), cloud.CloudDependencies{CloudSecretStore: secrets})
		if status.Status != configpkg.CloudTokenStatusNotConfigured {
			t.Fatalf("Status = %q, want not_configured", status.Status)
		}
	})

	t.Run("unknown rather than expired when the secret store is absent", func(t *testing.T) {
		provider := erunTestProvider()
		provider.ERun.RefreshTokenRef = ErunRefreshTokenRef(provider.Alias)
		status := erunCloudProviderTokenStatus(provider, cloud.CloudDependencies{})
		if status.Status != configpkg.CloudTokenStatusUnknown {
			t.Fatalf("Status = %q, want unknown: a provider with a refresh token but no store to check it against was never actually verified", status.Status)
		}
	})

	t.Run("expired when refresh fails", func(t *testing.T) {
		secrets := secretstore.NewFileCloudSecretStore(t.TempDir())
		provider := erunTestProvider()
		provider.ERun.RefreshTokenRef = ErunRefreshTokenRef(provider.Alias)
		if err := secrets.SaveCloudSecret(provider.ERun.RefreshTokenRef, "refresh-1"); err != nil {
			t.Fatalf("seed refresh token: %v", err)
		}
		deps := cloud.CloudDependencies{
			CloudSecretStore:   secrets,
			FetchOIDCDiscovery: func(core.Context, string) (cloud.OIDCDiscovery, error) { return cloud.OIDCDiscovery{}, nil },
			RefreshERunTokens: func(core.Context, cloud.OIDCDiscovery, string, string) (cloud.ERunTokens, error) {
				return cloud.ERunTokens{}, errors.New("refresh_token expired")
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
	secrets := secretstore.NewFileCloudSecretStore(t.TempDir())
	provider := erunTestProvider()
	provider.ERun.RefreshTokenRef = ErunRefreshTokenRef(provider.Alias)
	if err := secrets.SaveCloudSecret(provider.ERun.RefreshTokenRef, "refresh-1"); err != nil {
		t.Fatalf("seed refresh token: %v", err)
	}
	deps := cloud.CloudDependencies{
		CloudSecretStore: secrets,
		FetchOIDCDiscovery: func(ctx core.Context, issuer string) (cloud.OIDCDiscovery, error) {
			ctx.Trace("GET https://example.invalid/.well-known/openid-configuration")
			return cloud.OIDCDiscovery{TokenEndpoint: "https://example.invalid/token"}, nil
		},
		RefreshERunTokens: func(ctx core.Context, discovery cloud.OIDCDiscovery, clientID, refreshToken string) (cloud.ERunTokens, error) {
			ctx.Trace("POST " + discovery.TokenEndpoint)
			return cloud.ERunTokens{AccessToken: "fresh-token"}, nil
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

func TestERunCloudProviderBearerTokenRequiresConfiguration(t *testing.T) {
	provider := providerconfig.NormalizeCloudProviderConfig(model.CloudProviderConfig{Alias: "erun+x@erun", Provider: configpkg.CloudProviderERun})
	if _, err := erunCloudProviderBearerToken(core.Context{}, provider, cloud.CloudDependencies{CloudSecretStore: secretstore.NewFileCloudSecretStore(t.TempDir())}); err == nil {
		t.Fatal("expected an error for a provider missing its ERun client id")
	}
}
