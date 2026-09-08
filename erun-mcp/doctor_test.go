package erunmcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrg/xdg"
)

// TestDoctorToolSkipsPodDependentSectionsWhenClusterUnreachable is the MCP
// counterpart of erun-integration's
// real_run_cluster_unreachable_skips_pod_dependent_sections_once_established:
// once the helm status read confirms the Kubernetes API server itself is
// unreachable, the Pods, Git push access, and Docker storage sections must
// report a skip instead of re-probing (erun#2394). kubectl is stubbed to fail
// the test outright if invoked, since with the fix none of these sections
// should ever shell out to it.
func TestDoctorToolSkipsPodDependentSectionsWhenClusterUnreachable(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	xdg.Reload()
	t.Cleanup(xdg.Reload)

	helmPath := filepath.Join(t.TempDir(), "helm")
	helmScript := "#!/bin/sh\ncase \"$1\" in\n  status)\n" +
		"    echo 'Error: kubernetes cluster unreachable: Get \"https://198.51.100.10:6443/version\": dial tcp 198.51.100.10:6443: i/o timeout' >&2\n" +
		"    exit 1\n    ;;\nesac\nexit 0\n"
	if err := os.WriteFile(helmPath, []byte(helmScript), 0o755); err != nil {
		t.Fatalf("write helm stub: %v", err)
	}
	t.Setenv("ERUN_HELM_BIN", helmPath)

	kubectlPath := filepath.Join(t.TempDir(), "kubectl-must-not-run")
	if err := os.WriteFile(kubectlPath, []byte("#!/bin/sh\necho 'kubectl must not run in this test' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write kubectl stub: %v", err)
	}
	t.Setenv("ERUN_KUBECTL_BIN", kubectlPath)

	runtime := normalizeRuntimeConfig(RuntimeConfig{
		Context: RuntimeContext{Tenant: "acme", Environment: "dev"},
		Store:   usageTestStore("acme", "dev"),
	})

	_, output, err := doctorTool(runtime)(context.Background(), nil, DoctorInput{PruneImages: true})
	if err != nil {
		t.Fatalf("doctorTool failed: %v", err)
	}
	stdout := output.Stdout
	for _, header := range []string{"Pods", "Git push access", "Docker storage"} {
		want := "== " + header + " ==\nskipped: the runtime pod is not reachable for this check"
		if !strings.Contains(stdout, want) {
			t.Errorf("expected %q to report a skip, got:\n%s", header, stdout)
		}
	}
	if !strings.Contains(stdout, "Skipping the requested prune action(s) for the same reason.") {
		t.Errorf("expected the requested prune action to be skipped, got:\n%s", stdout)
	}
}
