package integration

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func testDeployDryRunUnaffectedByKubectlSecretGetLibraryExecutionMode(t *testing.T) {
	// Library mode must not change the dry-run trace: the secret is never read on a dry run.
	setup := env.New(t)
	fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
	envConfigPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
	existing, err := os.ReadFile(envConfigPath)
	if err != nil {
		t.Fatalf("read env config: %v", err)
	}
	mustWriteFile(t, envConfigPath, string(existing)+"imagepullsecrets:\n    - ghcr-pull\n")
	seedExecutionMode(t, setup, "kubectl-secret-get", "library")
	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=team-devops:1.0.0")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_remote_env_image_pull_secrets", normalize.Apply(result.Combined))
}

func testDeployRealRunKubectlSecretGetLibraryExecutionModeReachesTheAPIServerDirectly(t *testing.T) {
	// The kubectl stub fails only for "get secret ... -o json", so a subprocess
	// fallback surfaces as a recognizable failure.
	setup := env.New(t)
	fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
	envConfigPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
	existing, err := os.ReadFile(envConfigPath)
	if err != nil {
		t.Fatalf("read env config: %v", err)
	}
	mustWriteFile(t, envConfigPath, string(existing)+"imagepullsecrets:\n    - ecr-pull\n")
	seedExecutionMode(t, setup, "kubectl-secret-get", "library")

	dockerCfgDir, freshEncoded := seedInlineDockerAuth(t, setup, "registry.example", "AWS:fresh-token")

	existingSecretJSON := fmt.Sprintf(`{"kind":"Secret","apiVersion":"v1","metadata":{"name":"ecr-pull","namespace":"team-dev"},"data":{".dockerconfigjson":%q}}`,
		base64.StdEncoding.EncodeToString([]byte(`{"auths":{"legacy-other.example":{"auth":"preserved-value"}}}`)))

	var apiHits atomic.Int64
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiHits.Add(1)
		if r.URL.Path != "/api/v1/namespaces/team-dev/secrets/ecr-pull" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, existingSecretJSON)
	}))
	defer apiServer.Close()
	writeFakeAPIServerKubeconfig(t, setup, apiServer.URL)

	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryWithScript(t, stubs, "kubectl", strings.Join([]string{
		`case "$*" in`,
		`  *"get secret"*) printf '%s\n' "fell through to the kubectl subprocess for the image pull secret read" >&2; exit 1 ;;`,
		`  *) exit 0 ;;`,
		`esac`,
	}, "\n"))
	fixture.StubBinary(t, stubs, "helm", "")
	fixture.StubBinary(t, stubs, "docker", "")
	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:1.0.0", "DOCKER_CONFIG="+dockerCfgDir)
	envVars = append(envVars, fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if strings.Contains(result.Combined, "fell through to the kubectl subprocess") {
		t.Fatalf("image pull secret read used the kubectl subprocess despite library execution mode:\n%s", result.Combined)
	}
	if got := apiHits.Load(); got == 0 {
		t.Fatalf("fake API server received no requests; library path never ran")
	}
	if strings.Contains(result.Combined, "fresh-token") || strings.Contains(result.Combined, freshEncoded) {
		t.Fatalf("the resolved credential must never appear in trace output: %s", result.Combined)
	}
}

func testDeployDryRunUnaffectedByKubectlSecretApplyLibraryExecutionMode(t *testing.T) {
	// Library mode must not change the dry-run trace: the rendered command is the audit contract.
	setup := env.New(t)
	fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
	envConfigPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
	existing, err := os.ReadFile(envConfigPath)
	if err != nil {
		t.Fatalf("read env config: %v", err)
	}
	mustWriteFile(t, envConfigPath, string(existing)+"imagepullsecrets:\n    - ghcr-pull\n")
	seedExecutionMode(t, setup, "kubectl-secret-apply", "library")
	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=team-devops:1.0.0")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_remote_env_image_pull_secrets", normalize.Apply(result.Combined))
}

func testDeployRealRunKubectlSecretApplyLibraryExecutionModeClientSideAppliesViaTheAPIServer(t *testing.T) {
	// The library path must reproduce kubectl's client-side apply (strategic-merge
	// PATCH with last-applied annotation), not SSA, or objects diverge from kubectl's.
	// The stub fails only for "apply -f -", so a subprocess fallback is caught.
	setup := env.New(t)
	fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
	envConfigPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
	existing, err := os.ReadFile(envConfigPath)
	if err != nil {
		t.Fatalf("read env config: %v", err)
	}
	mustWriteFile(t, envConfigPath, string(existing)+"imagepullsecrets:\n    - ecr-pull\n")
	seedExecutionMode(t, setup, "kubectl-secret-apply", "library")

	dockerCfgDir, freshEncoded := seedInlineDockerAuth(t, setup, "registry.example", "AWS:fresh-token")

	liveSecretJSON := `{"kind":"Secret","apiVersion":"v1","metadata":{"name":"ecr-pull","namespace":"team-dev"},"type":"kubernetes.io/dockerconfigjson"}`

	var patchContentType, patchBody atomic.Value
	patchContentType.Store("")
	patchBody.Store("")
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/namespaces/team-dev/secrets/ecr-pull" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method == http.MethodPatch {
			body, _ := io.ReadAll(r.Body)
			patchContentType.Store(r.Header.Get("Content-Type"))
			patchBody.Store(string(body))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, liveSecretJSON)
	}))
	defer apiServer.Close()
	writeFakeAPIServerKubeconfig(t, setup, apiServer.URL)

	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryWithScript(t, stubs, "kubectl", strings.Join([]string{
		`case "$*" in`,
		`  *"get secret"*) echo 'Error from server (NotFound): secrets "ecr-pull" not found' >&2; exit 1 ;;`,
		`  *"apply -f -"*) printf '%s\n' "fell through to the kubectl subprocess for the image pull secret apply" >&2; exit 1 ;;`,
		`  *) exit 0 ;;`,
		`esac`,
	}, "\n"))
	fixture.StubBinary(t, stubs, "helm", "")
	fixture.StubBinary(t, stubs, "docker", "")
	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:1.0.0", "DOCKER_CONFIG="+dockerCfgDir)
	envVars = append(envVars, fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if strings.Contains(result.Combined, "fell through to the kubectl subprocess") {
		t.Fatalf("image pull secret apply used the kubectl subprocess despite library execution mode:\n%s", result.Combined)
	}
	gotContentType, _ := patchContentType.Load().(string)
	if gotContentType != "application/strategic-merge-patch+json" {
		t.Fatalf("apply must be kubectl's client-side three-way strategic merge, got content type %q", gotContentType)
	}
	gotBody, _ := patchBody.Load().(string)
	if !strings.Contains(gotBody, "kubectl.kubernetes.io/last-applied-configuration") {
		t.Fatalf("the patch must record the last-applied configuration kubectl's next apply merges against, got: %s", gotBody)
	}
	if strings.Contains(result.Combined, "fresh-token") || strings.Contains(result.Combined, freshEncoded) {
		t.Fatalf("the resolved credential must never appear in trace output: %s", result.Combined)
	}
}

func testDeployRealRunNewWorktreeVolumeAnnouncesTheAdoption(t *testing.T) {
	// A claim missing from the cluster is a decision input, so kubectl reports it NotFound.
	setup := env.New(t)
	fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinary(t, stubs, "helm", "")
	// The probe override marks erun-devops published so the runtime chart ladder confirms it.
	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:1.0.0")
	envVars = append(envVars, fixture.StubKubectlWorktreeClaim(t, stubs, fixture.KubectlWorktreeClaimStubSpec{
		ClaimName: "team-devops-worktree",
		Stderr:    `Error from server (NotFound): persistentvolumeclaims "team-devops-worktree" not found`,
		ExitCode:  1,
	})...)
	envVars = append(envVars, fixture.StubEnv(stubs, "helm")...)
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/real_run_new_worktree_volume_announces_the_adoption", normalize.Apply(result.Combined))
}

func testDeployRealRunUnreadableWorktreeVolumeStillAnnounces(t *testing.T) {
	// A failed claim read is "unknown", so the notice prints with the error traced.
	setup := env.New(t)
	fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinary(t, stubs, "helm", "")
	// The probe override marks erun-devops published so the runtime chart ladder confirms it.
	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:1.0.0")
	envVars = append(envVars, fixture.StubKubectlWorktreeClaim(t, stubs, fixture.KubectlWorktreeClaimStubSpec{
		ClaimName: "team-devops-worktree",
		Stderr:    "error: You must be logged in to the server (Unauthorized)",
		ExitCode:  1,
	})...)
	envVars = append(envVars, fixture.StubEnv(stubs, "helm")...)
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/real_run_unreadable_worktree_volume_still_announces", normalize.Apply(result.Combined))
}

func testDeployDryRunUnaffectedByKubectlPVCGetLibraryExecutionMode(t *testing.T) {
	// Library mode must not change the dry-run trace: the claim is never read on a dry run.
	setup := env.New(t)
	fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
	seedExecutionMode(t, setup, "kubectl-pvc-get", "library")
	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=registry.example/test/team-devops:1.0.0,ghcr.io/sophium/erun-devops:1.0.0")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_remote_env_prefers_tenant_published_runtime_chart", normalize.Apply(result.Combined))
}

func testDeployRealRunKubectlPVCGetLibraryExecutionModeReachesTheAPIServerDirectly(t *testing.T) {
	// The kubectl stub fails only for "get pvc ... -o name", so a subprocess
	// fallback surfaces as a recognizable failure.
	setup := env.New(t)
	fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
	seedExecutionMode(t, setup, "kubectl-pvc-get", "library")

	var apiHits atomic.Int64
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiHits.Add(1)
		if r.URL.Path != "/api/v1/namespaces/team-dev/persistentvolumeclaims/team-devops-worktree" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"kind":"PersistentVolumeClaim","apiVersion":"v1","metadata":{"name":"team-devops-worktree","namespace":"team-dev"}}`)
	}))
	defer apiServer.Close()
	writeFakeAPIServerKubeconfig(t, setup, apiServer.URL)

	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryWithScript(t, stubs, "kubectl", strings.Join([]string{
		`case "$*" in`,
		`  *"get pvc"*) printf '%s\n' "fell through to the kubectl subprocess for the worktree claim check" >&2; exit 1 ;;`,
		`  *) exit 0 ;;`,
		`esac`,
	}, "\n"))
	fixture.StubBinary(t, stubs, "helm", "")
	fixture.StubBinary(t, stubs, "docker", "")
	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:1.0.0")
	envVars = append(envVars, fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if strings.Contains(result.Combined, "fell through to the kubectl subprocess") {
		t.Fatalf("worktree claim check used the kubectl subprocess despite library execution mode:\n%s", result.Combined)
	}
	if got := apiHits.Load(); got == 0 {
		t.Fatalf("fake API server received no requests; library path never ran")
	}
}

func testDeployDryRunUnaffectedByKubectlNamespaceGetLibraryExecutionMode(t *testing.T) {
	// Library mode must not change the dry-run trace: the namespace is never read on a dry run.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	seedExecutionMode(t, setup, "kubectl-namespace-get", "library")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_from_devops_cwd", normalize.Apply(result.Combined))
}

func testDeployRealRunKubectlNamespaceGetLibraryExecutionModeReachesTheAPIServerDirectly(t *testing.T) {
	// The kubectl stub fails only for "get namespace", so a subprocess fallback
	// surfaces as a recognizable failure.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	seedExecutionMode(t, setup, "kubectl-namespace-get", "library")

	var apiHits atomic.Int64
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiHits.Add(1)
		if r.URL.Path != "/api/v1/namespaces/team-dev" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"kind":"Namespace","apiVersion":"v1","metadata":{"name":"team-dev"}}`)
	}))
	defer apiServer.Close()
	writeFakeAPIServerKubeconfig(t, setup, apiServer.URL)

	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryWithScript(t, stubs, "kubectl", strings.Join([]string{
		`case "$*" in`,
		`  *"get namespace"*) printf '%s\n' "fell through to the kubectl subprocess for the namespace check" >&2; exit 1 ;;`,
		`  *) exit 0 ;;`,
		`esac`,
	}, "\n"))
	fixture.StubBinary(t, stubs, "helm", "")
	fixture.StubBinary(t, stubs, "docker", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if strings.Contains(result.Combined, "fell through to the kubectl subprocess") {
		t.Fatalf("namespace check used the kubectl subprocess despite library execution mode:\n%s", result.Combined)
	}
	if got := apiHits.Load(); got == 0 {
		t.Fatalf("fake API server received no requests; library path never ran")
	}
}

func writeFakeAPIServerKubeconfig(t *testing.T, setup env.Setup, serverURL string) {
	t.Helper()
	kubeDir := filepath.Join(setup.Home, ".kube")
	if err := os.MkdirAll(kubeDir, 0o700); err != nil {
		t.Fatalf("mkdir .kube: %v", err)
	}
	kubeconfig := "apiVersion: v1\n" +
		"kind: Config\n" +
		"clusters:\n" +
		"  - name: test-cluster\n" +
		"    cluster:\n" +
		"      server: " + serverURL + "\n" +
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
	if err := os.WriteFile(filepath.Join(kubeDir, "config"), []byte(kubeconfig), 0o600); err != nil {
		t.Fatalf("write kubeconfig: %v", err)
	}
}
