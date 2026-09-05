package eruncommon

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"testing"
)

// imagePullSecretKubectlStub builds a kubectl stub that answers `get secret
// <name> -o json` from a fixed table (an empty body means the Secret does not
// exist yet) and, when applyLog is non-empty, appends every applied
// manifest's stdin to it; an empty applyLog makes any `apply` invocation fail
// the test, the same "must not run" shape failingBinaryPath uses.
func imagePullSecretKubectlStub(t *testing.T, getResponses map[string]string, applyLog string) {
	t.Helper()
	dir := t.TempDir()
	path := dir + "/kubectl-stub"

	var b strings.Builder
	b.WriteString("#!/bin/sh\nset -e\ncase \"$*\" in\n")
	for name, body := range getResponses {
		b.WriteString(fmt.Sprintf("  *\"get secret %s -o json\"*)\n", name))
		if body == "" {
			b.WriteString(fmt.Sprintf("    echo 'Error from server (NotFound): secrets \"%s\" not found' >&2\n    exit 1\n", name))
		} else {
			b.WriteString("    cat <<'STUBEOF'\n" + body + "\nSTUBEOF\n    exit 0\n")
		}
		b.WriteString("    ;;\n")
	}
	b.WriteString("  *\"apply -f -\"*)\n")
	if applyLog == "" {
		b.WriteString("    echo 'kubectl apply must not run in this test' >&2\n    exit 1\n")
	} else {
		b.WriteString("    cat >> '" + applyLog + "'\n    printf -- '---\\n' >> '" + applyLog + "'\n    exit 0\n")
	}
	b.WriteString("    ;;\n  *)\n    echo \"unexpected kubectl invocation: $*\" >&2\n    exit 1\n    ;;\nesac\n")

	if err := os.WriteFile(path, []byte(b.String()), 0o755); err != nil {
		t.Fatalf("write kubectl stub: %v", err)
	}
	t.Setenv("ERUN_KUBECTL_BIN", path)
}

// secretGetJSON renders the `kubectl get secret -o json` body a Secret with
// the given already-encoded .dockerconfigjson value would return.
func secretGetJSON(dockerConfigJSON string) string {
	encoded := base64.StdEncoding.EncodeToString([]byte(dockerConfigJSON))
	return fmt.Sprintf(`{"data":{".dockerconfigjson":%q}}`, encoded)
}

func useNoECRLogin(t *testing.T) {
	t.Helper()
	prev := runECRLoginPassword
	runECRLoginPassword = func(string) (string, bool) { return "", false }
	t.Cleanup(func() { runECRLoginPassword = prev })
}

// TestRefreshImagePullSecretsMergesUnresolvedHostAndOmitsNeverSeenHost proves
// the core fix: a redeploy that can only resolve one of the hosts a Secret
// already covers refreshes that host's entry and leaves the other byte-for-
// byte untouched (never "uncovered" the way a full-document rewrite left it),
// while a host that was never covered and still does not resolve gets no
// placeholder entry at all.
func TestRefreshImagePullSecretsMergesUnresolvedHostAndOmitsNeverSeenHost(t *testing.T) {
	dir := writeDockerConfig(t, fmt.Sprintf(`{"auths":{"ghcr.io":{"auth":%q}}}`, b64Auth("alice:s3cret")))
	useDockerConfigDir(t, dir)
	useNoECRLogin(t)

	const ecrHost = "111122223333.dkr.ecr.us-east-1.amazonaws.com"
	const existingECRAuth = "old-ecr-auth-value"
	existingSecretJSON := secretGetJSON(fmt.Sprintf(`{"auths":{"ghcr.io":{"auth":"stale-ghcr-auth"},%q:{"auth":%q}}}`, ecrHost, existingECRAuth))

	applyLog := t.TempDir() + "/applied.yaml"
	imagePullSecretKubectlStub(t, map[string]string{"regcred": existingSecretJSON}, applyLog)

	deployInput := HelmDeploySpec{
		Namespace:         "team-dev",
		ImagePullSecrets:  []string{"regcred"},
		ContainerRegistry: "ghcr.io/acme",
		ImageOverrides: map[string]string{
			"runtime": ecrHost + "/acme/runtime:v1",
			"sidecar": "never-seen.example.com/acme/sidecar:v1",
		},
	}

	if err := refreshImagePullSecrets(testTraceContext(false), deployInput); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	applied, err := os.ReadFile(applyLog)
	if err != nil {
		t.Fatalf("read applied manifest: %v", err)
	}
	manifest := string(applied)

	if want := b64Auth("alice:s3cret"); !strings.Contains(manifest, want) {
		t.Fatalf("manifest missing refreshed ghcr.io credential %q:\n%s", want, manifest)
	}
	if strings.Contains(manifest, "stale-ghcr-auth") {
		t.Fatalf("manifest still carries the stale ghcr.io credential it should have refreshed:\n%s", manifest)
	}
	if !strings.Contains(manifest, existingECRAuth) {
		t.Fatalf("manifest lost the unresolved ECR host's existing credential:\n%s", manifest)
	}
	if strings.Contains(manifest, "never-seen.example.com") {
		t.Fatalf("manifest carries a placeholder entry for a host that never resolved and never had one:\n%s", manifest)
	}
}

// TestRefreshImagePullSecretsFirstDeployNoExistingSecret proves a first
// deploy, with no pre-existing Secret to read back, behaves as it always did:
// the Secret is minted from whatever resolves this run.
func TestRefreshImagePullSecretsFirstDeployNoExistingSecret(t *testing.T) {
	dir := writeDockerConfig(t, fmt.Sprintf(`{"auths":{"ghcr.io":{"auth":%q}}}`, b64Auth("alice:s3cret")))
	useDockerConfigDir(t, dir)

	applyLog := t.TempDir() + "/applied.yaml"
	imagePullSecretKubectlStub(t, map[string]string{"regcred": ""}, applyLog)

	deployInput := HelmDeploySpec{
		Namespace:         "team-dev",
		ImagePullSecrets:  []string{"regcred"},
		ContainerRegistry: "ghcr.io/acme",
	}

	if err := refreshImagePullSecrets(testTraceContext(false), deployInput); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	applied, err := os.ReadFile(applyLog)
	if err != nil {
		t.Fatalf("read applied manifest: %v", err)
	}
	if want := b64Auth("alice:s3cret"); !strings.Contains(string(applied), want) {
		t.Fatalf("manifest missing the resolved ghcr.io credential %q:\n%s", want, string(applied))
	}
}

// TestRefreshImagePullSecretsDryRunWritesNothing proves dry-run calls kubectl
// not at all -- neither the read nor the apply -- the same tradeoff
// TraceEnsureKubernetesNamespace makes for the namespace-exists check: a dry
// run states the merge outcome as conditional on a read it never performs,
// instead of forcing every dry-run scenario to declare a kubectl stub. The
// stub here declares no responses at all, so any invocation fails the test.
func TestRefreshImagePullSecretsDryRunWritesNothing(t *testing.T) {
	dir := writeDockerConfig(t, fmt.Sprintf(`{"auths":{"ghcr.io":{"auth":%q}}}`, b64Auth("alice:s3cret")))
	useDockerConfigDir(t, dir)
	imagePullSecretKubectlStub(t, nil, "")

	deployInput := HelmDeploySpec{
		Namespace:         "team-dev",
		ImagePullSecrets:  []string{"regcred"},
		ContainerRegistry: "ghcr.io/acme",
	}

	if err := refreshImagePullSecrets(testTraceContext(true), deployInput); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestExistingImagePullSecretAuthsRefusesMalformedSecret proves a Secret that
// exists but cannot be decoded is refused rather than silently rebuilt from
// the resolved subset alone -- guessing wrong there would destroy exactly the
// coverage this function exists to protect.
func TestExistingImagePullSecretAuthsRefusesMalformedSecret(t *testing.T) {
	badEncoded := base64.StdEncoding.EncodeToString([]byte("not valid json"))
	getBody := fmt.Sprintf(`{"data":{".dockerconfigjson":%q}}`, badEncoded)
	imagePullSecretKubectlStub(t, map[string]string{"regcred": getBody}, "")

	args := imagePullSecretGetArgs("team-dev", "", "regcred")
	_, err := existingImagePullSecretAuths("", "team-dev", "regcred", args)
	if err == nil {
		t.Fatal("expected an error for a Secret whose .dockerconfigjson does not decode, got nil")
	}
	if strings.Contains(err.Error(), "not valid json") {
		t.Fatalf("error must not leak the decoded secret content: %v", err)
	}
}

// TestMergeImagePullSecretAuthsNoPlaceholderForUnresolvedHost locks the
// narrower unit-level contract behind the end-to-end test above: merging
// never invents an entry for a host with neither an existing entry nor a
// resolved credential.
func TestMergeImagePullSecretAuthsNoPlaceholderForUnresolvedHost(t *testing.T) {
	existing := map[string]dockerConfigJSONAuthEntry{"ghcr.io": {Auth: "existing-value"}}
	credentials := map[string]registryBasicAuth{"ghcr.io": {username: "alice", secret: "s3cret"}}

	merged := mergeImagePullSecretAuths(existing, credentials)
	if len(merged) != 1 {
		t.Fatalf("got %d entries, want 1 (no placeholder for anything else): %+v", len(merged), merged)
	}
	if got, want := merged["ghcr.io"].Auth, b64Auth("alice:s3cret"); got != want {
		t.Fatalf("ghcr.io auth = %q, want refreshed value %q", got, want)
	}
}
