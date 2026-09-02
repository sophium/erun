package eruncommon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func TestExecutionModeForKubectlContextConfigureDefaultsToSubprocess(t *testing.T) {
	if got := ExecutionModeFor(ERunConfig{}, kubectlContextConfigureExecutionOperation); got != ExecutionModeSubprocess {
		t.Fatalf("ExecutionModeFor = %q, want %q", got, ExecutionModeSubprocess)
	}
}

func TestExecutionModeReportListsKubectlContextConfigureOperation(t *testing.T) {
	statuses := ExecutionModeReport(ERunConfig{})
	for _, status := range statuses {
		if status.Operation == kubectlContextConfigureExecutionOperation {
			return
		}
	}
	t.Fatalf("ExecutionModeReport did not include %q: %+v", kubectlContextConfigureExecutionOperation, statuses)
}

func testCloudContextConfig() CloudContextConfig {
	return CloudContextConfig{
		Name:              "erun-001",
		KubernetesContext: "erun-001",
		PublicIP:          "203.0.113.10",
		AdminToken:        "s3cret-admin-token-value",
	}
}

// TestConfigureCloudKubeContextDryRunTracesRedactedTokenAndTouchesNothing
// pins the audit contract: the trace must render the equivalent CLI
// invocation with the admin token redacted (never the plaintext secret, the
// same convention kubectl-secret-apply's stdin-piped applies already use),
// and a dry run must neither call kubectl nor write a kubeconfig file.
func TestConfigureCloudKubeContextDryRunTracesRedactedTokenAndTouchesNothing(t *testing.T) {
	kubeconfigPath := filepath.Join(t.TempDir(), "config")
	t.Setenv("KUBECONFIG", kubeconfigPath)

	ctx, trace := traceCapturingContext()
	ctx.DryRun = true
	deps := CloudContextDependencies{
		RunKubectl: func(Context, []string) error {
			t.Fatal("RunKubectl must not be called on a dry run")
			return nil
		},
	}

	if err := configureCloudKubeContext(ctx, deps, testCloudContextConfig()); err != nil {
		t.Fatalf("configureCloudKubeContext: %v", err)
	}

	got := trace.String()
	if strings.Contains(got, "s3cret-admin-token-value") {
		t.Fatalf("trace leaked the plaintext admin token: %q", got)
	}
	if !strings.Contains(got, "kubectl config set-credentials erun-001 --token '<redacted>'") {
		t.Fatalf("trace missing redacted set-credentials line: %q", got)
	}
	if !strings.Contains(got, "kubectl config set-cluster erun-001 --server https://203.0.113.10:6443 --insecure-skip-tls-verify=true") {
		t.Fatalf("trace missing set-cluster line: %q", got)
	}
	if !strings.Contains(got, "kubectl config set-context erun-001 --cluster erun-001 --user erun-001") {
		t.Fatalf("trace missing set-context line: %q", got)
	}
	if _, err := os.Stat(kubeconfigPath); err == nil {
		t.Fatalf("dry run must not write a kubeconfig file at %s", kubeconfigPath)
	}
}

// TestConfigureCloudKubeContextSubprocessModeStillExecutesRunKubectl pins the
// default (unset) execution mode to the existing subprocess fallback, with
// the real (unredacted) argv still reaching RunKubectl -- only the trace is
// redacted, execution is unaffected.
func TestConfigureCloudKubeContextSubprocessModeStillExecutesRunKubectl(t *testing.T) {
	ctx, _ := traceCapturingContext()
	var calls [][]string
	deps := CloudContextDependencies{RunKubectl: func(_ Context, args []string) error {
		calls = append(calls, args)
		return nil
	}}

	if err := configureCloudKubeContext(ctx, deps, testCloudContextConfig()); err != nil {
		t.Fatalf("configureCloudKubeContext: %v", err)
	}

	if len(calls) != 3 {
		t.Fatalf("RunKubectl called %d times, want 3: %+v", len(calls), calls)
	}
	found := false
	for _, args := range calls {
		for _, arg := range args {
			if arg == "s3cret-admin-token-value" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("subprocess execution must still receive the real admin token: %+v", calls)
	}
}

// TestLibraryConfigureCloudKubeContextWritesKubeconfig is the library path's
// equivalence proof: starting from no kubeconfig file at all, it must land
// the same cluster/authinfo/context fields the three subprocess calls would.
func TestLibraryConfigureCloudKubeContextWritesKubeconfig(t *testing.T) {
	kubeconfigPath := filepath.Join(t.TempDir(), "config")
	t.Setenv("KUBECONFIG", kubeconfigPath)

	if err := libraryConfigureCloudKubeContext(testCloudContextConfig()); err != nil {
		t.Fatalf("libraryConfigureCloudKubeContext: %v", err)
	}

	assertCloudKubeContextEntryWritten(t, kubeconfigPath, "erun-001", "https://203.0.113.10:6443", "s3cret-admin-token-value")
}

// assertCloudKubeContextEntryWritten reloads the kubeconfig at path and
// asserts the named cluster/authinfo/context entries carry the values the
// three subprocess `kubectl config` calls would have written.
func assertCloudKubeContextEntryWritten(t *testing.T, path, name, server, token string) {
	t.Helper()
	written, err := clientcmd.LoadFromFile(path)
	if err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}
	cluster, ok := written.Clusters[name]
	if !ok {
		t.Fatalf("cluster %q not written: %+v", name, written.Clusters)
	}
	if cluster.Server != server || !cluster.InsecureSkipTLSVerify {
		t.Fatalf("cluster = %+v, want server %s insecure-skip-tls-verify=true", cluster, server)
	}
	authInfo, ok := written.AuthInfos[name]
	if !ok || authInfo.Token != token {
		t.Fatalf("authInfo = %+v, want token %s", authInfo, token)
	}
	kubeContext, ok := written.Contexts[name]
	if !ok || kubeContext.Cluster != name || kubeContext.AuthInfo != name {
		t.Fatalf("context = %+v, want cluster/user %s", kubeContext, name)
	}
}

// TestLibraryConfigureCloudKubeContextPreservesUnrelatedEntries proves the
// library path merges into an existing kubeconfig the same way kubectl
// itself would -- an unrelated cluster/context (and CurrentContext) already
// on disk survives untouched, and re-running against the same name updates
// that entry in place rather than duplicating it.
func TestLibraryConfigureCloudKubeContextPreservesUnrelatedEntries(t *testing.T) {
	kubeconfigPath := filepath.Join(t.TempDir(), "config")
	seedKubeconfigWithOneEntry(t, kubeconfigPath, "existing-cluster", "existing-context")
	t.Setenv("KUBECONFIG", kubeconfigPath)

	if err := libraryConfigureCloudKubeContext(testCloudContextConfig()); err != nil {
		t.Fatalf("libraryConfigureCloudKubeContext: %v", err)
	}

	written, err := clientcmd.LoadFromFile(kubeconfigPath)
	if err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}
	if _, ok := written.Clusters["existing-cluster"]; !ok {
		t.Fatalf("pre-existing cluster was dropped: %+v", written.Clusters)
	}
	if written.CurrentContext != "existing-context" {
		t.Fatalf("CurrentContext changed to %q, want unchanged existing-context", written.CurrentContext)
	}
	if _, ok := written.Clusters["erun-001"]; !ok {
		t.Fatalf("new cluster was not added: %+v", written.Clusters)
	}

	// Re-running against the same name (e.g. the instance's public IP
	// changed on restart) must update the existing entry, not duplicate it.
	updated := testCloudContextConfig()
	updated.PublicIP = "203.0.113.20"
	if err := libraryConfigureCloudKubeContext(updated); err != nil {
		t.Fatalf("libraryConfigureCloudKubeContext (update): %v", err)
	}
	written, err = clientcmd.LoadFromFile(kubeconfigPath)
	if err != nil {
		t.Fatalf("LoadFromFile (update): %v", err)
	}
	if len(written.Clusters) != 2 {
		t.Fatalf("Clusters = %+v, want exactly 2 (existing-cluster, erun-001)", written.Clusters)
	}
	if written.Clusters["erun-001"].Server != "https://203.0.113.20:6443" {
		t.Fatalf("cluster not updated in place: %+v", written.Clusters["erun-001"])
	}
}

// seedKubeconfigWithOneEntry writes a minimal existing kubeconfig with one
// cluster/user/context (and CurrentContext set to it) so a merge test can
// prove unrelated entries survive.
func seedKubeconfigWithOneEntry(t *testing.T, path, clusterName, contextName string) {
	t.Helper()
	config := clientcmdapi.NewConfig()
	config.Clusters[clusterName] = &clientcmdapi.Cluster{Server: "https://existing.example:6443"}
	config.AuthInfos[clusterName] = &clientcmdapi.AuthInfo{Token: "existing-token"}
	config.Contexts[contextName] = &clientcmdapi.Context{Cluster: clusterName, AuthInfo: clusterName}
	config.CurrentContext = contextName
	if err := clientcmd.WriteToFile(*config, path); err != nil {
		t.Fatalf("seedKubeconfigWithOneEntry: %v", err)
	}
}
