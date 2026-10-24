package registry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
)

// Other registries must be queried directly; the Docker Hub resolver would
// treat the registry host as a repository name.
func TestResolveConfiguredRuntimeRegistryVersionsListsPrivateRegistry(t *testing.T) {
	var gotPath, gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"petios-devops","tags":["1.0.76","1.0.178","1.0.179-snapshot-20260101010101"]}`))
	}))
	defer server.Close()

	restore := DockerConfigDir
	DockerConfigDir = func() string { return t.TempDir() }
	defer func() { DockerConfigDir = restore }()
	restoreECR := RunECRLoginPassword
	RunECRLoginPassword = func(string) (string, bool) { return "", false }
	t.Cleanup(func() { RunECRLoginPassword = restoreECR })

	versions, err := ResolveConfiguredRuntimeRegistryVersions(context.Background(), config.RuntimeRegistryConfig{
		Namespace:  "020362606330.dkr.ecr.eu-west-2.amazonaws.com",
		Repository: "petios-devops",
		BaseURL:    server.URL,
	})
	if err != nil {
		t.Fatalf("resolve versions: %v", err)
	}
	if gotPath != "/v2/petios-devops/tags/list" {
		t.Fatalf("listed %q, want the registry's own v2 tags endpoint", gotPath)
	}
	if gotAuth != "" {
		t.Fatal("sent authorization with no credential available")
	}
	if versions.LatestStable != "1.0.178" {
		t.Fatalf("latest stable %q, want 1.0.178", versions.LatestStable)
	}
	if versions.LatestSnapshot != "1.0.179-snapshot-20260101010101" {
		t.Fatalf("latest snapshot %q", versions.LatestSnapshot)
	}
	if versions.Image != "020362606330.dkr.ecr.eu-west-2.amazonaws.com/petios-devops" {
		t.Fatalf("image %q", versions.Image)
	}
}

// ECR tokens expire after 12 hours, so the AWS CLI mints a fresh one.
func TestResolveOCIRegistryBasicAuthFallsBackToECRLogin(t *testing.T) {
	restoreDir := DockerConfigDir
	DockerConfigDir = func() string { return t.TempDir() }
	defer func() { DockerConfigDir = restoreDir }()

	restoreECR := RunECRLoginPassword
	var gotRegion string
	RunECRLoginPassword = func(region string) (string, bool) {
		gotRegion = region
		return "minted-token", true
	}
	defer func() { RunECRLoginPassword = restoreECR }()

	auth, ok := ResolveOCIRegistryBasicAuth("020362606330.dkr.ecr.eu-west-2.amazonaws.com")
	if !ok {
		t.Fatal("expected an ECR credential from the AWS CLI fallback")
	}
	if gotRegion != "eu-west-2" {
		t.Fatalf("minted for region %q, want eu-west-2", gotRegion)
	}
	if auth.Username != "AWS" || auth.Secret != "minted-token" {
		t.Fatalf("unexpected credential %+v", auth)
	}
	if _, ok := ResolveOCIRegistryBasicAuth("registry.example"); ok {
		t.Fatal("a non-ECR host must not use the ECR fallback")
	}
}

// A namespace naming its own host resolves against it. Insecure registries are
// probed over HTTP; Insecure never affects ghcr.io or Docker Hub.
func TestResolvedBaseURLFollowsTheRegistryHost(t *testing.T) {
	for _, tc := range []struct {
		namespace string
		insecure  bool
		want      string
	}{
		{"020362606330.dkr.ecr.eu-west-2.amazonaws.com", false, "https://020362606330.dkr.ecr.eu-west-2.amazonaws.com"},
		{"registry.example/team", false, "https://registry.example"},
		{"localhost:5000", false, "https://localhost:5000"},
		{"ghcr.io/sophium", false, "https://ghcr.io"},
		{"sophium", false, "https://hub.docker.com"},
		{"10.43.0.100:5000", true, "http://10.43.0.100:5000"},
		{"10.43.0.100:5000/charts", true, "http://10.43.0.100:5000"},
		{"ghcr.io/sophium", true, "https://ghcr.io"},
	} {
		got := config.RuntimeRegistryConfig{Namespace: tc.namespace, Insecure: tc.insecure, Repository: "petios-devops"}.Resolved().BaseURL
		if got != tc.want {
			t.Errorf("namespace %q insecure=%v resolved base %q, want %q", tc.namespace, tc.insecure, got, tc.want)
		}
	}
}

// httptest.NewServer speaks only HTTP, so this proves the probe reaches an insecure registry.
func TestResolveConfiguredRuntimeRegistryVersionsHonoursInsecure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"charts/team-backend-api","tags":["1.0.0"]}`))
	}))
	defer server.Close()

	restore := DockerConfigDir
	DockerConfigDir = func() string { return t.TempDir() }
	defer func() { DockerConfigDir = restore }()

	host := strings.TrimPrefix(server.URL, "http://")
	versions, err := ResolveConfiguredRuntimeRegistryVersions(context.Background(), config.RuntimeRegistryConfig{
		Namespace:  host,
		Repository: "charts/team-backend-api",
		Insecure:   true,
	})
	if err != nil {
		t.Fatalf("resolve versions over the registry's plain-HTTP listener: %v", err)
	}
	if !versions.HasVersion("1.0.0") {
		t.Fatalf("versions %+v missing 1.0.0", versions)
	}
}
