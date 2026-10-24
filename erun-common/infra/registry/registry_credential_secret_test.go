package registry

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/kube"
)

func TestRegistryCredentialSecretName(t *testing.T) {
	if got, want := registryCredentialSecretName("team"), "team-devops-registry-credential"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestGhcrCredentialOwner(t *testing.T) {
	cases := []struct {
		registry string
		want     string
	}{
		{"ghcr.io/sophium", "sophium"},
		{"ghcr.io", ""},
		{"", ""},
		{"ghcr.io/sophium/nested", "sophium/nested"},
	}
	for _, tc := range cases {
		if got := ghcrCredentialOwner(tc.registry); got != tc.want {
			t.Errorf("ghcrCredentialOwner(%q) = %q, want %q", tc.registry, got, tc.want)
		}
	}
}

func TestResolveHostGHCRCredentialsResolvesEachHostOnce(t *testing.T) {
	dir := writeDockerConfig(t, fmt.Sprintf(`{"auths":{"ghcr.io":{"auth":%q}}}`, b64Auth("alice:s3cret")))
	useDockerConfigDir(t, dir)
	useCredentialHelper(t, func(string, string) ([]byte, error) {
		t.Fatal("credential helper must not run when no store is configured")
		return nil, nil
	})
	useGHToken(t, func(string) (string, bool) { return "", false })

	credentials := resolveHostGHCRCredentials([]string{"ghcr.io/sophium", "ghcr.io/sophium", "ghcr.io/other"})
	if len(credentials) != 1 {
		t.Fatalf("got %d credentials, want 1 (deduped by host): %+v", len(credentials), credentials)
	}
	auth, ok := credentials["ghcr.io"]
	if !ok || auth.Username != "alice" || auth.Secret != "s3cret" {
		t.Fatalf("got %+v ok=%v, want alice/s3cret", auth, ok)
	}
}

func TestResolveHostGHCRCredentialsNoneResolved(t *testing.T) {
	useDockerConfigDir(t, t.TempDir())
	useGHToken(t, func(string) (string, bool) { return "", false })
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")

	credentials := resolveHostGHCRCredentials([]string{"ghcr.io/sophium"})
	if len(credentials) != 0 {
		t.Fatalf("got %+v, want no credentials", credentials)
	}
}

func TestDockerConfigJSONForCredentials(t *testing.T) {
	rendered, err := dockerConfigJSONForCredentials(map[string]RegistryBasicAuth{
		"ghcr.io": {Username: "alice", Secret: "s3cret"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var decoded config.DockerConfigJSONFile
	if err := json.Unmarshal([]byte(rendered), &decoded); err != nil {
		t.Fatalf("rendered payload is not valid JSON: %v (%s)", err, rendered)
	}
	entry, ok := decoded.Auths["ghcr.io"]
	if !ok {
		t.Fatalf("rendered payload missing ghcr.io entry: %s", rendered)
	}
	if want := b64Auth("alice:s3cret"); entry.Auth != want {
		t.Fatalf("auth = %q, want %q", entry.Auth, want)
	}
}

func TestRenderRegistryCredentialSecretIsValidYAMLAndRedacted(t *testing.T) {
	manifest := renderRegistryCredentialSecret("team-devops-registry-credential", "team-dev", `{"auths":{"ghcr.io":{"auth":"c2VjcmV0"}}}`)
	for _, want := range []string{
		"kind: Secret",
		"name: team-devops-registry-credential",
		"namespace: team-dev",
		"type: kubernetes.io/dockerconfigjson",
		`".dockerconfigjson"`,
	} {
		if !strings.Contains(manifest, want) {
			t.Errorf("manifest missing %q:\n%s", want, manifest)
		}
	}
}

func TestKubectlApplyStdinArgs(t *testing.T) {
	got := kube.KubectlApplyStdinArgs("team-dev", "my-context")
	want := []string{"--context", "my-context", "-n", "team-dev", "apply", "-f", "-"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("got %v, want %v", got, want)
	}
	if got := kube.KubectlApplyStdinArgs("team-dev", ""); strings.Contains(strings.Join(got, " "), "--context") {
		t.Fatalf("no --context should render without a kubernetes context: %v", got)
	}
}

func testTraceContext(dryRun bool) core.Context {
	return core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, io.Discard, io.Discard), DryRun: dryRun}
}

func TestProvisionRegistryCredentialSecretNoRegistries(t *testing.T) {
	name, err := ProvisionRegistryCredentialSecret(testTraceContext(false), "team", "team-dev", "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "" {
		t.Fatalf("got %q, want empty when no registries require a credential", name)
	}
}

func TestProvisionRegistryCredentialSecretNoHostCredential(t *testing.T) {
	useDockerConfigDir(t, t.TempDir())
	useGHToken(t, func(string) (string, bool) { return "", false })
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("ERUN_KUBECTL_BIN", failingBinaryPath(t))

	name, err := ProvisionRegistryCredentialSecret(testTraceContext(false), "team", "team-dev", "", []string{"ghcr.io/sophium"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "" {
		t.Fatalf("got %q, want empty when the host has no credential to provision", name)
	}
}

// failingBinaryPath writes a script that fails the test if executed.
func failingBinaryPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := dir + "/kubectl-must-not-run"
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho 'kubectl must not run in this test' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	return path
}
