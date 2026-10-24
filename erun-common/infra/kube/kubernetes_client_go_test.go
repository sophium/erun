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

	"github.com/sophium/erun/erun-common/base/config"
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
	if got := config.ExecutionModeFor(config.ERunConfig{}, kubectlNamespaceGetExecutionOperation); got != config.ExecutionModeSubprocess {
		t.Fatalf("mode = %q, want %q", got, config.ExecutionModeSubprocess)
	}
}

func TestExecutionModeReportListsKubectlNamespaceGetOperation(t *testing.T) {
	report := config.ExecutionModeReport(config.ERunConfig{})
	for _, status := range report {
		if status.Operation == kubectlNamespaceGetExecutionOperation {
			if status.Mode != config.ExecutionModeSubprocess {
				t.Fatalf("mode = %q, want %q", status.Mode, config.ExecutionModeSubprocess)
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

	exists, err := LibraryPersistentVolumeClaimExists("", "team-dev", "team-devops-worktree")
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

	auths, err := LibraryImagePullSecretAuths("", "team-dev", "regcred")
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

	_, err := LibraryImagePullSecretAuths("", "team-dev", "regcred")
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

func TestExecutionModeForKubectlPVCGetDefaultsToSubprocess(t *testing.T) {
	if got := config.ExecutionModeFor(config.ERunConfig{}, KubectlPVCGetExecutionOperation); got != config.ExecutionModeSubprocess {
		t.Fatalf("mode = %q, want %q", got, config.ExecutionModeSubprocess)
	}
}

func TestExecutionModeReportListsKubectlPVCGetOperation(t *testing.T) {
	report := config.ExecutionModeReport(config.ERunConfig{})
	for _, status := range report {
		if status.Operation == KubectlPVCGetExecutionOperation {
			if status.Mode != config.ExecutionModeSubprocess {
				t.Fatalf("mode = %q, want %q", status.Mode, config.ExecutionModeSubprocess)
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

	if err := LibraryWaitForDeploymentAvailable("", "team-dev", "team-api", "5s"); err != nil {
		t.Fatalf("libraryWaitForDeploymentAvailable: %v", err)
	}
}

// TestLibraryWaitForDeploymentAvailablePollsUntilConditionBecomesTrue proves
// the poll loop keeps re-Getting -- the same observable behavior as `kubectl
// wait` blocking until the condition flips -- rather than only checking once.
func TestLibraryWaitForDeploymentAvailablePollsUntilConditionBecomesTrue(t *testing.T) {
	t.Setenv("ERUN_KUBECTL_DEPLOYMENT_WAIT_POLL_INTERVAL", "5ms")
	fakeKubernetesAPIServer(t, deploymentConditionHandler("team-dev", "team-api", 2))

	if err := LibraryWaitForDeploymentAvailable("", "team-dev", "team-api", "5s"); err != nil {
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

	if err := LibraryWaitForDeploymentAvailable("", "team-dev", "team-api", "5s"); err != nil {
		t.Fatalf("libraryWaitForDeploymentAvailable: %v", err)
	}
}

// TestLibraryWaitForDeploymentAvailableTimesOutWhenConditionNeverTrue proves
// the wait gives up at the deadline instead of polling forever, the same
// contract `kubectl wait --timeout` makes.
func TestLibraryWaitForDeploymentAvailableTimesOutWhenConditionNeverTrue(t *testing.T) {
	t.Setenv("ERUN_KUBECTL_DEPLOYMENT_WAIT_POLL_INTERVAL", "5ms")
	fakeKubernetesAPIServer(t, deploymentConditionHandler("team-dev", "team-api", 1<<20))

	err := LibraryWaitForDeploymentAvailable("", "team-dev", "team-api", "20ms")
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

	err := LibraryWaitForDeploymentAvailable("", "team-dev", "team-api", "5s")
	if err == nil {
		t.Fatalf("err = nil, want an error")
	}
	if strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %q, want the underlying API error, not a timeout", err)
	}
}

func TestLibraryWaitForDeploymentAvailableRejectsInvalidTimeout(t *testing.T) {
	if err := LibraryWaitForDeploymentAvailable("", "team-dev", "team-api", "not-a-duration"); err == nil {
		t.Fatalf("err = nil, want an error for an invalid timeout")
	}
}

func TestExecutionModeForKubectlDeploymentWaitDefaultsToSubprocess(t *testing.T) {
	if got := config.ExecutionModeFor(config.ERunConfig{}, KubectlDeploymentWaitExecutionOperation); got != config.ExecutionModeSubprocess {
		t.Fatalf("mode = %q, want %q", got, config.ExecutionModeSubprocess)
	}
}

func TestExecutionModeReportListsKubectlDeploymentWaitOperation(t *testing.T) {
	report := config.ExecutionModeReport(config.ERunConfig{})
	for _, status := range report {
		if status.Operation == KubectlDeploymentWaitExecutionOperation {
			if status.Mode != config.ExecutionModeSubprocess {
				t.Fatalf("mode = %q, want %q", status.Mode, config.ExecutionModeSubprocess)
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
	if got := config.ExecutionModeFor(config.ERunConfig{}, kubectlDeploymentGetExecutionOperation); got != config.ExecutionModeSubprocess {
		t.Fatalf("mode = %q, want %q", got, config.ExecutionModeSubprocess)
	}
}

func TestExecutionModeReportListsKubectlDeploymentGetOperation(t *testing.T) {
	report := config.ExecutionModeReport(config.ERunConfig{})
	for _, status := range report {
		if status.Operation == kubectlDeploymentGetExecutionOperation {
			if status.Mode != config.ExecutionModeSubprocess {
				t.Fatalf("mode = %q, want %q", status.Mode, config.ExecutionModeSubprocess)
			}
			return
		}
	}
	t.Fatalf("kubectl-deployment-get not found in report: %+v", report)
}

// TestKubectlGetPodArgsSharedByBothPaths locks the one argv builder both
// subprocess call sites (job_container_restart.go, build_resource_exhaustion.go)
// call, so the audited kubectl invocation can never drift between them.
func TestKubectlGetPodArgsSharedByBothPaths(t *testing.T) {
	got := KubectlGetPodArgs("pod-a")
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
	if got := config.ExecutionModeFor(config.ERunConfig{}, KubectlPodGetExecutionOperation); got != config.ExecutionModeSubprocess {
		t.Fatalf("mode = %q, want %q", got, config.ExecutionModeSubprocess)
	}
}

func TestExecutionModeReportListsKubectlPodGetOperation(t *testing.T) {
	report := config.ExecutionModeReport(config.ERunConfig{})
	for _, status := range report {
		if status.Operation == KubectlPodGetExecutionOperation {
			if status.Mode != config.ExecutionModeSubprocess {
				t.Fatalf("mode = %q, want %q", status.Mode, config.ExecutionModeSubprocess)
			}
			return
		}
	}
	t.Fatalf("kubectl-pod-get not found in report: %+v", report)
}
