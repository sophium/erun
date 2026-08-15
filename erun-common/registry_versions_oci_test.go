package eruncommon

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A registry that is neither ghcr nor Docker Hub used to fall through to the
// Docker Hub resolver, which looked up a repository named after the registry
// host and reported the tenant's own runtime image as unreachable.
func TestResolveConfiguredRuntimeRegistryVersionsListsPrivateRegistry(t *testing.T) {
	var gotPath, gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"petios-devops","tags":["1.0.76","1.0.178","1.0.179-snapshot-20260101010101"]}`))
	}))
	defer server.Close()

	restore := dockerConfigDir
	dockerConfigDir = func() string { return t.TempDir() }
	defer func() { dockerConfigDir = restore }()

	versions, err := ResolveConfiguredRuntimeRegistryVersions(context.Background(), RuntimeRegistryConfig{
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
		t.Fatalf("sent %q with no credential available", gotAuth)
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

// An ECR host with no docker credential falls back to the AWS CLI, mirroring the
// gh fallback for ghcr, because an ECR token expires after twelve hours and the
// docker credential is routinely absent or stale.
func TestResolveOCIRegistryBasicAuthFallsBackToECRLogin(t *testing.T) {
	restoreDir := dockerConfigDir
	dockerConfigDir = func() string { return t.TempDir() }
	defer func() { dockerConfigDir = restoreDir }()

	restoreECR := runECRLoginPassword
	var gotRegion string
	runECRLoginPassword = func(region string) (string, bool) {
		gotRegion = region
		return "minted-token", true
	}
	defer func() { runECRLoginPassword = restoreECR }()

	auth, ok := resolveOCIRegistryBasicAuth("020362606330.dkr.ecr.eu-west-2.amazonaws.com")
	if !ok {
		t.Fatal("expected an ECR credential from the AWS CLI fallback")
	}
	if gotRegion != "eu-west-2" {
		t.Fatalf("minted for region %q, want eu-west-2", gotRegion)
	}
	if auth.username != "AWS" || auth.secret != "minted-token" {
		t.Fatalf("unexpected credential %+v", auth)
	}
	if _, ok := resolveOCIRegistryBasicAuth("registry.example"); ok {
		t.Fatal("a non-ECR host must not use the ECR fallback")
	}
}

// A namespace that names its own registry host must resolve against that host.
// Defaulting it to Docker Hub sent the tags request to hub.docker.com and 404ed,
// which is what made a private image look unreachable.
func TestResolvedBaseURLFollowsTheRegistryHost(t *testing.T) {
	for _, tc := range []struct {
		namespace string
		want      string
	}{
		{"020362606330.dkr.ecr.eu-west-2.amazonaws.com", "https://020362606330.dkr.ecr.eu-west-2.amazonaws.com"},
		{"registry.example/team", "https://registry.example"},
		{"localhost:5000", "https://localhost:5000"},
		{"ghcr.io/sophium", "https://ghcr.io"},
		{"sophium", "https://hub.docker.com"},
	} {
		got := RuntimeRegistryConfig{Namespace: tc.namespace, Repository: "petios-devops"}.Resolved().BaseURL
		if got != tc.want {
			t.Errorf("namespace %q resolved base %q, want %q", tc.namespace, got, tc.want)
		}
	}
}
