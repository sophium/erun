package eruncommon

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/sophium/erun/erun-common/infra/kube"
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

// fakeKubernetesAPIServerForPod is fakeKubernetesAPIServer plus an explicit
// namespace on the kubeconfig context. libraryGetPod (unlike
// libraryKubernetesNamespaceExists/libraryPersistentVolumeClaimExists/
// libraryImagePullSecretAuths) takes no namespace argument -- it resolves the
// current namespace the same way kubectl does with no --namespace flag -- so
// its test kubeconfig needs an explicit namespace or clientcmd's
// DeferredLoadingClientConfig falls through to the in-cluster service account
// namespace file, which is unfakeable and depends on the host actually
// running this test.
func fakeKubernetesAPIServerForPod(t *testing.T, namespace string, handler http.HandlerFunc) *httptest.Server {
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
		"      namespace: " + namespace + "\n" +
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

// podGetResponseJSON renders the `kubectl get pod -o json` / client-go Pod
// body the two kubectl-pod-get call sites (jobSupervisorContainerRestart,
// dockerBuildContainerMemoryLimit) each read a different slice of: container
// status (for the restart check) and container spec (for the memory limit).
// One fixture covers both since a real Get returns the whole Pod either way.
func podGetResponseJSON(namespace, name string) string {
	return fmt.Sprintf(`{"kind":"Pod","apiVersion":"v1","metadata":{"name":%q,"namespace":%q},`+
		`"spec":{"containers":[{"name":"erun-devops"},{"name":"erun-dind","resources":{"limits":{"memory":"8916Mi"}}}]},`+
		`"status":{"containerStatuses":[`+
		`{"name":"erun-devops","lastState":{"terminated":{"reason":"OOMKilled","exitCode":137,"finishedAt":"2026-01-01T00:05:00Z"}}},`+
		`{"name":"erun-dind","lastState":{}}`+
		`]}}`, name, namespace)
}

func podFoundHandler(namespace, name string) http.HandlerFunc {
	path := "/api/v1/namespaces/" + namespace + "/pods/" + name
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"kind":"Status","status":"Failure","reason":"NotFound"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(podGetResponseJSON(namespace, name)))
	}
}

func podNotFoundHandler(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"kind":"Status","apiVersion":"v1","status":"Failure",` +
			`"message":"pods \"` + name + `\" not found","reason":"NotFound","code":404}`))
	}
}

// TestLibraryGetPodReadsContainerStatusAndSpec proves libraryGetPod returns a
// typed Pod carrying both the container-status slice
// libraryJobSupervisorContainerRestart reads and the container-spec slice
// libraryDockerBuildContainerMemoryLimit reads, from the one Get both call
// sites' dispatchers now share.
func TestLibraryGetPodReadsContainerStatusAndSpec(t *testing.T) {
	fakeKubernetesAPIServerForPod(t, "team-dev", podFoundHandler("team-dev", "pod-a"))

	pod, err := kube.LibraryGetPod("pod-a")
	if err != nil {
		t.Fatalf("libraryGetPod: %v", err)
	}
	if pod.Name != "pod-a" {
		t.Fatalf("pod.Name = %q, want %q", pod.Name, "pod-a")
	}
	restarted, reason, exitCode, _, ok := libraryJobSupervisorContainerRestart("pod-a", "erun-devops")
	if !ok || !restarted || reason != "OOMKilled" || exitCode != 137 {
		t.Fatalf("libraryJobSupervisorContainerRestart = (%v, %q, %d, ok=%v), want (true, OOMKilled, 137, true)", restarted, reason, exitCode, ok)
	}
	limit, found := libraryDockerBuildContainerMemoryLimit("pod-a", "erun-dind")
	if !found || limit != "8916Mi" {
		t.Fatalf("libraryDockerBuildContainerMemoryLimit = (%q, %v), want (8916Mi, true)", limit, found)
	}
}

// TestLibraryJobSupervisorContainerRestartReportsNoRestartAsADefiniteAnswer
// proves a container with no terminated lastState reads as "did not restart,
// but the answer is trusted" (ok=true), the same distinction
// defaultJobSupervisorContainerRestart draws for a live container.
func TestLibraryJobSupervisorContainerRestartReportsNoRestartAsADefiniteAnswer(t *testing.T) {
	fakeKubernetesAPIServerForPod(t, "team-dev", podFoundHandler("team-dev", "pod-a"))

	restarted, _, _, _, ok := libraryJobSupervisorContainerRestart("pod-a", "erun-dind")
	if !ok || restarted {
		t.Fatalf("restarted = %v, ok = %v, want (false, true) for a container with no terminated lastState", restarted, ok)
	}
}

// TestLibraryGetPodPropagatesNotFound proves a missing pod surfaces as an
// error rather than a zero-value Pod that could be misread as an answer --
// the "unknown must not render as a definite value" contract both dispatchers
// rely on when they fold any libraryGetPod error into ok=false/found=false.
func TestLibraryGetPodPropagatesNotFound(t *testing.T) {
	fakeKubernetesAPIServerForPod(t, "team-dev", podNotFoundHandler("pod-a"))

	if _, err := kube.LibraryGetPod("pod-a"); err == nil {
		t.Fatal("expected an error for a pod that does not exist, got nil")
	}
	if _, _, _, _, ok := libraryJobSupervisorContainerRestart("pod-a", "erun-devops"); ok {
		t.Fatal("expected ok=false when the pod cannot be read, not a guessed answer")
	}
	if _, found := libraryDockerBuildContainerMemoryLimit("pod-a", "erun-dind"); found {
		t.Fatal("expected found=false when the pod cannot be read, not a guessed answer")
	}
}
