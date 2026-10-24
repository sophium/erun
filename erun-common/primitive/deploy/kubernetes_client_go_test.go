package deploy

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// fakeKubernetesAPIServer serves plain HTTP and points KUBECONFIG at it, exercising
// the real clientcmd loading path without a cluster.
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
