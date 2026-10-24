package cloud

import (
	"path/filepath"
	"testing"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func testCloudContextConfig() model.CloudContextConfig {
	return model.CloudContextConfig{
		Name:              "erun-001",
		KubernetesContext: "erun-001",
		PublicIP:          "203.0.113.10",
		AdminToken:        "s3cret-admin-token-value",
	}
}

// Only the trace is redacted; RunKubectl still gets the real argv.
func TestConfigureCloudKubeContextSubprocessModeStillExecutesRunKubectl(t *testing.T) {
	ctx, _ := traceCapturingContext()
	var calls [][]string
	deps := CloudContextDependencies{RunKubectl: func(_ core.Context, args []string) error {
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

func TestLibraryConfigureCloudKubeContextWritesKubeconfig(t *testing.T) {
	kubeconfigPath := filepath.Join(t.TempDir(), "config")
	t.Setenv("KUBECONFIG", kubeconfigPath)

	if err := libraryConfigureCloudKubeContext(testCloudContextConfig()); err != nil {
		t.Fatalf("libraryConfigureCloudKubeContext: %v", err)
	}

	assertCloudKubeContextEntryWritten(t, kubeconfigPath, "erun-001", "https://203.0.113.10:6443", "s3cret-admin-token-value")
}

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

	// The public IP can change on restart; the entry must update, not duplicate.
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
