package setup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/platformclient"
)

func machineIdentityHostStore(apiURL string) staticCloudStore {
	provider := testPlatformAliasProvider()
	provider.ERun.APIURL = apiURL
	return staticCloudStore{config: config.ERunConfig{CloudProviders: []model.CloudProviderConfig{provider}}}
}

func fakePlatformAPI(t *testing.T, identity platformclient.PlatformMachineIdentity) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			http.Error(w, "missing bearer", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/environments":
			_ = json.NewEncoder(w).Encode([]model.PlatformEnvironment{{EnvironmentID: identity.EnvironmentID, Name: identity.EnvironmentName}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/environments/"+identity.EnvironmentID+"/machine-identity":
			_ = json.NewEncoder(w).Encode(identity)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

// machineIdentityDeps stubs login and OIDC discovery.
func machineIdentityDeps(t *testing.T, tokenEndpoint string, exchange func(clientID, clientSecret string) (cloud.ERunTokens, error)) (cloud.CloudDependencies, seam.CloudSecretStore) {
	t.Helper()
	store := cloud.NewFileCloudSecretStore(filepath.Join(t.TempDir(), cloud.CloudSecretStoreDirName))
	deps := cloud.CloudDependencies{
		CloudSecretStore: store,
		FetchOIDCDiscovery: func(core.Context, string) (cloud.OIDCDiscovery, error) {
			return cloud.OIDCDiscovery{Issuer: "https://auth.example", TokenEndpoint: tokenEndpoint}, nil
		},
		RefreshERunTokens: func(core.Context, cloud.OIDCDiscovery, string, string) (cloud.ERunTokens, error) {
			return cloud.ERunTokens{AccessToken: "caller-token"}, nil
		},
		ClientCredentialsERunTokens: func(_ core.Context, _ cloud.OIDCDiscovery, clientID, clientSecret string) (cloud.ERunTokens, error) {
			return exchange(clientID, clientSecret)
		},
	}
	if err := store.SaveCloudSecret(cloud.ErunRefreshTokenRef(testPlatformAliasProvider().Alias), "host-session"); err != nil {
		t.Fatalf("seed host session: %v", err)
	}
	return deps, store
}

func testMachineIdentity() platformclient.PlatformMachineIdentity {
	return platformclient.PlatformMachineIdentity{
		EnvironmentID:   "0192aaaa-bbbb-cccc-dddd-eeeeffff0000",
		EnvironmentName: "team-dev",
		Issuer:          "https://auth.example",
		Subject:         "client-env-0192",
		ClientID:        "client-env-0192",
		ClientSecret:    "secret-env-0192",
		UserID:          "user-env-0192",
	}
}

func TestProvisionMachineIdentitySecretWritesTheEnvironmentsOwnIdentity(t *testing.T) {
	captured := installCapturingKubectl(t)
	identity := testMachineIdentity()
	api := fakePlatformAPI(t, identity)
	var sawClientID, sawClientSecret string
	deps, _ := machineIdentityDeps(t, "https://auth.example/token", func(clientID, clientSecret string) (cloud.ERunTokens, error) {
		sawClientID, sawClientSecret = clientID, clientSecret
		return cloud.ERunTokens{AccessToken: "machine-token"}, nil
	})

	name, ok, err := provisionMachineIdentitySecret(testTraceContext(false), machineIdentityHostStore(api.URL), "team", "team-dev", "team-dev", "", deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected the machine identity to be provisioned")
	}
	if want := "team-devops-platform-alias"; name != want {
		t.Fatalf("got secret name %q, want %q", name, want)
	}
	if sawClientID != identity.ClientID || sawClientSecret != identity.ClientSecret {
		t.Fatalf("verification exchanged %q/%q, want the minted credential", sawClientID, sawClientSecret)
	}

	assertMachineIdentityManifest(t, captured.read(t), identity, api.URL)
}

func assertMachineIdentityManifest(t *testing.T, manifest string, identity platformclient.PlatformMachineIdentity, apiURL string) {
	t.Helper()
	for _, want := range []string{
		"kind: Secret",
		"name: team-devops-platform-alias",
		"namespace: team-dev",
		"clientid: " + identity.ClientID,
		"clientsecretref: " + machineIdentityClientSecretRef(testPlatformAliasProvider().Alias),
		"apiurl: " + apiURL,
		identity.ClientSecret,
	} {
		if !strings.Contains(manifest, want) {
			t.Errorf("manifest missing %q:\n%s", want, manifest)
		}
	}
	// Carrying both credentials would make the identity ambiguous.
	if strings.Contains(manifest, "refresh-tokenref:") || strings.Contains(manifest, "host-session") {
		t.Fatalf("machine identity secret carried the delegated session:\n%s", manifest)
	}
	sum := sha256.Sum256([]byte(machineIdentityClientSecretRef(testPlatformAliasProvider().Alias)))
	if want := hex.EncodeToString(sum[:]) + ".token"; !strings.Contains(manifest, want) {
		t.Fatalf("manifest does not name the client secret's own store file %q:\n%s", want, manifest)
	}
}

// An unregistered environment is normal on first init and must fall back.
func TestProvisionMachineIdentitySecretFallsBackWhenTheEnvironmentIsUnregistered(t *testing.T) {
	captured := installCapturingKubectl(t)
	api := fakePlatformAPI(t, testMachineIdentity())
	deps, _ := machineIdentityDeps(t, "https://auth.example/token", func(string, string) (cloud.ERunTokens, error) {
		return cloud.ERunTokens{AccessToken: "machine-token"}, nil
	})

	_, ok, err := provisionMachineIdentitySecret(testTraceContext(false), machineIdentityHostStore(api.URL), "team", "not-registered", "team-dev", "", deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected provisioning to decline an environment the platform does not have")
	}
	assertNothingApplied(t, captured.path)
}

func assertNothingApplied(t *testing.T, capturePath string) {
	t.Helper()
	data, err := os.ReadFile(capturePath)
	if err != nil {
		return
	}
	if len(data) > 0 {
		t.Fatalf("nothing should have been applied:\n%s", data)
	}
}

// `erun init` has no alias flag, so ambiguity must fall back, not fail.
func TestProvisionMachineIdentitySecretFallsBackOnAnAmbiguousHost(t *testing.T) {
	captured := installCapturingKubectl(t)
	store := ambiguousHostStore(t)

	_, ok, err := provisionMachineIdentitySecret(testTraceContext(false), store, "team", "team-dev", "team-dev", "", cloud.DefaultCloudDependencies())
	if err != nil {
		t.Fatalf("an ambiguous host is a fallback for machine identity, not an error: %v", err)
	}
	if ok {
		t.Fatal("expected no machine identity from a host with no unambiguous alias")
	}
	assertNothingApplied(t, captured.path)
}
