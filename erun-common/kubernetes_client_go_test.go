package eruncommon

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

// TestLibraryKubernetesNamespaceExistsMatchesSubprocessObservableResult pins
// the same equivalence property cloud_aws_sdk_test.go's
// TestLibraryResolveAWSIdentityMatchesSubprocessObservableResult does: the
// library path's returned bool must agree with what
// defaultKubernetesNamespaceExists derives from kubectl's exit code (found -> exit
// 0, not found -> "NotFound" stderr).
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

// TestLibraryKubernetesNamespaceExistsPropagatesOtherErrors proves a refusal
// distinct from NotFound (Forbidden here) surfaces as an error rather than
// being folded into "does not exist" — the same distinction
// defaultKubernetesNamespaceExists's KubernetesResourceNotFound check draws
// against kubectl's own stderr.
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

// TestLibraryKubernetesNamespaceExistsHonorsContextOverride proves the
// context-name argument actually selects the kubeconfig context, the same way
// `kubectl --context X` does, rather than always following current-context.
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

// TestKubectlGetNamespaceArgsSharedByBothPaths locks the one argv builder
// TraceEnsureKubernetesNamespace and defaultKubernetesNamespaceExists both
// call, so the dry-run trace can never drift from the subprocess execution
// path regardless of which execution mode is active.
func TestKubectlGetNamespaceArgsSharedByBothPaths(t *testing.T) {
	got := kubectlGetNamespaceArgs("my-context", "team-dev")
	want := []string{"--context", "my-context", "get", "namespace", "team-dev", "-o", "name"}
	if len(got) != len(want) {
		t.Fatalf("args = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("args = %v, want %v", got, want)
		}
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

func TestExecutionModeForKubectlNamespaceGetDefaultsToSubprocess(t *testing.T) {
	if got := ExecutionModeFor(ERunConfig{}, kubectlNamespaceGetExecutionOperation); got != ExecutionModeSubprocess {
		t.Fatalf("mode = %q, want %q", got, ExecutionModeSubprocess)
	}
}

func TestExecutionModeReportListsKubectlNamespaceGetOperation(t *testing.T) {
	report := ExecutionModeReport(ERunConfig{})
	for _, status := range report {
		if status.Operation == kubectlNamespaceGetExecutionOperation {
			if status.Mode != ExecutionModeSubprocess {
				t.Fatalf("mode = %q, want %q", status.Mode, ExecutionModeSubprocess)
			}
			return
		}
	}
	t.Fatalf("kubectl-namespace-get not found in report: %+v", report)
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

// TestLibraryPersistentVolumeClaimExistsMatchesSubprocessObservableResult pins
// the same equivalence property
// TestLibraryKubernetesNamespaceExistsMatchesSubprocessObservableResult does:
// the library path's returned bool must agree with what
// defaultWorktreeClaimExists derives from kubectl's exit code (found -> exit
// 0, not found -> "NotFound" stderr).
func TestLibraryPersistentVolumeClaimExistsMatchesSubprocessObservableResult(t *testing.T) {
	fakeKubernetesAPIServer(t, pvcFoundHandler("team-dev", "team-devops-worktree"))

	exists, err := libraryPersistentVolumeClaimExists("", "team-dev", "team-devops-worktree")
	if err != nil {
		t.Fatalf("libraryPersistentVolumeClaimExists: %v", err)
	}
	if !exists {
		t.Fatalf("exists = false, want true")
	}
}

func TestLibraryPersistentVolumeClaimExistsReportsNotFoundAsAbsentNotError(t *testing.T) {
	fakeKubernetesAPIServer(t, pvcNotFoundHandler("team-devops-worktree"))

	exists, err := libraryPersistentVolumeClaimExists("", "team-dev", "team-devops-worktree")
	if err != nil {
		t.Fatalf("libraryPersistentVolumeClaimExists: %v", err)
	}
	if exists {
		t.Fatalf("exists = true, want false")
	}
}

// TestLibraryPersistentVolumeClaimExistsPropagatesOtherErrors proves a
// refusal distinct from NotFound (Forbidden here) surfaces as an error rather
// than being folded into "does not exist" — the same distinction
// defaultWorktreeClaimExists's KubernetesResourceNotFound check draws against
// kubectl's own stderr.
func TestLibraryPersistentVolumeClaimExistsPropagatesOtherErrors(t *testing.T) {
	fakeKubernetesAPIServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"kind":"Status","apiVersion":"v1","status":"Failure",` +
			`"message":"persistentvolumeclaims \"team-devops-worktree\" is forbidden","reason":"Forbidden","code":403}`))
	})

	exists, err := libraryPersistentVolumeClaimExists("", "team-dev", "team-devops-worktree")
	if err == nil {
		t.Fatalf("err = nil, want a forbidden error")
	}
	if exists {
		t.Fatalf("exists = true, want false")
	}
}

// TestLibraryPersistentVolumeClaimExistsHonorsContextOverride proves the
// context-name argument actually selects the kubeconfig context, the same way
// `kubectl --context X` does, rather than always following current-context.
func TestLibraryPersistentVolumeClaimExistsHonorsContextOverride(t *testing.T) {
	fakeKubernetesAPIServer(t, pvcFoundHandler("team-dev", "team-devops-worktree"))

	exists, err := libraryPersistentVolumeClaimExists("test-context", "team-dev", "team-devops-worktree")
	if err != nil {
		t.Fatalf("libraryPersistentVolumeClaimExists: %v", err)
	}
	if !exists {
		t.Fatalf("exists = false, want true")
	}

	if _, err := libraryPersistentVolumeClaimExists("unknown-context", "team-dev", "team-devops-worktree"); err == nil {
		t.Fatalf("err = nil, want an error for an unknown context")
	}
}

// secretGetResponseJSON renders the `kubectl get secret -o json` /
// client-go Secret body a Secret carrying the given (already-encoded)
// .dockerconfigjson value would return.
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

// TestLibraryImagePullSecretAuthsMatchesSubprocessObservableResult pins the
// same equivalence property the namespace/PVC tests do: the library path's
// decoded auths must agree with what defaultExistingImagePullSecretAuths
// derives from kubectl's own base64-encoded JSON output. A typed Secret's
// Data field arrives already base64-decoded, so the library path needs no
// separate decode step -- the property under test is that it lands on the
// same auths document regardless.
func TestLibraryImagePullSecretAuthsMatchesSubprocessObservableResult(t *testing.T) {
	fakeKubernetesAPIServer(t, secretFoundHandler("team-dev", "regcred", `{"auths":{"ghcr.io":{"auth":"alice:s3cret"}}}`))

	auths, err := libraryImagePullSecretAuths("", "team-dev", "regcred")
	if err != nil {
		t.Fatalf("libraryImagePullSecretAuths: %v", err)
	}
	if got, want := auths["ghcr.io"].Auth, "alice:s3cret"; got != want {
		t.Fatalf("auths[ghcr.io].Auth = %q, want %q", got, want)
	}
}

func TestLibraryImagePullSecretAuthsReportsNotFoundAsNilNotError(t *testing.T) {
	fakeKubernetesAPIServer(t, secretNotFoundHandler("regcred"))

	auths, err := libraryImagePullSecretAuths("", "team-dev", "regcred")
	if err != nil {
		t.Fatalf("libraryImagePullSecretAuths: %v", err)
	}
	if auths != nil {
		t.Fatalf("auths = %+v, want nil for a Secret that does not exist yet", auths)
	}
}

// TestLibraryImagePullSecretAuthsPropagatesOtherErrors proves a refusal
// distinct from NotFound (Forbidden here) surfaces as an error rather than
// being folded into "no existing coverage" -- collapsing the two would let a
// merge silently drop credentials the operator's RBAC only stopped it from
// reading, not from actually needing.
func TestLibraryImagePullSecretAuthsPropagatesOtherErrors(t *testing.T) {
	fakeKubernetesAPIServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"kind":"Status","apiVersion":"v1","status":"Failure",` +
			`"message":"secrets \"regcred\" is forbidden","reason":"Forbidden","code":403}`))
	})

	auths, err := libraryImagePullSecretAuths("", "team-dev", "regcred")
	if err == nil {
		t.Fatalf("err = nil, want a forbidden error")
	}
	if auths != nil {
		t.Fatalf("auths = %+v, want nil alongside the error", auths)
	}
}

// TestLibraryImagePullSecretAuthsRefusesMalformedDockerConfig proves a
// Secret that exists but whose .dockerconfigjson does not decode is refused
// rather than silently read as no existing coverage -- the same
// don't-guess-and-destroy-coverage contract
// TestExistingImagePullSecretAuthsRefusesMalformedSecret pins for the
// subprocess path.
func TestLibraryImagePullSecretAuthsRefusesMalformedDockerConfig(t *testing.T) {
	fakeKubernetesAPIServer(t, secretFoundHandler("team-dev", "regcred", "not valid json"))

	_, err := libraryImagePullSecretAuths("", "team-dev", "regcred")
	if err == nil {
		t.Fatal("expected an error for a Secret whose .dockerconfigjson does not decode, got nil")
	}
	if strings.Contains(err.Error(), "not valid json") {
		t.Fatalf("error must not leak the decoded secret content: %v", err)
	}
}

// TestLibraryImagePullSecretAuthsHonorsContextOverride proves the
// context-name argument actually selects the kubeconfig context, the same
// way `kubectl --context X` does, rather than always following
// current-context.
func TestLibraryImagePullSecretAuthsHonorsContextOverride(t *testing.T) {
	fakeKubernetesAPIServer(t, secretFoundHandler("team-dev", "regcred", `{"auths":{"ghcr.io":{"auth":"alice:s3cret"}}}`))

	auths, err := libraryImagePullSecretAuths("test-context", "team-dev", "regcred")
	if err != nil {
		t.Fatalf("libraryImagePullSecretAuths: %v", err)
	}
	if got, want := auths["ghcr.io"].Auth, "alice:s3cret"; got != want {
		t.Fatalf("auths[ghcr.io].Auth = %q, want %q", got, want)
	}

	if _, err := libraryImagePullSecretAuths("unknown-context", "team-dev", "regcred"); err == nil {
		t.Fatalf("err = nil, want an error for an unknown context")
	}
}

// TestImagePullSecretGetArgsSharedByBothPaths locks the one argv builder
// applyImagePullSecrets traces and defaultExistingImagePullSecretAuths
// executes, so the dry-run trace can never drift from the subprocess
// execution path regardless of which execution mode is active.
func TestImagePullSecretGetArgsSharedByBothPaths(t *testing.T) {
	got := imagePullSecretGetArgs("team-dev", "my-context", "regcred")
	want := []string{"--context", "my-context", "-n", "team-dev", "get", "secret", "regcred", "-o", "json"}
	if len(got) != len(want) {
		t.Fatalf("args = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("args = %v, want %v", got, want)
		}
	}
}

func TestExecutionModeForKubectlSecretGetDefaultsToSubprocess(t *testing.T) {
	if got := ExecutionModeFor(ERunConfig{}, kubectlSecretGetExecutionOperation); got != ExecutionModeSubprocess {
		t.Fatalf("mode = %q, want %q", got, ExecutionModeSubprocess)
	}
}

func TestExecutionModeReportListsKubectlSecretGetOperation(t *testing.T) {
	report := ExecutionModeReport(ERunConfig{})
	for _, status := range report {
		if status.Operation == kubectlSecretGetExecutionOperation {
			if status.Mode != ExecutionModeSubprocess {
				t.Fatalf("mode = %q, want %q", status.Mode, ExecutionModeSubprocess)
			}
			return
		}
	}
	t.Fatalf("kubectl-secret-get not found in report: %+v", report)
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

func TestExecutionModeForKubectlPVCGetDefaultsToSubprocess(t *testing.T) {
	if got := ExecutionModeFor(ERunConfig{}, kubectlPVCGetExecutionOperation); got != ExecutionModeSubprocess {
		t.Fatalf("mode = %q, want %q", got, ExecutionModeSubprocess)
	}
}

func TestExecutionModeReportListsKubectlPVCGetOperation(t *testing.T) {
	report := ExecutionModeReport(ERunConfig{})
	for _, status := range report {
		if status.Operation == kubectlPVCGetExecutionOperation {
			if status.Mode != ExecutionModeSubprocess {
				t.Fatalf("mode = %q, want %q", status.Mode, ExecutionModeSubprocess)
			}
			return
		}
	}
	t.Fatalf("kubectl-pvc-get not found in report: %+v", report)
}
