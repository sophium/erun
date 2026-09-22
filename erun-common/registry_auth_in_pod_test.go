package eruncommon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// inPodTestContext is a Context that can trace, so the resolver's trace lines
// are exercised rather than panicking on a nil logger.
func inPodTestContext(dryRun bool) Context {
	var trace bytes.Buffer
	return Context{Logger: NewLoggerWithWriters(0, &trace, &trace), DryRun: dryRun}
}

// stubDeclaredImagePullSecrets replaces the cluster read with a static auths
// document, so these tests exercise the resolution without a live cluster.
func stubDeclaredImagePullSecrets(t *testing.T, auths map[string]dockerConfigJSONAuthEntry) {
	t.Helper()
	prev := readDeclaredImagePullSecretAuths
	readDeclaredImagePullSecretAuths = func(string, string, string) (map[string]dockerConfigJSONAuthEntry, error) {
		return auths, nil
	}
	t.Cleanup(func() {
		readDeclaredImagePullSecretAuths = prev
		inPodDeclaredRegistryAuth = nil
	})
}

// useInPodIdentity marks this process as the target env's own runtime pod and
// clears every other credential route, reproducing the pod's boot state: no
// docker config, no gh session, no GH_TOKEN/GITHUB_TOKEN.
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

func ghcrDeclaredAuths(userPass string) map[string]dockerConfigJSONAuthEntry {
	return map[string]dockerConfigJSONAuthEntry{
		"ghcr.io": {Auth: b64Auth(userPass)},
	}
}

func frsProdRuntimeTarget() OpenResult {
	return OpenResult{
		Tenant:      "frs",
		Environment: "prod",
		EnvConfig:   EnvConfig{ImagePullSecrets: []string{"ghcr-pull"}},
	}
}

// The env declares the credential as its own imagePullSecret; the pod has none
// of the three routes resolveGHCRBasicAuth otherwise knows.
func TestConfigureInPodDeclaredRegistryAuthResolvesForGHCR(t *testing.T) {
	useInPodIdentity(t)
	stubDeclaredImagePullSecrets(t, ghcrDeclaredAuths("_json_key:ghcrtok"))

	ctx := inPodTestContext(false)
	configureInPodDeclaredRegistryAuth(ctx, frsProdRuntimeTarget())

	auth, ok := resolveGHCRBasicAuth("sophium")
	if !ok || auth.username != "_json_key" || auth.secret != "ghcrtok" {
		t.Fatalf("got %+v ok=%v, want the declared pull-secret credential", auth, ok)
	}
}

// Every pre-existing route keeps its precedence: the declared pull secret is
// consulted only once all three have come up empty.
func TestResolveGHCRBasicAuthPrefersExistingRoutesOverDeclaredPullSecret(t *testing.T) {
	useInPodIdentity(t)
	stubDeclaredImagePullSecrets(t, ghcrDeclaredAuths("pullsecret:pw"))
	t.Setenv("GH_TOKEN", "envtok")

	configureInPodDeclaredRegistryAuth(inPodTestContext(false), frsProdRuntimeTarget())

	auth, ok := resolveGHCRBasicAuth("sophium")
	if !ok || auth.secret != "envtok" || auth.username != "sophium" {
		t.Fatalf("got %+v ok=%v, want GH_TOKEN to outrank the declared pull secret", auth, ok)
	}

	dir := writeDockerConfig(t, fmt.Sprintf(`{"auths":{"ghcr.io":{"auth":%q}}}`, b64Auth("dockeruser:dockerpw")))
	useDockerConfigDir(t, dir)
	configureInPodDeclaredRegistryAuth(inPodTestContext(false), frsProdRuntimeTarget())

	auth, ok = resolveGHCRBasicAuth("sophium")
	if !ok || auth.username != "dockeruser" {
		t.Fatalf("got %+v ok=%v, want the docker config to outrank the declared pull secret", auth, ok)
	}
}

// Outside the target env's own pod the declared pull secrets are not this
// process's credential, so nothing is read and nothing is resolved.
func TestConfigureInPodDeclaredRegistryAuthRequiresRuntimePodIdentity(t *testing.T) {
	useInPodIdentity(t)
	t.Setenv("ERUN_TENANT", "")
	calls := 0
	prev := readDeclaredImagePullSecretAuths
	readDeclaredImagePullSecretAuths = func(string, string, string) (map[string]dockerConfigJSONAuthEntry, error) {
		calls++
		return ghcrDeclaredAuths("pullsecret:pw"), nil
	}
	t.Cleanup(func() {
		readDeclaredImagePullSecretAuths = prev
		inPodDeclaredRegistryAuth = nil
	})

	configureInPodDeclaredRegistryAuth(inPodTestContext(false), frsProdRuntimeTarget())

	if calls != 0 {
		t.Fatalf("read declared pull secrets %d time(s) outside a runtime pod, want 0", calls)
	}
	if _, ok := resolveGHCRBasicAuth("sophium"); ok {
		t.Fatal("a host process must not resolve a credential from an env's declared pull secrets")
	}
}

// A pod of one env must not authenticate as another env.
func TestConfigureInPodDeclaredRegistryAuthIgnoresAnotherEnvironmentsTarget(t *testing.T) {
	useInPodIdentity(t)
	stubDeclaredImagePullSecrets(t, ghcrDeclaredAuths("pullsecret:pw"))

	target := frsProdRuntimeTarget()
	target.Environment = "staging"
	configureInPodDeclaredRegistryAuth(inPodTestContext(false), target)

	if _, ok := resolveGHCRBasicAuth("sophium"); ok {
		t.Fatal("the pod's own declared credential must not cover a different environment's deploy")
	}
}

// A dry run performs no cluster read and states no credential.
func TestConfigureInPodDeclaredRegistryAuthSkipsDryRun(t *testing.T) {
	useInPodIdentity(t)
	calls := 0
	prev := readDeclaredImagePullSecretAuths
	readDeclaredImagePullSecretAuths = func(string, string, string) (map[string]dockerConfigJSONAuthEntry, error) {
		calls++
		return ghcrDeclaredAuths("pullsecret:pw"), nil
	}
	t.Cleanup(func() {
		readDeclaredImagePullSecretAuths = prev
		inPodDeclaredRegistryAuth = nil
	})

	configureInPodDeclaredRegistryAuth(inPodTestContext(true), frsProdRuntimeTarget())

	if calls != 0 {
		t.Fatalf("read declared pull secrets %d time(s) on a dry run, want 0", calls)
	}
}

// ghcrPrivateRegistryServer answers the token exchange and tag list the way a
// private ghcr namespace does: anonymous token requests are refused, a Basic
// credential mints a pull token for the declared tag.
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

func resolveGHCRChartWithDeclaredCredential(t *testing.T, version string) (RuntimeRegistryVersions, error) {
	t.Helper()
	server := ghcrPrivateRegistryServer(t, version)
	return ResolveConfiguredRuntimeRegistryVersions(context.Background(), RuntimeRegistryConfig{
		Namespace:  "ghcr.io/sophium",
		Repository: "charts/frs-devops",
		BaseURL:    server.URL,
		TokenURL:   server.URL,
	})
}

// End to end through the real registry read: with the env's declared credential
// resolved, the private chart is confirmed instead of refused.
func TestDeclaredImagePullSecretConfirmsPrivateChart(t *testing.T) {
	useInPodIdentity(t)
	stubDeclaredImagePullSecrets(t, ghcrDeclaredAuths("pullsecret:pw"))
	configureInPodDeclaredRegistryAuth(inPodTestContext(false), frsProdRuntimeTarget())

	versions, err := resolveGHCRChartWithDeclaredCredential(t, "1.0.104")
	if err != nil {
		t.Fatalf("private chart read with the declared credential: %v", err)
	}
	if !versions.HasVersion("1.0.104") {
		t.Fatalf("versions = %+v, want 1.0.104 confirmed", versions)
	}
}

// The refusal direction is unchanged: a genuinely credential-less run gets the
// same anonymous 401, so the read stays indeterminate and deploy still refuses
// with the message it has today rather than guessing.
func TestCredentiallessRunStillRefusesWithTheSameMessage(t *testing.T) {
	useInPodIdentity(t)
	inPodDeclaredRegistryAuth = nil

	_, err := resolveGHCRChartWithDeclaredCredential(t, "1.0.104")
	if err == nil {
		t.Fatal("an anonymous read of a private chart must not resolve")
	}
	wantProbe := "ghcr token request failed: 401 Unauthorized"
	if !strings.Contains(err.Error(), wantProbe) {
		t.Fatalf("probe error = %q, want it to carry %q", err, wantProbe)
	}

	refusal := (&RuntimeChartConfirmationError{
		Version:      "1.0.104",
		Candidates:   []string{"ghcr.io/sophium/charts/frs-devops (the tenant's own umbrella): could not determine: " + err.Error()},
		Inconclusive: true,
	}).Error()
	want := "deploy could not confirm a runtime chart at version 1.0.104 at any coordinate probed — " +
		"ghcr.io/sophium/charts/frs-devops (the tenant's own umbrella): could not determine: " + err.Error() +
		". At least one registry read did not return a definitive answer, " +
		"so deploy refuses to guess rather than install a coordinate it has not confirmed; " +
		"check registry credentials/connectivity and retry."
	if refusal != want {
		t.Fatalf("refusal text changed:\n got: %s\nwant: %s", refusal, want)
	}
}

// A Secret the pod cannot read (RBAC denial, unreachable API server) must leave
// the run credential-less rather than failing the deploy on the read.
func TestConfigureInPodDeclaredRegistryAuthToleratesUnreadableSecret(t *testing.T) {
	useInPodIdentity(t)
	prev := readDeclaredImagePullSecretAuths
	readDeclaredImagePullSecretAuths = func(string, string, string) (map[string]dockerConfigJSONAuthEntry, error) {
		return nil, fmt.Errorf("read existing image pull secret ghcr-pull: forbidden")
	}
	t.Cleanup(func() {
		readDeclaredImagePullSecretAuths = prev
		inPodDeclaredRegistryAuth = nil
	})

	configureInPodDeclaredRegistryAuth(inPodTestContext(false), frsProdRuntimeTarget())

	if len(inPodDeclaredRegistryAuth) != 0 {
		t.Fatalf("inPodDeclaredRegistryAuth = %+v, want empty after an unreadable secret", inPodDeclaredRegistryAuth)
	}
}
