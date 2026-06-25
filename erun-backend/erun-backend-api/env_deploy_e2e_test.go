package backendapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
	_ "github.com/jackc/pgx/v5/stdlib"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/secrets"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	eruncommon "github.com/sophium/erun/erun-common"
)

// TestDeployEnvironmentEndToEnd exercises the live env-deploy executor against a
// real cluster (issue #680/#681): the DBOS durable workflow reads the env's
// running context + the custodied k3s token, builds an in-memory REST config that
// addresses the cluster's token-authed :6443 API server, and installs the runtime
// chart into the per-env namespace — all IN-PROCESS via the Kubernetes + Helm Go
// SDKs (no kubectl/helm subprocess, exactly as the API pod runs it). It drives the
// env's deploy status registered -> deploying -> deployed.
//
// It is opt-in (it needs a migrated Postgres, a DBOS system database, and a k3s
// cluster reachable at https://127.0.0.1:6443 that accepts the bearer token
// `deploy-verify-token` — e.g. the Lima erun-deploy cluster whose user-data is
// byte-for-byte erun's k3s bootstrap) and skips otherwise, mirroring the #676
// provisioning e2e. To keep the run light it bypasses the published OCI chart +
// the ~1GB runtime image: it points the executor at the repo's local runtime
// chart with a tiny stand-in image and disables helm's wait (EnvDeployNoWait), so
// it asserts that the release + namespace + Deployment land without blocking on
// the runtime pod's dind readiness probe a stand-in image can never satisfy. The
// published-OCI-pull path is NOT exercised here (the local chart short-circuits
// chart resolution) — that registry path is a separate verification. Run it with:
//
//	ERUN_E2E_ENV_DEPLOY=1 \
//	ERUN_E2E_ENV_DEPLOY_DATABASE_URL=postgres://erun:erun@127.0.0.1:5432/erun?sslmode=disable \
//	DBOS_SYSTEM_DATABASE_URL=postgres://erun:erun@127.0.0.1:5432/dbos_system?sslmode=disable \
//	  go test ./... -run TestDeployEnvironmentEndToEnd
const limaK3sToken = "deploy-verify-token"

func TestDeployEnvironmentEndToEnd(t *testing.T) {
	if os.Getenv("ERUN_E2E_ENV_DEPLOY") != "1" {
		t.Skip("opt-in: set ERUN_E2E_ENV_DEPLOY=1 (+ a migrated Postgres, DBOS_SYSTEM_DATABASE_URL, a k3s cluster on https://127.0.0.1:6443 accepting deploy-verify-token)")
	}
	databaseURL := os.Getenv("ERUN_E2E_ENV_DEPLOY_DATABASE_URL")
	dbosURL := os.Getenv("DBOS_SYSTEM_DATABASE_URL")
	if databaseURL == "" || dbosURL == "" {
		t.Skip("ERUN_E2E_ENV_DEPLOY_DATABASE_URL and DBOS_SYSTEM_DATABASE_URL are required")
	}

	chartPath, err := filepath.Abs(filepath.Join("..", "..", "erun-devops", "k8s", "erun-devops"))
	mustNoErr(t, err, "resolve chart path")
	if _, statErr := os.Stat(filepath.Join(chartPath, "Chart.yaml")); statErr != nil {
		t.Fatalf("runtime chart not found at %s: %v", chartPath, statErr)
	}

	key, err := secrets.GenerateKey()
	mustNoErr(t, err, "generate key")
	cipher, err := secrets.NewCipher(key)
	mustNoErr(t, err, "new cipher")
	dbosCtx, err := dbos.NewDBOSContext(context.Background(), dbos.Config{AppName: "erun-env-deploy-e2e", DatabaseURL: dbosURL})
	mustNoErr(t, err, "dbos context")
	db, err := sql.Open("pgx", databaseURL)
	mustNoErr(t, err, "open db")
	t.Cleanup(func() { _ = db.Close() })

	handler, err := NewHandler(HandlerOptions{
		TokenVerifier: TokenVerifierFunc(func(_ context.Context, token string) (Claims, error) {
			if token != e2eDevToken {
				return Claims{}, fmt.Errorf("invalid dev token")
			}
			return Claims{Issuer: "https://dev.local", Subject: "dev-user", Username: "dev"}, nil
		}),
		IdentityCache: NewIdentityResolutionCache(IdentityCacheOptions{}),
		DB:            db,
		DBDialect:     repository.DialectPostgres,
		DBOSContext:   dbosCtx,
		Cipher:        cipher,
		// Verification seams: install the repo's local runtime chart with a tiny
		// stand-in image and disable helm's wait (the chart's wait would block on
		// the dind readiness probe a stand-in image can never satisfy).
		EnvDeployChartPath: chartPath,
		EnvDeployImage:     "registry.k8s.io/pause:3.9",
		EnvDeployNoWait:    true,
	})
	mustNoErr(t, err, "new handler")
	mustNoErr(t, dbos.Launch(dbosCtx), "dbos launch")
	t.Cleanup(func() { dbos.Shutdown(dbosCtx, 5*time.Second) })

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	// The first authenticated request bootstraps the OPERATIONS tenant; read its
	// id/type back so we can seed the context + env under it via repositories.
	tenantID, tenantName := bootstrapTenant(t, srv.URL)

	envID, namespace, release := seedRunningContextAndEnv(t, db, cipher, tenantID, tenantName)
	t.Cleanup(func() { cleanupRelease(release, namespace) })

	// First deploy: the helm release does not exist yet, so this exercises the
	// in-process INSTALL branch. The env reaches deployed and the Deployment lands.
	deployAndAwaitDeployed(t, srv.URL, envID)
	assertReleaseLanded(t, release, namespace)

	// Second deploy of the SAME version: the release now exists, so this exercises
	// the UPGRADE branch — and proves a same-version re-deploy actually re-runs
	// (the prior terminal per-(env,version) workflow ID would have made this a
	// silent no-op). It must reach deployed again.
	deployAndAwaitDeployed(t, srv.URL, envID)
	assertReleaseLanded(t, release, namespace)
}

// deployAndAwaitDeployed drives one deploy through the real HTTP route (validate
// -> flip to deploying -> start the durable workflow) and polls the env until it
// reaches deployed (failing on a failed/timeout outcome).
func deployAndAwaitDeployed(t *testing.T, baseURL, envID string) {
	t.Helper()
	code, body := e2eRequest(t, baseURL, http.MethodPost, "/v1/environments/"+envID+"/deploy", nil)
	if code != http.StatusAccepted {
		t.Fatalf("deploy env: HTTP %d (want 202): %s", code, body)
	}

	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		c, b := e2eRequest(t, baseURL, http.MethodGet, "/v1/environments/"+envID, nil)
		if c != http.StatusOK {
			t.Fatalf("get env: HTTP %d: %s", c, b)
		}
		var got struct {
			DeployStatus    string `json:"deployStatus"`
			DeployedVersion string `json:"deployedVersion"`
			DeployError     string `json:"deployError"`
		}
		mustNoErr(t, json.Unmarshal([]byte(b), &got), "parse get env")
		switch got.DeployStatus {
		case "deployed":
			if got.DeployedVersion != "1.2.3" {
				t.Fatalf("deployed but version = %q, want 1.2.3: %s", got.DeployedVersion, b)
			}
			return // success
		case "failed":
			t.Fatalf("deploy failed: %s", got.DeployError)
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatal("timed out waiting for deploy to reach deployed")
}

// TestDeployEnvironmentFromPublishedOCIChart verifies the PRODUCTION chart
// resolution the Lima happy-path test skips: the executor pulling the published
// runtime chart from an OCI registry via the Helm SDK registry client (not a
// local chart dir). It packages + pushes the runtime chart to a local registry
// (registry:2 on plain HTTP), points the executor at it (no ChartPathOverride,
// RegistryPlainHTTP=true), and asserts the SDK pulls + installs it. It then
// deploys a version that was never pushed and asserts the actionable
// chart-not-found error reaches deploy_error. Opt-in, additionally gated on a
// reachable local registry; run with:
//
//	ERUN_E2E_ENV_DEPLOY=1 ERUN_E2E_OCI_REGISTRY=localhost:5001 \
//	ERUN_E2E_ENV_DEPLOY_DATABASE_URL=… DBOS_SYSTEM_DATABASE_URL=… \
//	  go test ./... -run TestDeployEnvironmentFromPublishedOCIChart
func TestDeployEnvironmentFromPublishedOCIChart(t *testing.T) {
	if os.Getenv("ERUN_E2E_ENV_DEPLOY") != "1" {
		t.Skip("opt-in: set ERUN_E2E_ENV_DEPLOY=1 (+ Postgres, DBOS, Lima k3s, and a local OCI registry)")
	}
	registry := os.Getenv("ERUN_E2E_OCI_REGISTRY")
	if registry == "" {
		t.Skip("set ERUN_E2E_OCI_REGISTRY=host:port to a plain-HTTP OCI registry (e.g. registry:2)")
	}
	databaseURL := os.Getenv("ERUN_E2E_ENV_DEPLOY_DATABASE_URL")
	dbosURL := os.Getenv("DBOS_SYSTEM_DATABASE_URL")
	if databaseURL == "" || dbosURL == "" {
		t.Skip("ERUN_E2E_ENV_DEPLOY_DATABASE_URL and DBOS_SYSTEM_DATABASE_URL are required")
	}

	chartPath, err := filepath.Abs(filepath.Join("..", "..", "erun-devops", "k8s", "erun-devops"))
	mustNoErr(t, err, "resolve chart path")
	// Setup (not the code under test): publish the chart to the local registry at
	// the version the seeded env pins, via the host helm CLI.
	helmPushChart(t, chartPath, "1.2.3", registry)

	key, err := secrets.GenerateKey()
	mustNoErr(t, err, "generate key")
	cipher, err := secrets.NewCipher(key)
	mustNoErr(t, err, "new cipher")
	dbosCtx, err := dbos.NewDBOSContext(context.Background(), dbos.Config{AppName: "erun-env-deploy-oci-e2e", DatabaseURL: dbosURL})
	mustNoErr(t, err, "dbos context")
	db, err := sql.Open("pgx", databaseURL)
	mustNoErr(t, err, "open db")
	t.Cleanup(func() { _ = db.Close() })

	handler, err := NewHandler(HandlerOptions{
		TokenVerifier: TokenVerifierFunc(func(_ context.Context, token string) (Claims, error) {
			if token != e2eDevToken {
				return Claims{}, fmt.Errorf("invalid dev token")
			}
			return Claims{Issuer: "https://dev.local", Subject: "dev-user", Username: "dev"}, nil
		}),
		IdentityCache: NewIdentityResolutionCache(IdentityCacheOptions{}),
		DB:            db,
		DBDialect:     repository.DialectPostgres,
		DBOSContext:   dbosCtx,
		Cipher:        cipher,
		// Production chart resolution: pull the published OCI chart from the local
		// registry (plain HTTP) — NO ChartPathOverride. Stand-in image + no wait
		// keep the run light.
		RuntimeRegistry:            registry,
		EnvDeployRegistryPlainHTTP: true,
		EnvDeployImage:             "registry.k8s.io/pause:3.9",
		EnvDeployNoWait:            true,
	})
	mustNoErr(t, err, "new handler")
	mustNoErr(t, dbos.Launch(dbosCtx), "dbos launch")
	t.Cleanup(func() { dbos.Shutdown(dbosCtx, 5*time.Second) })

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	tenantID, tenantName := bootstrapTenant(t, srv.URL)
	envID, namespace, release := seedRunningContextAndEnv(t, db, cipher, tenantID, tenantName)
	t.Cleanup(func() { cleanupRelease(release, namespace) })

	// Pull the published chart at 1.2.3 from the registry via the Helm SDK and
	// install it — the production chart-resolution path.
	deployAndAwaitDeployed(t, srv.URL, envID)
	assertReleaseLanded(t, release, namespace)

	// A version that was never pushed must fail with the actionable error (not a
	// bare helm message), recorded in deploy_error.
	deployAndAwaitFailed(t, srv.URL, envID, "9.9.9", "erun push")
}

// helmPushChart packages the chart at version and pushes it to a plain-HTTP OCI
// registry via the host helm CLI. This is test setup — it primes the registry
// the executor's in-process SDK then pulls from; it is not the code under test.
func helmPushChart(t *testing.T, chartDir, version, registry string) {
	t.Helper()
	dest := t.TempDir()
	if out, err := exec.Command("helm", "package", chartDir, "--version", version, "--app-version", version, "--destination", dest).CombinedOutput(); err != nil {
		t.Fatalf("helm package: %v: %s", err, strings.TrimSpace(string(out)))
	}
	tgz := filepath.Join(dest, "erun-devops-"+version+".tgz")
	if out, err := exec.Command("helm", "push", tgz, "oci://"+registry+"/charts", "--plain-http").CombinedOutput(); err != nil {
		t.Fatalf("helm push: %v: %s", err, strings.TrimSpace(string(out)))
	}
}

// deployAndAwaitFailed drives a deploy at versionOverride and polls the env until
// it reaches failed, asserting the recorded deploy_error contains wantReason.
func deployAndAwaitFailed(t *testing.T, baseURL, envID, versionOverride, wantReason string) {
	t.Helper()
	code, body := e2eRequest(t, baseURL, http.MethodPost, "/v1/environments/"+envID+"/deploy",
		map[string]any{"version": versionOverride})
	if code != http.StatusAccepted {
		t.Fatalf("deploy env: HTTP %d (want 202): %s", code, body)
	}
	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		c, b := e2eRequest(t, baseURL, http.MethodGet, "/v1/environments/"+envID, nil)
		if c != http.StatusOK {
			t.Fatalf("get env: HTTP %d: %s", c, b)
		}
		var got struct {
			DeployStatus string `json:"deployStatus"`
			DeployError  string `json:"deployError"`
		}
		mustNoErr(t, json.Unmarshal([]byte(b), &got), "parse get env")
		switch got.DeployStatus {
		case "failed":
			if !strings.Contains(got.DeployError, wantReason) {
				t.Fatalf("deploy_error = %q, want it to contain %q", got.DeployError, wantReason)
			}
			return
		case "deployed":
			t.Fatalf("expected the unpushed version %q to fail, but it deployed", versionOverride)
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatal("timed out waiting for the unpushed-version deploy to reach failed")
}

// bootstrapTenant issues one authenticated request (which bootstraps the
// OPERATIONS tenant on the empty database) and returns the tenant's id + name.
func bootstrapTenant(t *testing.T, baseURL string) (string, string) {
	t.Helper()
	code, body := e2eRequest(t, baseURL, http.MethodGet, "/v1/config", nil)
	if code != http.StatusOK {
		t.Fatalf("bootstrap via GET /v1/config: HTTP %d: %s", code, body)
	}
	var config struct {
		Tenant struct {
			TenantID string `json:"tenantId"`
			Name     string `json:"name"`
		} `json:"tenant"`
	}
	mustNoErr(t, json.Unmarshal([]byte(body), &config), "parse config")
	if config.Tenant.TenantID == "" {
		t.Fatalf("bootstrap produced no tenant: %s", body)
	}
	return config.Tenant.TenantID, config.Tenant.Name
}

// seedRunningContextAndEnv persists, under the bootstrapped tenant, a context in
// the running state (public_ip 127.0.0.1 + the custodied Lima token) and a
// runtime env linked to it — the state a successful provisioning run would have
// produced. It returns the env id and the namespace + release the deploy targets.
func seedRunningContextAndEnv(t *testing.T, db *sql.DB, cipher *secrets.Cipher, tenantID, tenantName string) (string, string, string) {
	t.Helper()
	txManager := repository.NewTxManager(db, repository.DialectPostgres)
	contexts := repository.NewContextRepository(txManager)
	credentials := repository.NewContextCredentialRepository(txManager, cipher)
	environments := repository.NewEnvironmentRepository(txManager)
	// The OPERATIONS tenant uses the erun_operations role, which the RLS policies
	// allow across tenants; the seeds land under the bootstrapped tenant id.
	sc := security.WithContext(context.Background(), security.Context{
		TenantID:   tenantID,
		TenantType: string(model.TenantTypeOperations),
	})

	cloudContext, err := contexts.Create(sc, model.Context{
		Name:              "primary",
		Provider:          eruncommon.CloudProviderAWS,
		Region:            "eu-west-2",
		KubernetesContext: "primary",
	})
	mustNoErr(t, err, "seed context")
	mustNoErr(t, contexts.UpdateProvisioningResult(sc, cloudContext.ContextID, "running", "i-lima", "127.0.0.1", ""), "seed running status")
	mustNoErr(t, credentials.Set(sc, cloudContext.ContextID, limaK3sToken), "custody token")

	env, err := environments.Create(sc, model.Environment{
		Name:           "prod",
		Type:           model.EnvironmentTypeRuntime,
		ContextID:      cloudContext.ContextID,
		RuntimeVersion: "1.2.3",
	})
	mustNoErr(t, err, "seed environment")

	namespace := eruncommon.KubernetesNamespaceName(tenantName, env.Name)
	release := eruncommon.RuntimeReleaseName(tenantName)
	return env.EnvironmentID, namespace, release
}

// limaRestConfig addresses the Lima cluster the way the executor does — the
// same in-memory REST config (https://127.0.0.1:6443, the static token,
// TLS-verify skipped) — so the test-side checks talk to the cluster via
// client-go, with no kubeconfig file and no kubectl/helm host binaries.
func limaRestConfig() *rest.Config {
	return &rest.Config{
		Host:            "https://127.0.0.1:6443",
		BearerToken:     limaK3sToken,
		TLSClientConfig: rest.TLSClientConfig{Insecure: true},
	}
}

// assertReleaseLanded confirms the runtime Deployment object the deploy created
// exists on the cluster — the deploy reached a real cluster and rendered the
// chart in-process via the Helm SDK, not just flipped a database row.
func assertReleaseLanded(t *testing.T, release, namespace string) {
	t.Helper()
	clientset, err := kubernetes.NewForConfig(limaRestConfig())
	mustNoErr(t, err, "build kube client")
	if _, err := clientset.AppsV1().Deployments(namespace).Get(context.Background(), release, metav1.GetOptions{}); err != nil {
		t.Fatalf("deployment %s/%s not found after deploy: %v", namespace, release, err)
	}
}

// cleanupRelease deletes the namespace the deploy created (cascading the release
// and its objects) so the cluster returns to a clean state for re-runs
// (verification-gate hygiene), via client-go — no host binaries.
func cleanupRelease(_, namespace string) {
	clientset, err := kubernetes.NewForConfig(limaRestConfig())
	if err != nil {
		return
	}
	err = clientset.CoreV1().Namespaces().Delete(context.Background(), namespace, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return
	}
}
