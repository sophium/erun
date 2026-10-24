package kube

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeKubernetesAPIServer points KUBECONFIG at a plain-HTTP fake API server so the
// real clientcmd loading path runs without a cluster.
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

func namespaceFoundHandler(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/namespaces/"+name {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"kind":"Status","status":"Failure","reason":"NotFound"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"kind":"Namespace","apiVersion":"v1","metadata":{"name":"` + name + `"}}`))
	}
}

func namespaceNotFoundHandler(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"kind":"Status","apiVersion":"v1","status":"Failure",` +
			`"message":"namespaces \"` + name + `\" not found","reason":"NotFound","code":404}`))
	}
}

// The library path's bool must match kubectl's exit-code result.
func TestLibraryKubernetesNamespaceExistsMatchesSubprocessObservableResult(t *testing.T) {
	fakeKubernetesAPIServer(t, namespaceFoundHandler("team-dev"))

	exists, err := libraryKubernetesNamespaceExists("", "team-dev")
	if err != nil {
		t.Fatalf("libraryKubernetesNamespaceExists: %v", err)
	}
	if !exists {
		t.Fatalf("exists = false, want true")
	}
}

func TestLibraryKubernetesNamespaceExistsReportsNotFoundAsAbsentNotError(t *testing.T) {
	fakeKubernetesAPIServer(t, namespaceNotFoundHandler("team-dev"))

	exists, err := libraryKubernetesNamespaceExists("", "team-dev")
	if err != nil {
		t.Fatalf("libraryKubernetesNamespaceExists: %v", err)
	}
	if exists {
		t.Fatalf("exists = true, want false")
	}
}

func TestLibraryKubernetesNamespaceExistsPropagatesOtherErrors(t *testing.T) {
	fakeKubernetesAPIServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"kind":"Status","apiVersion":"v1","status":"Failure",` +
			`"message":"namespaces \"team-dev\" is forbidden","reason":"Forbidden","code":403}`))
	})

	exists, err := libraryKubernetesNamespaceExists("", "team-dev")
	if err == nil {
		t.Fatalf("err = nil, want a forbidden error")
	}
	if exists {
		t.Fatalf("exists = true, want false")
	}
}

func TestLibraryKubernetesNamespaceExistsHonorsContextOverride(t *testing.T) {
	fakeKubernetesAPIServer(t, namespaceFoundHandler("team-dev"))

	exists, err := libraryKubernetesNamespaceExists("test-context", "team-dev")
	if err != nil {
		t.Fatalf("libraryKubernetesNamespaceExists: %v", err)
	}
	if !exists {
		t.Fatalf("exists = false, want true")
	}

	if _, err := libraryKubernetesNamespaceExists("unknown-context", "team-dev"); err == nil {
		t.Fatalf("err = nil, want an error for an unknown context")
	}
}

func TestKubectlGetNamespaceArgsOmitsContextFlagWhenUnset(t *testing.T) {
	got := kubectlGetNamespaceArgs("", "team-dev")
	want := []string{"get", "namespace", "team-dev", "-o", "name"}
	if len(got) != len(want) {
		t.Fatalf("args = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("args = %v, want %v", got, want)
		}
	}
}

func pvcFoundHandler(namespace, name string) http.HandlerFunc {
	path := "/api/v1/namespaces/" + namespace + "/persistentvolumeclaims/" + name
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"kind":"Status","status":"Failure","reason":"NotFound"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"kind":"PersistentVolumeClaim","apiVersion":"v1","metadata":{"name":"` + name + `","namespace":"` + namespace + `"}}`))
	}
}

func pvcNotFoundHandler(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"kind":"Status","apiVersion":"v1","status":"Failure",` +
			`"message":"persistentvolumeclaims \"` + name + `\" not found","reason":"NotFound","code":404}`))
	}
}

func TestLibraryPersistentVolumeClaimExistsMatchesSubprocessObservableResult(t *testing.T) {
	fakeKubernetesAPIServer(t, pvcFoundHandler("team-dev", "team-devops-worktree"))

	exists, err := LibraryPersistentVolumeClaimExists("", "team-dev", "team-devops-worktree")
	if err != nil {
		t.Fatalf("libraryPersistentVolumeClaimExists: %v", err)
	}
	if !exists {
		t.Fatalf("exists = false, want true")
	}
}

func TestLibraryPersistentVolumeClaimExistsReportsNotFoundAsAbsentNotError(t *testing.T) {
	fakeKubernetesAPIServer(t, pvcNotFoundHandler("team-devops-worktree"))

	exists, err := LibraryPersistentVolumeClaimExists("", "team-dev", "team-devops-worktree")
	if err != nil {
		t.Fatalf("libraryPersistentVolumeClaimExists: %v", err)
	}
	if exists {
		t.Fatalf("exists = true, want false")
	}
}

func TestLibraryPersistentVolumeClaimExistsPropagatesOtherErrors(t *testing.T) {
	fakeKubernetesAPIServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"kind":"Status","apiVersion":"v1","status":"Failure",` +
			`"message":"persistentvolumeclaims \"team-devops-worktree\" is forbidden","reason":"Forbidden","code":403}`))
	})

	exists, err := LibraryPersistentVolumeClaimExists("", "team-dev", "team-devops-worktree")
	if err == nil {
		t.Fatalf("err = nil, want a forbidden error")
	}
	if exists {
		t.Fatalf("exists = true, want false")
	}
}

func TestLibraryPersistentVolumeClaimExistsHonorsContextOverride(t *testing.T) {
	fakeKubernetesAPIServer(t, pvcFoundHandler("team-dev", "team-devops-worktree"))

	exists, err := LibraryPersistentVolumeClaimExists("test-context", "team-dev", "team-devops-worktree")
	if err != nil {
		t.Fatalf("libraryPersistentVolumeClaimExists: %v", err)
	}
	if !exists {
		t.Fatalf("exists = false, want true")
	}

	if _, err := LibraryPersistentVolumeClaimExists("unknown-context", "team-dev", "team-devops-worktree"); err == nil {
		t.Fatalf("err = nil, want an error for an unknown context")
	}
}

func secretGetResponseJSON(namespace, name, dockerConfigJSON string) string {
	encoded := base64.StdEncoding.EncodeToString([]byte(dockerConfigJSON))
	return fmt.Sprintf(`{"kind":"Secret","apiVersion":"v1","metadata":{"name":%q,"namespace":%q},"data":{".dockerconfigjson":%q}}`,
		name, namespace, encoded)
}

func secretFoundHandler(namespace, name, dockerConfigJSON string) http.HandlerFunc {
	path := "/api/v1/namespaces/" + namespace + "/secrets/" + name
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"kind":"Status","status":"Failure","reason":"NotFound"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(secretGetResponseJSON(namespace, name, dockerConfigJSON)))
	}
}

func secretNotFoundHandler(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"kind":"Status","apiVersion":"v1","status":"Failure",` +
			`"message":"secrets \"` + name + `\" not found","reason":"NotFound","code":404}`))
	}
}

// Secret.Data arrives already decoded, so the library path needs no base64 step.
func TestLibraryImagePullSecretAuthsMatchesSubprocessObservableResult(t *testing.T) {
	fakeKubernetesAPIServer(t, secretFoundHandler("team-dev", "regcred", `{"auths":{"ghcr.io":{"auth":"alice:s3cret"}}}`))

	auths, err := LibraryImagePullSecretAuths("", "team-dev", "regcred")
	if err != nil {
		t.Fatalf("libraryImagePullSecretAuths: %v", err)
	}
	if got, want := auths["ghcr.io"].Auth, "alice:s3cret"; got != want {
		t.Fatalf("auths[ghcr.io].Auth = %q, want %q", got, want)
	}
}

func TestLibraryImagePullSecretAuthsReportsNotFoundAsNilNotError(t *testing.T) {
	fakeKubernetesAPIServer(t, secretNotFoundHandler("regcred"))

	auths, err := LibraryImagePullSecretAuths("", "team-dev", "regcred")
	if err != nil {
		t.Fatalf("libraryImagePullSecretAuths: %v", err)
	}
	if auths != nil {
		t.Fatalf("auths = %+v, want nil for a Secret that does not exist yet", auths)
	}
}

// Folding Forbidden into "no coverage" would let a merge drop credentials RBAC only hid.
func TestLibraryImagePullSecretAuthsPropagatesOtherErrors(t *testing.T) {
	fakeKubernetesAPIServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"kind":"Status","apiVersion":"v1","status":"Failure",` +
			`"message":"secrets \"regcred\" is forbidden","reason":"Forbidden","code":403}`))
	})

	auths, err := LibraryImagePullSecretAuths("", "team-dev", "regcred")
	if err == nil {
		t.Fatalf("err = nil, want a forbidden error")
	}
	if auths != nil {
		t.Fatalf("auths = %+v, want nil alongside the error", auths)
	}
}

func TestLibraryImagePullSecretAuthsRefusesMalformedDockerConfig(t *testing.T) {
	fakeKubernetesAPIServer(t, secretFoundHandler("team-dev", "regcred", "not valid json"))

	_, err := LibraryImagePullSecretAuths("", "team-dev", "regcred")
	if err == nil {
		t.Fatal("expected an error for a Secret whose .dockerconfigjson does not decode, got nil")
	}
	if strings.Contains(err.Error(), "not valid json") {
		t.Fatalf("error must not leak the decoded secret content: %v", err)
	}
}

func TestLibraryImagePullSecretAuthsHonorsContextOverride(t *testing.T) {
	fakeKubernetesAPIServer(t, secretFoundHandler("team-dev", "regcred", `{"auths":{"ghcr.io":{"auth":"alice:s3cret"}}}`))

	auths, err := LibraryImagePullSecretAuths("test-context", "team-dev", "regcred")
	if err != nil {
		t.Fatalf("libraryImagePullSecretAuths: %v", err)
	}
	if got, want := auths["ghcr.io"].Auth, "alice:s3cret"; got != want {
		t.Fatalf("auths[ghcr.io].Auth = %q, want %q", got, want)
	}

	if _, err := LibraryImagePullSecretAuths("unknown-context", "team-dev", "regcred"); err == nil {
		t.Fatalf("err = nil, want an error for an unknown context")
	}
}
