package published

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/registry"
)

// useInPodIdentity reproduces the pod's boot state: no docker config, gh session, or GH_TOKEN.
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

// ghcrPrivateRegistryServer refuses anonymous token requests and accepts Basic credentials, like private ghcr.
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
	return registry.ResolveConfiguredRuntimeRegistryVersions(context.Background(), config.RuntimeRegistryConfig{
		Namespace:  "ghcr.io/sophium",
		Repository: "charts/frs-devops",
		BaseURL:    server.URL,
		TokenURL:   server.URL,
	})
}

func TestCredentiallessRunStillRefusesWithTheSameMessage(t *testing.T) {
	useInPodIdentity(t)
	registry.InPodDeclaredRegistryAuth = nil

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
