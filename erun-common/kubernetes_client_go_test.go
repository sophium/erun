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

func deploymentFoundHandler(namespace, name string) http.HandlerFunc {
	path := "/apis/apps/v1/namespaces/" + namespace + "/deployments/" + name
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"kind":"Status","status":"Failure","reason":"NotFound"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"kind":"Deployment","apiVersion":"apps/v1","metadata":{"name":"` + name + `","namespace":"` + namespace + `"}}`))
	}
}

func deploymentNotFoundHandler(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"kind":"Status","apiVersion":"v1","status":"Failure",` +
			`"message":"deployments.apps \"` + name + `\" not found","reason":"NotFound","code":404}`))
	}
}

// TestLibraryDeploymentPresentMatchesSubprocessObservableResult pins the same
// equivalence property the namespace/PVC tests do: the library path's
// returned bool must agree with what defaultDeploymentPresent derives from
// kubectl's exit code (found -> exit 0, not found -> "NotFound" stderr).
func TestLibraryDeploymentPresentMatchesSubprocessObservableResult(t *testing.T) {
	fakeKubernetesAPIServer(t, deploymentFoundHandler("team-dev", "team-api"))

	present, err := libraryDeploymentPresent("", "team-dev", "team-api")
	if err != nil {
		t.Fatalf("libraryDeploymentPresent: %v", err)
	}
	if !present {
		t.Fatalf("present = false, want true")
	}
}

func TestLibraryDeploymentPresentReportsNotFoundAsAbsentNotError(t *testing.T) {
	fakeKubernetesAPIServer(t, deploymentNotFoundHandler("team-api"))

	present, err := libraryDeploymentPresent("", "team-dev", "team-api")
	if err != nil {
		t.Fatalf("libraryDeploymentPresent: %v", err)
	}
	if present {
		t.Fatalf("present = true, want false")
	}
}

// TestLibraryDeploymentPresentPropagatesOtherErrors proves a refusal distinct
// from NotFound (Forbidden here) surfaces as an error rather than being
// folded into "does not exist" — the same distinction defaultDeploymentPresent's
// substring check draws against kubectl's own stderr.
func TestLibraryDeploymentPresentPropagatesOtherErrors(t *testing.T) {
	fakeKubernetesAPIServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"kind":"Status","apiVersion":"v1","status":"Failure",` +
			`"message":"deployments.apps \"team-api\" is forbidden","reason":"Forbidden","code":403}`))
	})

	present, err := libraryDeploymentPresent("", "team-dev", "team-api")
	if err == nil {
		t.Fatalf("err = nil, want a forbidden error")
	}
	if present {
		t.Fatalf("present = true, want false")
	}
}

// TestLibraryDeploymentPresentHonorsContextOverride proves the context-name
// argument actually selects the kubeconfig context, the same way `kubectl
// --context X` does, rather than always following current-context.
func TestLibraryDeploymentPresentHonorsContextOverride(t *testing.T) {
	fakeKubernetesAPIServer(t, deploymentFoundHandler("team-dev", "team-api"))

	present, err := libraryDeploymentPresent("test-context", "team-dev", "team-api")
	if err != nil {
		t.Fatalf("libraryDeploymentPresent: %v", err)
	}
	if !present {
		t.Fatalf("present = false, want true")
	}

	if _, err := libraryDeploymentPresent("unknown-context", "team-dev", "team-api"); err == nil {
		t.Fatalf("err = nil, want an error for an unknown context")
	}
}

// deploymentConditionHandler serves a Deployment Get whose Available
// condition is False for the first failCount requests, then True -- so tests
// can exercise libraryWaitForDeploymentAvailable's poll loop instead of only
// its immediate-success path.
func deploymentConditionHandler(namespace, name string, failCount int) http.HandlerFunc {
	path := "/apis/apps/v1/namespaces/" + namespace + "/deployments/" + name
	calls := 0
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"kind":"Status","status":"Failure","reason":"NotFound"}`))
			return
		}
		calls++
		status := "False"
		if calls > failCount {
			status = "True"
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"kind":"Deployment","apiVersion":"apps/v1","metadata":{"name":"` + name + `","namespace":"` + namespace + `"},` +
			`"status":{"conditions":[{"type":"Available","status":"` + status + `"}]}}`))
	}
}

// TestLibraryWaitForDeploymentAvailableReturnsImmediatelyWhenAlreadyAvailable
// proves the common case -- the Deployment is already Available on the first
// Get -- returns without polling again.
func TestLibraryWaitForDeploymentAvailableReturnsImmediatelyWhenAlreadyAvailable(t *testing.T) {
	fakeKubernetesAPIServer(t, deploymentConditionHandler("team-dev", "team-api", 0))

	if err := libraryWaitForDeploymentAvailable("", "team-dev", "team-api", "5s"); err != nil {
		t.Fatalf("libraryWaitForDeploymentAvailable: %v", err)
	}
}

// TestLibraryWaitForDeploymentAvailablePollsUntilConditionBecomesTrue proves
// the poll loop keeps re-Getting -- the same observable behavior as `kubectl
// wait` blocking until the condition flips -- rather than only checking once.
func TestLibraryWaitForDeploymentAvailablePollsUntilConditionBecomesTrue(t *testing.T) {
	t.Setenv("ERUN_KUBECTL_DEPLOYMENT_WAIT_POLL_INTERVAL", "5ms")
	fakeKubernetesAPIServer(t, deploymentConditionHandler("team-dev", "team-api", 2))

	if err := libraryWaitForDeploymentAvailable("", "team-dev", "team-api", "5s"); err != nil {
		t.Fatalf("libraryWaitForDeploymentAvailable: %v", err)
	}
}

// TestLibraryWaitForDeploymentAvailableToleratesNotFoundUntilCreated proves
// the wait survives the Deployment not existing yet at the first poll --
// matching `kubectl wait`'s tolerance for a resource that has not been
// created yet -- rather than failing outright on the first NotFound.
func TestLibraryWaitForDeploymentAvailableToleratesNotFoundUntilCreated(t *testing.T) {
	t.Setenv("ERUN_KUBECTL_DEPLOYMENT_WAIT_POLL_INTERVAL", "5ms")
	calls := 0
	fakeKubernetesAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls <= 2 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"kind":"Status","apiVersion":"v1","status":"Failure",` +
				`"message":"deployments.apps \"team-api\" not found","reason":"NotFound","code":404}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"kind":"Deployment","apiVersion":"apps/v1","metadata":{"name":"team-api","namespace":"team-dev"},` +
			`"status":{"conditions":[{"type":"Available","status":"True"}]}}`))
	})

	if err := libraryWaitForDeploymentAvailable("", "team-dev", "team-api", "5s"); err != nil {
		t.Fatalf("libraryWaitForDeploymentAvailable: %v", err)
	}
}

// TestLibraryWaitForDeploymentAvailableTimesOutWhenConditionNeverTrue proves
// the wait gives up at the deadline instead of polling forever, the same
// contract `kubectl wait --timeout` makes.
func TestLibraryWaitForDeploymentAvailableTimesOutWhenConditionNeverTrue(t *testing.T) {
	t.Setenv("ERUN_KUBECTL_DEPLOYMENT_WAIT_POLL_INTERVAL", "5ms")
	fakeKubernetesAPIServer(t, deploymentConditionHandler("team-dev", "team-api", 1<<20))

	err := libraryWaitForDeploymentAvailable("", "team-dev", "team-api", "20ms")
	if err == nil {
		t.Fatalf("err = nil, want a timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %q, want it to mention a timeout", err)
	}
}

// TestLibraryWaitForDeploymentAvailablePropagatesOtherErrors proves a
// non-NotFound API error is not swallowed into a plain timeout the way a
// bare "condition never became true" loop would.
func TestLibraryWaitForDeploymentAvailablePropagatesOtherErrors(t *testing.T) {
	fakeKubernetesAPIServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"kind":"Status","status":"Failure","reason":"InternalError"}`))
	})

	err := libraryWaitForDeploymentAvailable("", "team-dev", "team-api", "5s")
	if err == nil {
		t.Fatalf("err = nil, want an error")
	}
	if strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %q, want the underlying API error, not a timeout", err)
	}
}

func TestLibraryWaitForDeploymentAvailableRejectsInvalidTimeout(t *testing.T) {
	if err := libraryWaitForDeploymentAvailable("", "team-dev", "team-api", "not-a-duration"); err == nil {
		t.Fatalf("err = nil, want an error for an invalid timeout")
	}
}

func TestExecutionModeForKubectlDeploymentWaitDefaultsToSubprocess(t *testing.T) {
	if got := ExecutionModeFor(ERunConfig{}, kubectlDeploymentWaitExecutionOperation); got != ExecutionModeSubprocess {
		t.Fatalf("mode = %q, want %q", got, ExecutionModeSubprocess)
	}
}

func TestExecutionModeReportListsKubectlDeploymentWaitOperation(t *testing.T) {
	report := ExecutionModeReport(ERunConfig{})
	for _, status := range report {
		if status.Operation == kubectlDeploymentWaitExecutionOperation {
			if status.Mode != ExecutionModeSubprocess {
				t.Fatalf("mode = %q, want %q", status.Mode, ExecutionModeSubprocess)
			}
			return
		}
	}
	t.Fatalf("kubectl-deployment-wait not found in report: %+v", report)
}

// TestKubectlGetDeploymentArgsSharedByBothPaths locks the one argv builder
// ensureAPIPortForward (erun-cli) traces and defaultDeploymentPresent
// executes, so the dry-run trace can never drift from the subprocess
// execution path regardless of which execution mode is active.
func TestKubectlGetDeploymentArgsSharedByBothPaths(t *testing.T) {
	got := KubectlGetDeploymentArgs("my-context", "team-dev", "team-api")
	want := []string{"--context", "my-context", "--namespace", "team-dev", "get", "deployment", "team-api", "-o", "name"}
	if len(got) != len(want) {
		t.Fatalf("args = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("args = %v, want %v", got, want)
		}
	}
}

func TestKubectlGetDeploymentArgsOmitsContextAndNamespaceFlagsWhenUnset(t *testing.T) {
	got := KubectlGetDeploymentArgs("", "", "team-api")
	want := []string{"get", "deployment", "team-api", "-o", "name"}
	if len(got) != len(want) {
		t.Fatalf("args = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("args = %v, want %v", got, want)
		}
	}
}

func TestExecutionModeForKubectlDeploymentGetDefaultsToSubprocess(t *testing.T) {
	if got := ExecutionModeFor(ERunConfig{}, kubectlDeploymentGetExecutionOperation); got != ExecutionModeSubprocess {
		t.Fatalf("mode = %q, want %q", got, ExecutionModeSubprocess)
	}
}

func TestExecutionModeReportListsKubectlDeploymentGetOperation(t *testing.T) {
	report := ExecutionModeReport(ERunConfig{})
	for _, status := range report {
		if status.Operation == kubectlDeploymentGetExecutionOperation {
			if status.Mode != ExecutionModeSubprocess {
				t.Fatalf("mode = %q, want %q", status.Mode, ExecutionModeSubprocess)
			}
			return
		}
	}
	t.Fatalf("kubectl-deployment-get not found in report: %+v", report)
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

	pod, err := libraryGetPod("pod-a")
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

	if _, err := libraryGetPod("pod-a"); err == nil {
		t.Fatal("expected an error for a pod that does not exist, got nil")
	}
	if _, _, _, _, ok := libraryJobSupervisorContainerRestart("pod-a", "erun-devops"); ok {
		t.Fatal("expected ok=false when the pod cannot be read, not a guessed answer")
	}
	if _, found := libraryDockerBuildContainerMemoryLimit("pod-a", "erun-dind"); found {
		t.Fatal("expected found=false when the pod cannot be read, not a guessed answer")
	}
}

// TestKubectlGetPodArgsSharedByBothPaths locks the one argv builder both
// subprocess call sites (job_container_restart.go, build_resource_exhaustion.go)
// call, so the audited kubectl invocation can never drift between them.
func TestKubectlGetPodArgsSharedByBothPaths(t *testing.T) {
	got := kubectlGetPodArgs("pod-a")
	want := []string{"get", "pod", "pod-a", "-o", "json"}
	if len(got) != len(want) {
		t.Fatalf("args = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("args = %v, want %v", got, want)
		}
	}
}

func TestExecutionModeForKubectlPodGetDefaultsToSubprocess(t *testing.T) {
	if got := ExecutionModeFor(ERunConfig{}, kubectlPodGetExecutionOperation); got != ExecutionModeSubprocess {
		t.Fatalf("mode = %q, want %q", got, ExecutionModeSubprocess)
	}
}

func TestExecutionModeReportListsKubectlPodGetOperation(t *testing.T) {
	report := ExecutionModeReport(ERunConfig{})
	for _, status := range report {
		if status.Operation == kubectlPodGetExecutionOperation {
			if status.Mode != ExecutionModeSubprocess {
				t.Fatalf("mode = %q, want %q", status.Mode, ExecutionModeSubprocess)
			}
			return
		}
	}
	t.Fatalf("kubectl-pod-get not found in report: %+v", report)
}
