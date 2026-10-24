package registry

import (
	"strings"
	"testing"
)

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
