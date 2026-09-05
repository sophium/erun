package eruncommon

import (
	"errors"
	"testing"
)

// TestRunHelmDeployEnsuresTheNamespaceBeforeApplyingSecrets proves an ordering
// the trace could not. RunHelmDeploy traced the namespace ensure first, then
// applied real kubectl secrets into that namespace -- but the namespace was only
// created later, by WrapHelmChartDeployerWithNamespaceEnsure around the chart
// deployer RunHelmDeploy calls at the end. So a first deploy into a freshly
// provisioned environment failed with "namespaces not found" and every hosted
// provision broke; an environment that already existed hid it entirely.
//
// Swapping the ensure for one that refuses is what makes the order checkable: if
// it runs first, RunHelmDeploy returns this sentinel; if it runs late -- or not
// at all, as before -- the secret apply is reached first and the error differs.
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
	err := RunHelmDeploy(Context{}, spec, func(HelmDeployParams) error {
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
