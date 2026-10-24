package jobs

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/sophium/erun/erun-common/infra/kube"
	"github.com/sophium/erun/erun-common/primitive/build"
)

// fakeKubernetesAPIServerForPod pins the kubeconfig namespace; libraryGetPod
// takes none and would otherwise fall back to the in-cluster service account file.
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

// podGetResponseJSON renders one Pod carrying both container status and spec.
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
	limit, found := build.LibraryDockerBuildContainerMemoryLimit("pod-a", "erun-dind")
	if !found || limit != "8916Mi" {
		t.Fatalf("libraryDockerBuildContainerMemoryLimit = (%q, %v), want (8916Mi, true)", limit, found)
	}
}

// No terminated lastState is a trusted "did not restart" (ok=true).
func TestLibraryJobSupervisorContainerRestartReportsNoRestartAsADefiniteAnswer(t *testing.T) {
	fakeKubernetesAPIServerForPod(t, "team-dev", podFoundHandler("team-dev", "pod-a"))

	restarted, _, _, _, ok := libraryJobSupervisorContainerRestart("pod-a", "erun-dind")
	if !ok || restarted {
		t.Fatalf("restarted = %v, ok = %v, want (false, true) for a container with no terminated lastState", restarted, ok)
	}
}

// A missing pod must be an error, never a zero-value Pod.
func TestLibraryGetPodPropagatesNotFound(t *testing.T) {
	fakeKubernetesAPIServerForPod(t, "team-dev", podNotFoundHandler("pod-a"))

	if _, err := kube.LibraryGetPod("pod-a"); err == nil {
		t.Fatal("expected an error for a pod that does not exist, got nil")
	}
	if _, _, _, _, ok := libraryJobSupervisorContainerRestart("pod-a", "erun-devops"); ok {
		t.Fatal("expected ok=false when the pod cannot be read, not a guessed answer")
	}
	if _, found := build.LibraryDockerBuildContainerMemoryLimit("pod-a", "erun-dind"); found {
		t.Fatal("expected found=false when the pod cannot be read, not a guessed answer")
	}
}
