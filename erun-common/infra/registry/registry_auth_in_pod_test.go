package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/config/containerregistry"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
)

// inPodTestContext can trace, avoiding a nil-logger panic.
func inPodTestContext(dryRun bool) core.Context {
	var trace bytes.Buffer
	return core.Context{Logger: core.NewLoggerWithWriters(0, &trace, &trace), DryRun: dryRun}
}

func stubDeclaredImagePullSecrets(t *testing.T, auths map[string]containerregistry.DockerConfigJSONAuthEntry) {
	t.Helper()
	prev := readDeclaredImagePullSecretAuths
	readDeclaredImagePullSecretAuths = func(string, string, string) (map[string]containerregistry.DockerConfigJSONAuthEntry, error) {
		return auths, nil
	}
	t.Cleanup(func() {
		readDeclaredImagePullSecretAuths = prev
		InPodDeclaredRegistryAuth = nil
	})
}

// useInPodIdentity reproduces a runtime pod's boot state: in-pod identity, no other credential route.
func useInPodIdentity(t *testing.T) {
	t.Helper()
	useDockerConfigDir(t, t.TempDir())
	useGHToken(t, func(string) (string, bool) { return "", false })
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("ERUN_TENANT", "frs")
	t.Setenv("ERUN_ENVIRONMENT", "prod")
	t.Setenv("ERUN_NAMESPACE", "")
}

func ghcrDeclaredAuths(userPass string) map[string]containerregistry.DockerConfigJSONAuthEntry {
	return map[string]containerregistry.DockerConfigJSONAuthEntry{
		"ghcr.io": {Auth: b64Auth(userPass)},
	}
}

func frsProdRuntimeTarget() config.OpenResult {
	return config.OpenResult{
		Tenant:      "frs",
		Environment: "prod",
		EnvConfig:   config.EnvConfig{ImagePullSecrets: []string{"ghcr-pull"}},
	}
}

func TestConfigureInPodDeclaredRegistryAuthResolvesForGHCR(t *testing.T) {
	useInPodIdentity(t)
	stubDeclaredImagePullSecrets(t, ghcrDeclaredAuths("_json_key:ghcrtok"))

	ctx := inPodTestContext(false)
	ConfigureInPodDeclaredRegistryAuth(ctx, frsProdRuntimeTarget())

	auth, ok := resolveGHCRBasicAuth("sophium")
	if !ok || auth.Username != "_json_key" || auth.Secret != "ghcrtok" {
		t.Fatalf("got %+v ok=%v, want the declared pull-secret credential", auth, ok)
	}
}

// The declared pull secret is consulted only after every other route is empty.
func TestResolveGHCRBasicAuthPrefersExistingRoutesOverDeclaredPullSecret(t *testing.T) {
	useInPodIdentity(t)
	stubDeclaredImagePullSecrets(t, ghcrDeclaredAuths("pullsecret:pw"))
	t.Setenv("GH_TOKEN", "envtok")

	ConfigureInPodDeclaredRegistryAuth(inPodTestContext(false), frsProdRuntimeTarget())

	auth, ok := resolveGHCRBasicAuth("sophium")
	if !ok || auth.Secret != "envtok" || auth.Username != "sophium" {
		t.Fatalf("got %+v ok=%v, want GH_TOKEN to outrank the declared pull secret", auth, ok)
	}

	dir := writeDockerConfig(t, fmt.Sprintf(`{"auths":{"ghcr.io":{"auth":%q}}}`, b64Auth("dockeruser:dockerpw")))
	useDockerConfigDir(t, dir)
	ConfigureInPodDeclaredRegistryAuth(inPodTestContext(false), frsProdRuntimeTarget())

	auth, ok = resolveGHCRBasicAuth("sophium")
	if !ok || auth.Username != "dockeruser" {
		t.Fatalf("got %+v ok=%v, want the docker config to outrank the declared pull secret", auth, ok)
	}
}

func TestConfigureInPodDeclaredRegistryAuthRequiresRuntimePodIdentity(t *testing.T) {
	useInPodIdentity(t)
	t.Setenv("ERUN_TENANT", "")
	calls := 0
	prev := readDeclaredImagePullSecretAuths
	readDeclaredImagePullSecretAuths = func(string, string, string) (map[string]containerregistry.DockerConfigJSONAuthEntry, error) {
		calls++
		return ghcrDeclaredAuths("pullsecret:pw"), nil
	}
	t.Cleanup(func() {
		readDeclaredImagePullSecretAuths = prev
		InPodDeclaredRegistryAuth = nil
	})

	ConfigureInPodDeclaredRegistryAuth(inPodTestContext(false), frsProdRuntimeTarget())

	if calls != 0 {
		t.Fatalf("read declared pull secrets %d time(s) outside a runtime pod, want 0", calls)
	}
	if _, ok := resolveGHCRBasicAuth("sophium"); ok {
		t.Fatal("a host process must not resolve a credential from an env's declared pull secrets")
	}
}

func TestConfigureInPodDeclaredRegistryAuthIgnoresAnotherEnvironmentsTarget(t *testing.T) {
	useInPodIdentity(t)
	stubDeclaredImagePullSecrets(t, ghcrDeclaredAuths("pullsecret:pw"))

	target := frsProdRuntimeTarget()
	target.Environment = "staging"
	ConfigureInPodDeclaredRegistryAuth(inPodTestContext(false), target)

	if _, ok := resolveGHCRBasicAuth("sophium"); ok {
		t.Fatal("the pod's own declared credential must not cover a different environment's deploy")
	}
}

func TestConfigureInPodDeclaredRegistryAuthSkipsDryRun(t *testing.T) {
	useInPodIdentity(t)
	calls := 0
	prev := readDeclaredImagePullSecretAuths
	readDeclaredImagePullSecretAuths = func(string, string, string) (map[string]containerregistry.DockerConfigJSONAuthEntry, error) {
		calls++
		return ghcrDeclaredAuths("pullsecret:pw"), nil
	}
	t.Cleanup(func() {
		readDeclaredImagePullSecretAuths = prev
		InPodDeclaredRegistryAuth = nil
	})

	ConfigureInPodDeclaredRegistryAuth(inPodTestContext(true), frsProdRuntimeTarget())

	if calls != 0 {
		t.Fatalf("read declared pull secrets %d time(s) on a dry run, want 0", calls)
	}
}

// ghcrPrivateRegistryServer refuses anonymous tokens, as a private ghcr namespace does.
func ghcrPrivateRegistryServer(t *testing.T, version string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/token"):
			user, pass, ok := r.BasicAuth()
			if !ok || user != "pullsecret" || pass != "pw" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"errors":[{"code":"UNAUTHORIZED","message":"authentication required"}]}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "pull-token"})
		case strings.HasPrefix(r.URL.Path, "/v2/") && strings.HasSuffix(r.URL.Path, "/tags/list"):
			if r.Header.Get("Authorization") != "Bearer pull-token" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"name": "sophium/charts/frs-devops", "tags": []string{version}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func resolveGHCRChartWithDeclaredCredential(t *testing.T, version string) (model.RuntimeRegistryVersions, error) {
	t.Helper()
	server := ghcrPrivateRegistryServer(t, version)
	return ResolveConfiguredRuntimeRegistryVersions(context.Background(), config.RuntimeRegistryConfig{
		Namespace:  "ghcr.io/sophium",
		Repository: "charts/frs-devops",
		BaseURL:    server.URL,
		TokenURL:   server.URL,
	})
}

func TestDeclaredImagePullSecretConfirmsPrivateChart(t *testing.T) {
	useInPodIdentity(t)
	stubDeclaredImagePullSecrets(t, ghcrDeclaredAuths("pullsecret:pw"))
	ConfigureInPodDeclaredRegistryAuth(inPodTestContext(false), frsProdRuntimeTarget())

	versions, err := resolveGHCRChartWithDeclaredCredential(t, "1.0.104")
	if err != nil {
		t.Fatalf("private chart read with the declared credential: %v", err)
	}
	if !versions.HasVersion("1.0.104") {
		t.Fatalf("versions = %+v, want 1.0.104 confirmed", versions)
	}
}

func TestConfigureInPodDeclaredRegistryAuthToleratesUnreadableSecret(t *testing.T) {
	useInPodIdentity(t)
	prev := readDeclaredImagePullSecretAuths
	readDeclaredImagePullSecretAuths = func(string, string, string) (map[string]containerregistry.DockerConfigJSONAuthEntry, error) {
		return nil, fmt.Errorf("read existing image pull secret ghcr-pull: forbidden")
	}
	t.Cleanup(func() {
		readDeclaredImagePullSecretAuths = prev
		InPodDeclaredRegistryAuth = nil
	})

	ConfigureInPodDeclaredRegistryAuth(inPodTestContext(false), frsProdRuntimeTarget())

	if len(InPodDeclaredRegistryAuth) != 0 {
		t.Fatalf("inPodDeclaredRegistryAuth = %+v, want empty after an unreadable secret", InPodDeclaredRegistryAuth)
	}
}
