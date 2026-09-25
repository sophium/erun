package eruncommon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// machineIdentityHostStore is a host holding one signed-in erun alias pointed
// at whatever platform the test stands up, so provisioning is exercised
// through the same resolution production uses.
func machineIdentityHostStore(apiURL string) staticCloudStore {
	provider := testPlatformAliasProvider()
	provider.ERun.APIURL = apiURL
	return staticCloudStore{config: ERunConfig{CloudProviders: []CloudProviderConfig{provider}}}
}

// fakePlatformAPI stands in for the two calls provisioning makes: resolving the
// environment by name, and minting its identity.
func fakePlatformAPI(t *testing.T, identity PlatformMachineIdentity) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			http.Error(w, "missing bearer", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/environments":
			_ = json.NewEncoder(w).Encode([]PlatformEnvironment{{EnvironmentID: identity.EnvironmentID, Name: identity.EnvironmentName}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/environments/"+identity.EnvironmentID+"/machine-identity":
			_ = json.NewEncoder(w).Encode(identity)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

// machineIdentityDeps is the cloud dependency set provisioning needs, with the
// network-facing halves stubbed: login (so the platform client can authenticate
// as the host's alias) and OIDC discovery.
func machineIdentityDeps(t *testing.T, tokenEndpoint string, exchange func(clientID, clientSecret string) (ERunTokens, error)) (CloudDependencies, CloudSecretStore) {
	t.Helper()
	store := NewFileCloudSecretStore(filepath.Join(t.TempDir(), cloudSecretStoreDirName))
	deps := CloudDependencies{
		CloudSecretStore: store,
		FetchOIDCDiscovery: func(Context, string) (OIDCDiscovery, error) {
			return OIDCDiscovery{Issuer: "https://auth.example", TokenEndpoint: tokenEndpoint}, nil
		},
		RefreshERunTokens: func(Context, OIDCDiscovery, string, string) (ERunTokens, error) {
			return ERunTokens{AccessToken: "caller-token"}, nil
		},
		ClientCredentialsERunTokens: func(_ Context, _ OIDCDiscovery, clientID, clientSecret string) (ERunTokens, error) {
			return exchange(clientID, clientSecret)
		},
	}
	if err := store.SaveCloudSecret(erunRefreshTokenRef(testPlatformAliasProvider().Alias), "host-session"); err != nil {
		t.Fatalf("seed host session: %v", err)
	}
	return deps, store
}

func testMachineIdentity() PlatformMachineIdentity {
	return PlatformMachineIdentity{
		EnvironmentID:   "0192aaaa-bbbb-cccc-dddd-eeeeffff0000",
		EnvironmentName: "team-dev",
		Issuer:          "https://auth.example",
		Subject:         "client-env-0192",
		ClientID:        "client-env-0192",
		ClientSecret:    "secret-env-0192",
		UserID:          "user-env-0192",
	}
}

// TestProvisionMachineIdentitySecretWritesTheEnvironmentsOwnIdentity is the
// delivery half end to end: the credential the platform minted reaches the
// Secret the runtime chart already mounts, as the alias entry's client-secret
// form rather than a delegated session.
func TestProvisionMachineIdentitySecretWritesTheEnvironmentsOwnIdentity(t *testing.T) {
	captured := installCapturingKubectl(t)
	identity := testMachineIdentity()
	api := fakePlatformAPI(t, identity)
	var sawClientID, sawClientSecret string
	deps, _ := machineIdentityDeps(t, "https://auth.example/token", func(clientID, clientSecret string) (ERunTokens, error) {
		sawClientID, sawClientSecret = clientID, clientSecret
		return ERunTokens{AccessToken: "machine-token"}, nil
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
	// The credential is proven usable before anything is written, with the
	// values the platform actually returned.
	if sawClientID != identity.ClientID || sawClientSecret != identity.ClientSecret {
		t.Fatalf("verification exchanged %q/%q, want the minted credential", sawClientID, sawClientSecret)
	}

	assertMachineIdentityManifest(t, captured.read(t), identity, api.URL)
}

// assertMachineIdentityManifest checks the Secret the pod will actually mount:
// the machine form of the alias entry, the credential it points at, and no
// trace of the delegated session the host itself holds.
func assertMachineIdentityManifest(t *testing.T, manifest string, identity PlatformMachineIdentity, apiURL string) {
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
	// The delegated session's own credential must not travel with it: an
	// environment that records both is an environment whose identity is
	// ambiguous.
	if strings.Contains(manifest, "refresh-tokenref:") || strings.Contains(manifest, "host-session") {
		t.Fatalf("machine identity secret carried the delegated session:\n%s", manifest)
	}
	// The token file's name is derived from the client-secret reference, not
	// the refresh-token one, so the pod reads the file the entry points at.
	sum := sha256.Sum256([]byte(machineIdentityClientSecretRef(testPlatformAliasProvider().Alias)))
	if want := hex.EncodeToString(sum[:]) + ".token"; !strings.Contains(manifest, want) {
		t.Fatalf("manifest does not name the client secret's own store file %q:\n%s", want, manifest)
	}
}

// TestProvisionMachineIdentitySecretRefusesACredentialItCannotUse is the check
// that keeps a working environment from being replaced by a broken one: a
// credential the platform returned but that mints no token is not written, and
// the caller is told to fall back.
func TestProvisionMachineIdentitySecretRefusesACredentialItCannotUse(t *testing.T) {
	captured := installCapturingKubectl(t)
	api := fakePlatformAPI(t, testMachineIdentity())
	deps, _ := machineIdentityDeps(t, "https://auth.example/token", func(string, string) (ERunTokens, error) {
		return ERunTokens{}, context.DeadlineExceeded
	})

	_, ok, err := provisionMachineIdentitySecret(testTraceContext(false), machineIdentityHostStore(api.URL), "team", "team-dev", "team-dev", "", deps)
	if err != nil {
		t.Fatalf("a credential that cannot be used is a fallback, not an error: %v", err)
	}
	if ok {
		t.Fatal("expected provisioning to decline a credential it could not exchange for a token")
	}
	assertNothingApplied(t, captured.path)
}

// TestProvisionMachineIdentitySecretFallsBackWhenTheEnvironmentIsUnregistered:
// an environment the platform does not know yet is the ordinary state during a
// first init, and it must leave the delegated path to run rather than fail.
func TestProvisionMachineIdentitySecretFallsBackWhenTheEnvironmentIsUnregistered(t *testing.T) {
	captured := installCapturingKubectl(t)
	api := fakePlatformAPI(t, testMachineIdentity())
	deps, _ := machineIdentityDeps(t, "https://auth.example/token", func(string, string) (ERunTokens, error) {
		return ERunTokens{AccessToken: "machine-token"}, nil
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

// TestProvisionMachineIdentitySecretIsANoOpWithoutAHostAlias: a host that is
// not signed in has nothing to ask with, and most installs are that host.
func TestProvisionMachineIdentitySecretIsANoOpWithoutAHostAlias(t *testing.T) {
	installCapturingKubectl(t)
	deps, _ := machineIdentityDeps(t, "https://auth.example/token", func(string, string) (ERunTokens, error) {
		return ERunTokens{AccessToken: "machine-token"}, nil
	})

	_, ok, err := provisionMachineIdentitySecret(testTraceContext(false), staticCloudStore{}, "team", "team-dev", "team-dev", "", deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected no provisioning from a host with no alias")
	}
}

// TestResolveERunAccessTokenMintsThroughTheClientCredentialsGrant pins which
// grant an alias's credential form selects -- the two are not interchangeable,
// and reading a client secret down the refresh-token path (or the reverse)
// would present the wrong kind of credential to the issuer.
func TestResolveERunAccessTokenMintsThroughTheClientCredentialsGrant(t *testing.T) {
	store := NewFileCloudSecretStore(filepath.Join(t.TempDir(), cloudSecretStoreDirName))
	alias := testPlatformAliasProvider().Alias
	ref := machineIdentityClientSecretRef(alias)
	if err := store.SaveCloudSecret(ref, "client-secret-value"); err != nil {
		t.Fatalf("save: %v", err)
	}
	provider := CloudProviderConfig{
		Alias:         alias,
		Provider:      CloudProviderERun,
		OIDCIssuerURL: "https://auth.example",
		ERun: &ERunProviderConfig{
			APIURL:          "https://api.example",
			ClientID:        "client-env-0192",
			ClientSecretRef: ref,
		},
	}
	refreshCalled, machineCalled := false, false
	deps := CloudDependencies{
		CloudSecretStore: store,
		FetchOIDCDiscovery: func(Context, string) (OIDCDiscovery, error) {
			return OIDCDiscovery{Issuer: "https://auth.example", TokenEndpoint: "https://auth.example/token"}, nil
		},
		RefreshERunTokens: func(Context, OIDCDiscovery, string, string) (ERunTokens, error) {
			refreshCalled = true
			return ERunTokens{}, nil
		},
		ClientCredentialsERunTokens: func(_ Context, _ OIDCDiscovery, clientID, clientSecret string) (ERunTokens, error) {
			machineCalled = true
			if clientID != "client-env-0192" || clientSecret != "client-secret-value" {
				t.Fatalf("grant presented %q/%q", clientID, clientSecret)
			}
			return ERunTokens{AccessToken: "machine-token"}, nil
		},
	}

	token, err := resolveERunAccessToken(testTraceContext(false), provider, deps)
	if err != nil {
		t.Fatalf("resolveERunAccessToken: %v", err)
	}
	if token != "machine-token" {
		t.Fatalf("got token %q", token)
	}
	if !machineCalled || refreshCalled {
		t.Fatalf("machineCalled=%v refreshCalled=%v, want the client_credentials grant only", machineCalled, refreshCalled)
	}
}

// assertNothingApplied reports whether the kubectl stub captured a manifest.
// It distinguishes "no file" from "an empty file" only in the message, because
// both mean provisioning declined to write anything.
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
