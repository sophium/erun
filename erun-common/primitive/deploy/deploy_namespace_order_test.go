package deploy

import (
	"errors"
	"testing"

	"github.com/sophium/erun/erun-common/base/core"
)

// A refusing ensure makes the order checkable: run first, it returns the
// sentinel; run late, the secret apply fails differently.
func TestRunHelmDeployEnsuresTheNamespaceBeforeApplyingSecrets(t *testing.T) {
	sentinel := errors.New("namespace-ensure-ran-first")
	var gotNamespace string
	called := false

	original := ensureDeployNamespace
	ensureDeployNamespace = func(_, namespace string) error {
		called = true
		gotNamespace = namespace
		return sentinel
	}
	t.Cleanup(func() { ensureDeployNamespace = original })

	spec := HelmDeploySpec{
		ReleaseName:         "operations-devops",
		KubernetesContext:   "in-cluster",
		Namespace:           "operations-probe",
		ChartPath:           "oci://ghcr.io/sophium/charts/erun-devops",
		MCPAuthEnabled:      true,
		MCPAuthSecretName:   "operations-devops-mcp-auth",
		MCPAuthPublicKeyPEM: "-----BEGIN PUBLIC KEY-----\nAAAA\n-----END PUBLIC KEY-----",
	}
	err := RunHelmDeploy(core.Context{}, spec, func(HelmDeployParams) error {
		t.Error("the chart deployer must not run after the namespace ensure failed")
		return nil
	})

	if !called {
		t.Fatal("RunHelmDeploy must ensure the namespace before applying secrets into it")
	}
	if gotNamespace != "operations-probe" {
		t.Errorf("ensured namespace = %q, want operations-probe", gotNamespace)
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("err = %v, want the namespace-ensure failure to abort before the secret apply", err)
	}
}
