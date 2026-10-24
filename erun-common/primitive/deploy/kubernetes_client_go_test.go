package deploy

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// fakeKubernetesAPIServer starts a plain-HTTP fake API server and writes a
// kubeconfig pointing KUBECONFIG at it, so libraryKubernetesNamespaceExists
// can be exercised through the exact same clientcmd loading path real kubectl
// use goes through — no daemon, no real cluster needed. Plain HTTP (not TLS)
// keeps the fixture to a single seam, mirroring stsCallerIdentityFixture's use
// of AWS_ENDPOINT_URL for the aws-sdk-go-v2 equivalence tests.
func fakeKubernetesAPIServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	kubeconfig := "apiVersion: v1\n" +
		"kind: Config\n" +
		"clusters:\n" +
		"  - name: test-cluster\n" +
		"    cluster:\n" +
		"      server: " + server.URL + "\n" +
		"contexts:\n" +
		"  - name: test-context\n" +
		"    context:\n" +
		"      cluster: test-cluster\n" +
		"      user: test-user\n" +
		"users:\n" +
		"  - name: test-user\n" +
		"    user:\n" +
		"      token: test-token\n" +
		"current-context: test-context\n"
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(kubeconfig), 0o600); err != nil {
		t.Fatalf("write kubeconfig: %v", err)
	}
	t.Setenv("KUBECONFIG", path)
	return server
}

// TestKubectlGetPVCArgsSharedByBothPaths locks the one argv builder
// kubectlWorktreeClaimArgs and defaultWorktreeClaimExists both call (via
// kubectlGetPVCArgs), so the dry-run trace can never drift from the
// subprocess execution path regardless of which execution mode is active.
func TestKubectlGetPVCArgsSharedByBothPaths(t *testing.T) {
	got := kubectlGetPVCArgs("my-context", "team-dev", "team-devops-worktree")
	want := []string{"--context", "my-context", "--namespace", "team-dev", "get", "pvc", "team-devops-worktree", "-o", "name"}
	if len(got) != len(want) {
		t.Fatalf("args = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("args = %v, want %v", got, want)
		}
	}
}

func TestKubectlGetPVCArgsOmitsContextAndNamespaceFlagsWhenUnset(t *testing.T) {
	got := kubectlGetPVCArgs("", "", "team-devops-worktree")
	want := []string{"get", "pvc", "team-devops-worktree", "-o", "name"}
	if len(got) != len(want) {
		t.Fatalf("args = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("args = %v, want %v", got, want)
		}
	}
}
