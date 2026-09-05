package eruncommon

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
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
	if !ok || auth.username != "alice" || auth.secret != "s3cret" {
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
	rendered, err := dockerConfigJSONForCredentials(map[string]registryBasicAuth{
		"ghcr.io": {username: "alice", secret: "s3cret"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var decoded dockerConfigJSONFile
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
	got := kubectlApplyStdinArgs("team-dev", "my-context")
	want := []string{"--context", "my-context", "-n", "team-dev", "apply", "-f", "-"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("got %v, want %v", got, want)
	}
	if got := kubectlApplyStdinArgs("team-dev", ""); strings.Contains(strings.Join(got, " "), "--context") {
		t.Fatalf("no --context should render without a kubernetes context: %v", got)
	}
}

func testTraceContext(dryRun bool) Context {
	return Context{Logger: NewLoggerWithWriters(VerbosityInfo, io.Discard, io.Discard), DryRun: dryRun}
}

func TestProvisionRegistryCredentialSecretNoRegistries(t *testing.T) {
	name, err := provisionRegistryCredentialSecret(testTraceContext(false), "team", "team-dev", "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "" {
		t.Fatalf("got %q, want empty when no registries require a credential", name)
	}
}

// TestProvisionRegistryCredentialSecretNoHostCredential proves the function
// never touches kubectl when the host has nothing to provision: it points
// ERUN_KUBECTL_BIN at a script that fails the test if invoked at all.
func TestProvisionRegistryCredentialSecretNoHostCredential(t *testing.T) {
	useDockerConfigDir(t, t.TempDir())
	useGHToken(t, func(string) (string, bool) { return "", false })
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("ERUN_KUBECTL_BIN", failingBinaryPath(t))

	name, err := provisionRegistryCredentialSecret(testTraceContext(false), "team", "team-dev", "", []string{"ghcr.io/sophium"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "" {
		t.Fatalf("got %q, want empty when the host has no credential to provision", name)
	}
}

// TestProvisionRegistryCredentialSecretDryRunSkipsApply proves dry-run
// resolves the same name real mode would, but never shells out to kubectl —
// the decision belongs in the trace, the side effect does not.
func TestProvisionRegistryCredentialSecretDryRunSkipsApply(t *testing.T) {
	dir := writeDockerConfig(t, fmt.Sprintf(`{"auths":{"ghcr.io":{"auth":%q}}}`, b64Auth("alice:s3cret")))
	useDockerConfigDir(t, dir)
	useGHToken(t, func(string) (string, bool) { return "", false })
	t.Setenv("ERUN_KUBECTL_BIN", failingBinaryPath(t))

	name, err := provisionRegistryCredentialSecret(testTraceContext(true), "team", "team-dev", "", []string{"ghcr.io/sophium"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "team-devops-registry-credential"; name != want {
		t.Fatalf("got %q, want %q", name, want)
	}
}

// failingBinaryPath writes a script that fails the test if ever executed, so a
// test asserting "kubectl must not run" catches a regression as a hard
// failure rather than a silently-passed apply.
func failingBinaryPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := dir + "/kubectl-must-not-run"
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho 'kubectl must not run in this test' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	return path
}
