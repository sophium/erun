package providerlifecycle

import (
	"fmt"
	"strings"
	"testing"
	"time"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/cloud/clouddefaults"
	"github.com/sophium/erun/erun-common/infra/cloud/providerconfig"
	"github.com/sophium/erun/erun-common/infra/cloud/providertoken"
	"github.com/sophium/erun/erun-common/infra/cloud/secretstore"
)

// An issuer that rejects the org-claim scope must still let login through.
func TestERunCloudProviderLoginDegradesWhenIssuerRejectsOrgClaimScope(t *testing.T) {
	store := erunTestCloudStore{config: configpkg.ERunConfig{CloudProviders: []model.CloudProviderConfig{erunTestProvider()}}}
	secrets := secretstore.NewFileCloudSecretStore(t.TempDir())
	provider := erunTestProvider()

	var gotScopes []string
	deps := cloud.CloudDependencies{
		CloudSecretStore: secrets,
		FetchOIDCDiscovery: func(core.Context, string) (cloud.OIDCDiscovery, error) {
			return cloud.OIDCDiscovery{Issuer: provider.OIDCIssuerURL, DeviceAuthorizationEndpoint: "https://auth.example.test/device", TokenEndpoint: "https://auth.example.test/token"}, nil
		},
		StartERunDeviceAuthorization: func(_ core.Context, _ cloud.OIDCDiscovery, _ string, scope string) (cloud.ERunDeviceAuthorization, error) {
			gotScopes = append(gotScopes, scope)
			if strings.Contains(scope, clouddefaults.ErunOrgClaimScope) {
				return cloud.ERunDeviceAuthorization{}, clouddefaults.ErrERunInvalidScope
			}
			return cloud.ERunDeviceAuthorization{DeviceCode: "device-code-degrade", UserCode: "DGRD-1234"}, nil
		},
		PollERunDeviceToken: func(core.Context, cloud.OIDCDiscovery, string, cloud.ERunDeviceAuthorization) (cloud.ERunTokens, error) {
			return cloud.ERunTokens{AccessToken: "access-degrade", RefreshToken: "refresh-degrade", ExpiresIn: time.Hour}, nil
		},
	}

	status, err := LoginCloudProviderAlias(core.Context{}, &store, model.CloudLoginParams{Alias: provider.Alias}, deps)
	if err != nil {
		t.Fatalf("LoginCloudProviderAlias: %v", err)
	}
	if status.Status != configpkg.CloudTokenStatusActive {
		t.Fatalf("Status = %q, want active", status.Status)
	}
	assertDegradedScopeAttempts(t, gotScopes)
	if cached, ok := providertoken.LoadCachedERunAccessToken(secrets, provider.Alias); !ok || cached != "access-degrade" {
		t.Fatalf("cached = %q (ok=%v), want the fallback attempt's token persisted", cached, ok)
	}
}

func assertDegradedScopeAttempts(t *testing.T, gotScopes []string) {
	t.Helper()
	if len(gotScopes) != 2 {
		t.Fatalf("expected exactly 2 device-authorization attempts (with, then without, the org-claim scope), got %v", gotScopes)
	}
	if !strings.Contains(gotScopes[0], clouddefaults.ErunOrgClaimScope) {
		t.Fatalf("first attempt scope = %q, want the org-claim scope requested by default", gotScopes[0])
	}
	if strings.Contains(gotScopes[1], clouddefaults.ErunOrgClaimScope) {
		t.Fatalf("fallback attempt scope = %q, must not repeat the scope the issuer just rejected", gotScopes[1])
	}
	for _, baseline := range []string{"openid", "offline_access"} {
		if !strings.Contains(gotScopes[1], baseline) {
			t.Fatalf("fallback scope = %q, want the %q baseline kept", gotScopes[1], baseline)
		}
	}
}

func TestERunCloudProviderLoginFallsBackToAuthCodeWithoutDeviceEndpoint(t *testing.T) {
	store := erunTestCloudStore{config: configpkg.ERunConfig{CloudProviders: []model.CloudProviderConfig{erunTestProvider()}}}
	secrets := secretstore.NewFileCloudSecretStore(t.TempDir())
	provider := erunTestProvider()

	called := false
	deps := cloud.CloudDependencies{
		CloudSecretStore: secrets,
		FetchOIDCDiscovery: func(core.Context, string) (cloud.OIDCDiscovery, error) {
			// No DeviceAuthorizationEndpoint: login must fall back to PKCE.
			return cloud.OIDCDiscovery{Issuer: provider.OIDCIssuerURL, AuthorizationEndpoint: "https://auth.example.test/authorize", TokenEndpoint: "https://auth.example.test/token"}, nil
		},
		StartERunDeviceAuthorization: func(core.Context, cloud.OIDCDiscovery, string, string) (cloud.ERunDeviceAuthorization, error) {
			t.Fatal("device authorization must not be started when the issuer advertises no device endpoint")
			return cloud.ERunDeviceAuthorization{}, nil
		},
		RunERunAuthCodeLogin: func(_ core.Context, discovery cloud.OIDCDiscovery, clientID, scope string) (cloud.ERunTokens, error) {
			called = true
			if clientID != "cli-client-1" {
				t.Fatalf("clientID = %q", clientID)
			}
			return cloud.ERunTokens{AccessToken: "access-2", RefreshToken: "refresh-2", ExpiresIn: time.Hour}, nil
		},
	}

	if _, err := LoginCloudProviderAlias(core.Context{}, &store, model.CloudLoginParams{Alias: provider.Alias}, deps); err != nil {
		t.Fatalf("LoginCloudProviderAlias: %v", err)
	}
	if !called {
		t.Fatal("expected the Authorization Code + PKCE fallback to run")
	}
}

func TestERunCloudProviderLoginRequiresInit(t *testing.T) {
	store := erunTestCloudStore{}
	provider := providerconfig.NormalizeCloudProviderConfig(model.CloudProviderConfig{Alias: "erun+x@erun", Provider: configpkg.CloudProviderERun})
	if _, err := erunCloudProviderLogin(core.Context{}, &store, provider, "", nil, cloud.CloudDependencies{}); err == nil {
		t.Fatal("expected an error for a provider with no ERun config")
	} else if !strings.Contains(err.Error(), "cloud init erun") {
		t.Fatalf("error %q does not point at `erun cloud init erun`", err.Error())
	}
}

func TestERunCloudProviderLoginFallsBackToAuthCodeWhenDeviceFlowFails(t *testing.T) {
	store := erunTestCloudStore{config: configpkg.ERunConfig{CloudProviders: []model.CloudProviderConfig{erunTestProvider()}}}
	secrets := secretstore.NewFileCloudSecretStore(t.TempDir())
	provider := erunTestProvider()

	deviceStarted, authCodeCalled := false, false
	deps := cloud.CloudDependencies{
		CloudSecretStore: secrets,
		FetchOIDCDiscovery: func(core.Context, string) (cloud.OIDCDiscovery, error) {
			return cloud.OIDCDiscovery{Issuer: provider.OIDCIssuerURL, DeviceAuthorizationEndpoint: "https://auth.example.test/device", AuthorizationEndpoint: "https://auth.example.test/authorize", TokenEndpoint: "https://auth.example.test/token"}, nil
		},
		StartERunDeviceAuthorization: func(core.Context, cloud.OIDCDiscovery, string, string) (cloud.ERunDeviceAuthorization, error) {
			deviceStarted = true
			return cloud.ERunDeviceAuthorization{DeviceCode: "device-code-3", UserCode: "WXYZ-1234"}, nil
		},
		PollERunDeviceToken: func(core.Context, cloud.OIDCDiscovery, string, cloud.ERunDeviceAuthorization) (cloud.ERunTokens, error) {
			return cloud.ERunTokens{}, fmt.Errorf("device authorization expired before sign-in completed")
		},
		RunERunAuthCodeLogin: func(core.Context, cloud.OIDCDiscovery, string, string) (cloud.ERunTokens, error) {
			authCodeCalled = true
			return cloud.ERunTokens{AccessToken: "access-3", RefreshToken: "refresh-3", ExpiresIn: time.Hour}, nil
		},
	}

	status, err := LoginCloudProviderAlias(core.Context{}, &store, model.CloudLoginParams{Alias: provider.Alias}, deps)
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
	if cached, ok := providertoken.LoadCachedERunAccessToken(secrets, provider.Alias); !ok || cached != "access-3" {
		t.Fatalf("cached = %q (ok=%v), want access-3 from the fallback flow", cached, ok)
	}
}

func TestERunCloudProviderLoginHonoursExplicitAuthCodeFlow(t *testing.T) {
	store := erunTestCloudStore{config: configpkg.ERunConfig{CloudProviders: []model.CloudProviderConfig{erunTestProvider()}}}
	provider := erunTestProvider()

	deps := cloud.CloudDependencies{
		CloudSecretStore: secretstore.NewFileCloudSecretStore(t.TempDir()),
		FetchOIDCDiscovery: func(core.Context, string) (cloud.OIDCDiscovery, error) {
			return cloud.OIDCDiscovery{Issuer: provider.OIDCIssuerURL, DeviceAuthorizationEndpoint: "https://auth.example.test/device", AuthorizationEndpoint: "https://auth.example.test/authorize", TokenEndpoint: "https://auth.example.test/token"}, nil
		},
		StartERunDeviceAuthorization: func(core.Context, cloud.OIDCDiscovery, string, string) (cloud.ERunDeviceAuthorization, error) {
			t.Fatal("an explicit authcode flow must not start the device grant")
			return cloud.ERunDeviceAuthorization{}, nil
		},
		RunERunAuthCodeLogin: func(core.Context, cloud.OIDCDiscovery, string, string) (cloud.ERunTokens, error) {
			return cloud.ERunTokens{AccessToken: "access-4", RefreshToken: "refresh-4", ExpiresIn: time.Hour}, nil
		},
	}

	if _, err := LoginCloudProviderAlias(core.Context{}, &store, model.CloudLoginParams{Alias: provider.Alias, Flow: ERunLoginFlowAuthCode}, deps); err != nil {
		t.Fatalf("LoginCloudProviderAlias: %v", err)
	}
}

func TestERunCloudProviderLoginExplicitDeviceFlowWithoutEndpointErrors(t *testing.T) {
	store := erunTestCloudStore{config: configpkg.ERunConfig{CloudProviders: []model.CloudProviderConfig{erunTestProvider()}}}
	provider := erunTestProvider()

	deps := cloud.CloudDependencies{
		CloudSecretStore: secretstore.NewFileCloudSecretStore(t.TempDir()),
		FetchOIDCDiscovery: func(core.Context, string) (cloud.OIDCDiscovery, error) {
			return cloud.OIDCDiscovery{Issuer: provider.OIDCIssuerURL, AuthorizationEndpoint: "https://auth.example.test/authorize", TokenEndpoint: "https://auth.example.test/token"}, nil
		},
		RunERunAuthCodeLogin: func(core.Context, cloud.OIDCDiscovery, string, string) (cloud.ERunTokens, error) {
			t.Fatal("an explicit device flow must not silently use authorization code")
			return cloud.ERunTokens{}, nil
		},
	}

	_, err := LoginCloudProviderAlias(core.Context{}, &store, model.CloudLoginParams{Alias: provider.Alias, Flow: ERunLoginFlowDevice}, deps)
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

// Reserved scopes are often missing from discovery, so --scope must still reach the request.
func TestERunCloudProviderLoginRequestsExtraScopes(t *testing.T) {
	store := erunTestCloudStore{config: configpkg.ERunConfig{CloudProviders: []model.CloudProviderConfig{erunTestProvider()}}}
	provider := erunTestProvider()

	var gotScope string
	deps := cloud.CloudDependencies{
		CloudSecretStore: secretstore.NewFileCloudSecretStore(t.TempDir()),
		FetchOIDCDiscovery: func(core.Context, string) (cloud.OIDCDiscovery, error) {
			return cloud.OIDCDiscovery{Issuer: provider.OIDCIssuerURL, AuthorizationEndpoint: "https://auth.example.test/authorize", TokenEndpoint: "https://auth.example.test/token"}, nil
		},
		RunERunAuthCodeLogin: func(_ core.Context, _ cloud.OIDCDiscovery, _ string, scope string) (cloud.ERunTokens, error) {
			gotScope = scope
			return cloud.ERunTokens{AccessToken: "access-5", RefreshToken: "refresh-5", ExpiresIn: time.Hour}, nil
		},
	}

	params := model.CloudLoginParams{Alias: provider.Alias, Scopes: []string{"urn:zitadel:iam:user:resourceowner"}}
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
	if got := erunLoginScope(nil); got != clouddefaults.ErunOAuthScope {
		t.Fatalf("erunLoginScope(nil) = %q, want the baseline %q", got, clouddefaults.ErunOAuthScope)
	}
	got := erunLoginScope([]string{"openid", "a b", "", "a"})
	if got != "openid offline_access a b" {
		t.Fatalf("erunLoginScope = %q, want deduped and split", got)
	}
}
