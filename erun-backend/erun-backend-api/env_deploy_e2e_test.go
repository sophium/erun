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

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/secrets"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	eruncommon "github.com/sophium/erun/erun-common"
)

// TestDeployEnvironmentEndToEnd exercises the live env-deploy executor against a
// real cluster (issue #680): the DBOS durable workflow reads the env's running
// context + the custodied k3s token, materializes a kube-context that addresses
// the cluster's token-authed :6443 API server, and helm-installs the runtime
// chart into the per-env namespace, driving the env's deploy status
// registered -> deploying -> deployed.
//
// It is opt-in (it needs a migrated Postgres, a DBOS system database, and a k3s
// cluster reachable at https://127.0.0.1:6443 that accepts the bearer token
// `deploy-verify-token` — e.g. the Lima erun-deploy cluster whose user-data is
// byte-for-byte erun's k3s bootstrap) and skips otherwise, mirroring the #676
// provisioning e2e. To keep the run light it bypasses the published OCI chart +
// the ~1GB runtime image: it points the executor at the repo's local runtime
// chart with a tiny stand-in image and injects a no-wait helm deployer, so it
// asserts that the release + namespace + Deployment land without blocking on the
// runtime pod's dind readiness probe (the chart hardcodes --wait). Run it with:
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

	// Isolate the kubeconfig the executor's kubectl writes + the injected helm
	// deployer read, so the run never touches the developer's ~/.kube/config.
	kubeconfig := filepath.Join(t.TempDir(), "kubeconfig")
	t.Setenv("KUBECONFIG", kubeconfig)

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
		// stand-in image via a no-wait deployer (the chart's --wait would block on
		// the dind readiness probe a stand-in image can never satisfy).
		EnvDeployChartPath: chartPath,
		EnvDeployImage:     "registry.k8s.io/pause:3.9",
		EnvHelmDeployer:    noWaitHelmDeployer(t),
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

	// Drive the deploy through the real HTTP route (validate -> flip to deploying
	// -> start the durable workflow), then poll the env to deployed.
	code, body := e2eRequest(t, srv.URL, http.MethodPost, "/v1/environments/"+envID+"/deploy", nil)
	if code != http.StatusAccepted {
		t.Fatalf("deploy env: HTTP %d (want 202): %s", code, body)
	}

	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		c, b := e2eRequest(t, srv.URL, http.MethodGet, "/v1/environments/"+envID, nil)
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
			assertReleaseLanded(t, release, namespace)
			return // success
		case "failed":
			t.Fatalf("deploy failed: %s", got.DeployError)
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatal("timed out waiting for deploy to reach deployed")
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

// noWaitHelmDeployer is a HelmChartDeployerFunc that installs the chart the
// executor assembled (local chart dir + stand-in image) WITHOUT helm's --wait,
// so the deploy returns as soon as the objects are applied rather than blocking
// on the runtime pod going Ready (the real runtime pod needs dind + the ~1GB
// image, out of scope for this mechanism check). It threads the kube-context the
// executor wrote into $KUBECONFIG and overrides all three pod images so they
// schedule.
func noWaitHelmDeployer(t *testing.T) eruncommon.HelmChartDeployerFunc {
	return func(params eruncommon.HelmDeployParams) error {
		args := []string{
			"upgrade", "--install", params.ReleaseName, params.ChartPath,
			"--namespace", params.Namespace,
			"--create-namespace",
			"--kube-context", params.KubernetesContext,
			"--set", "tenant=" + params.Tenant,
			"--set", "environment=" + params.Environment,
			"--set", "worktreeStorage=none",
		}
		for _, name := range []string{"erun-devops", "erun-mcp", "erun-dind"} {
			args = append(args, "--set-string", "imageOverrides."+name+"=registry.k8s.io/pause:3.9")
		}
		out, err := exec.Command("helm", args...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("helm upgrade %s: %w: %s", params.ReleaseName, err, strings.TrimSpace(string(out)))
		}
		t.Logf("helm upgrade --install %s landed in %s", params.ReleaseName, params.Namespace)
		return nil
	}
}

// assertReleaseLanded confirms the runtime Deployment object the deploy created
// exists on the cluster — the deploy reached a real cluster and rendered the
// chart, not just flipped a database row.
func assertReleaseLanded(t *testing.T, release, namespace string) {
	t.Helper()
	out, err := exec.Command("kubectl", "--context", "primary",
		"get", "deployment", release, "-n", namespace, "-o", "name").CombinedOutput()
	if err != nil {
		t.Fatalf("deployment %s/%s not found after deploy: %v: %s", namespace, release, err, strings.TrimSpace(string(out)))
	}
	if !strings.Contains(string(out), release) {
		t.Fatalf("unexpected deployment lookup output: %s", strings.TrimSpace(string(out)))
	}
}

// cleanupRelease removes the helm release + namespace the deploy created so the
// cluster returns to a clean state for re-runs (verification-gate hygiene).
func cleanupRelease(release, namespace string) {
	_, _ = exec.Command("helm", "uninstall", release, "--namespace", namespace, "--kube-context", "primary").CombinedOutput()
	_, _ = exec.Command("kubectl", "--context", "primary", "delete", "namespace", namespace, "--ignore-not-found").CombinedOutput()
}
