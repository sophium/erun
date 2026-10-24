package deploy

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// imagePullSecretKubectlStub answers `get secret` from getResponses ("" means
// NotFound); an empty applyLog makes any apply fail.
func imagePullSecretKubectlStub(t *testing.T, getResponses map[string]string, applyLog string) {
	t.Helper()
	dir := t.TempDir()
	path := dir + "/kubectl-stub"

	var b strings.Builder
	b.WriteString("#!/bin/sh\nset -e\ncase \"$*\" in\n")
	for name, body := range getResponses {
		fmt.Fprintf(&b, "  *\"get secret %s -o json\"*)\n", name)
		if body == "" {
			fmt.Fprintf(&b, "    echo 'Error from server (NotFound): secrets \"%s\" not found' >&2\n    exit 1\n", name)
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
