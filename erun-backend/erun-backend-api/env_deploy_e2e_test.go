package backendapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
	_ "github.com/jackc/pgx/v5/stdlib"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"

	eruncommon "github.com/sophium/erun/erun-common"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/provision"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

// envDeployE2E is the opt-in environment for the live env-deploy gate. It needs
// a real Kubernetes cluster (any conformant one — a local k3d/kind cluster is
// enough, no virtualization required), a migrated Postgres, and a tenant runtime
// image the cluster can run, because the deploy executor's whole job is to
// launch that image as a Job and watch it install the runtime chart.
type envDeployE2E struct {
	databaseURL    string
	dbosURL        string
	kubeconfig     string
	registry       string
	version        string
	namespace      string
	serviceAccount string
}

func envDeployE2EFromEnv(t *testing.T) envDeployE2E {
	t.Helper()
	if os.Getenv("ERUN_E2E_ENV_DEPLOY") != "1" {
		t.Skip("opt-in: set ERUN_E2E_ENV_DEPLOY=1 (+ a Kubernetes cluster, a migrated Postgres, and a tenant runtime image)")
	}
	config := envDeployE2E{
		databaseURL:    os.Getenv("ERUN_E2E_ENV_DEPLOY_DATABASE_URL"),
		dbosURL:        os.Getenv("DBOS_SYSTEM_DATABASE_URL"),
		kubeconfig:     os.Getenv("ERUN_E2E_ENV_DEPLOY_KUBECONFIG"),
		registry:       os.Getenv("ERUN_E2E_ENV_DEPLOY_REGISTRY"),
		version:        os.Getenv("ERUN_E2E_ENV_DEPLOY_VERSION"),
		namespace:      os.Getenv("ERUN_E2E_ENV_DEPLOY_NAMESPACE"),
		serviceAccount: os.Getenv("ERUN_E2E_ENV_DEPLOY_SERVICE_ACCOUNT"),
	}
	for name, value := range map[string]string{
		"ERUN_E2E_ENV_DEPLOY_DATABASE_URL":    config.databaseURL,
		"DBOS_SYSTEM_DATABASE_URL":            config.dbosURL,
		"ERUN_E2E_ENV_DEPLOY_KUBECONFIG":      config.kubeconfig,
		"ERUN_E2E_ENV_DEPLOY_REGISTRY":        config.registry,
		"ERUN_E2E_ENV_DEPLOY_VERSION":         config.version,
		"ERUN_E2E_ENV_DEPLOY_NAMESPACE":       config.namespace,
		"ERUN_E2E_ENV_DEPLOY_SERVICE_ACCOUNT": config.serviceAccount,
	} {
		if value == "" {
			t.Skipf("%s is required", name)
		}
	}
	return config
}

// TestDeployEnvironmentEndToEnd drives the deploy endpoint against a live
// cluster: the durable workflow must launch a real deploy Job, watch it install
// the runtime chart into the per-env namespace, and land the environment on
// `running` naming the version it deployed.
func TestDeployEnvironmentEndToEnd(t *testing.T) {
	config := envDeployE2EFromEnv(t)
	srv, db, kube, kubeConfig := startEnvDeployAPI(t, config, "erun-env-deploy-e2e")

	tenant := e2eTenantName(t, srv.URL)
	// Registered without a pinned version, so nothing deploys on create and the
	// explicit endpoint is unambiguously what performs the deploy.
	environmentID := e2eRegisterEnvironment(t, srv.URL, "prod")

	assertDeployClaimIsExclusive(t, db, environmentID)

	code, body := e2eRequest(t, srv.URL, http.MethodPost, "/v1/environments/"+environmentID+"/deploy",
		map[string]any{"version": config.version})
	if code != http.StatusAccepted {
		t.Fatalf("deploy: HTTP %d (want 202): %s", code, body)
	}

	awaitEnvironmentRunning(t, srv.URL, environmentID, config.version)
	namespace := tenant + "-prod"
	assertRuntimeChartInstalled(t, kube, tenant)
	assertMCPReachable(t, kubeConfig, kube, namespace, tenant)
	assertRedeployReruns(t, kube, srv.URL, config, environmentID)
	assertDeployRejectedWhileInFlight(t, db, srv.URL, environmentID, config.version)
	// Teardown last: this is the full lifecycle #605's e2e gate asks for — a
	// provisioned env reaching Running with its MCP reachable, then torn down.
	assertEnvironmentTornDown(t, srv.URL, kube, environmentID, namespace)
}

// assertMCPReachable proves the per-env MCP edge (erun-devops/AGENTS.md: it
// runs inside the runtime container, sharing its toolchain) is actually
// serving requests, not just that a pod landed. It port-forwards to the
// runtime pod's MCP port and drives a real JSON-RPC `initialize` call — the
// same handshake `erun-mcp/AGENTS.md`'s in-pod diagnostic recipe uses.
func assertMCPReachable(t *testing.T, kubeConfig *rest.Config, kube kubernetes.Interface, namespace, tenant string) {
	t.Helper()
	ctx := context.Background()
	releaseName := tenant + "-devops"
	pods, err := kube.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: "app=" + releaseName})
	mustNoErr(t, err, "list runtime pods")
	if len(pods.Items) == 0 {
		t.Fatalf("no runtime pod found in %s for app=%s", namespace, releaseName)
	}
	podName := pods.Items[0].Name

	localPort := forwardToPod(t, kubeConfig, kube, namespace, podName, eruncommon.LowerServicePort)

	initBody := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"erun-e2e","version":"1.0.0"}}}`
	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/mcp", localPort), bytes.NewBufferString(initBody))
	mustNoErr(t, err, "build mcp initialize request")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")

	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	mustNoErr(t, err, "call mcp initialize")
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("mcp initialize: HTTP %d: %s", resp.StatusCode, respBody)
	}
	if resp.Header.Get("Mcp-Session-Id") == "" {
		t.Fatalf("mcp initialize response carries no Mcp-Session-Id header: %s", respBody)
	}
}

// forwardToPod opens a client-go SPDY port-forward to the pod's targetPort and
// returns the local port it picked, cleaned up when the test ends.
func forwardToPod(t *testing.T, kubeConfig *rest.Config, kube kubernetes.Interface, namespace, podName string, targetPort int) int {
	t.Helper()
	transport, upgrader, err := spdy.RoundTripperFor(kubeConfig)
	mustNoErr(t, err, "build spdy round tripper")
	req := kube.CoreV1().RESTClient().Post().Resource("pods").Namespace(namespace).Name(podName).SubResource("portforward")
	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, http.MethodPost, req.URL())

	stopCh := make(chan struct{})
	readyCh := make(chan struct{})
	t.Cleanup(func() { close(stopCh) })

	fw, err := portforward.New(dialer, []string{fmt.Sprintf("0:%d", targetPort)}, stopCh, readyCh, io.Discard, io.Discard)
	mustNoErr(t, err, "build port forwarder")
	go func() {
		if err := fw.ForwardPorts(); err != nil {
			t.Logf("port-forward to %s/%s:%d ended: %v", namespace, podName, targetPort, err)
		}
	}()
	select {
	case <-readyCh:
	case <-time.After(30 * time.Second):
		t.Fatalf("port-forward to %s/%s:%d never became ready", namespace, podName, targetPort)
	}
	ports, err := fw.GetPorts()
	mustNoErr(t, err, "get forwarded ports")
	if len(ports) != 1 {
		t.Fatalf("forwarded ports = %+v, want exactly one", ports)
	}
	return int(ports[0].Local)
}

// assertEnvironmentTornDown drives the stop then delete lifecycle endpoints —
// #605's acceptance criterion ends with "tear it down" — and confirms the
// namespace is actually gone, not just that the API answered.
func assertEnvironmentTornDown(t *testing.T, baseURL string, kube kubernetes.Interface, environmentID, namespace string) {
	t.Helper()
	code, body := e2eRequest(t, baseURL, http.MethodPost, "/v1/environments/"+environmentID+"/stop", nil)
	if code != http.StatusOK {
		t.Fatalf("stop: HTTP %d (want 200): %s", code, body)
	}
	code, body = e2eRequest(t, baseURL, http.MethodDelete, "/v1/environments/"+environmentID, nil)
	if code != http.StatusNoContent {
		t.Fatalf("delete: HTTP %d (want 204): %s", code, body)
	}
	ctx := context.Background()
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		if _, err := kube.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{}); err != nil {
			return // namespace gone (NotFound), teardown confirmed
		}
		time.Sleep(3 * time.Second)
	}
	t.Fatalf("namespace %s still exists after delete", namespace)
}

// startEnvDeployAPI wires the API the way the control plane runs it — real
// Kubernetes client, real Postgres, real durable workflow — and hands back the
// pieces a scenario asserts against.
func startEnvDeployAPI(t *testing.T, config envDeployE2E, appName string) (*httptest.Server, *sql.DB, kubernetes.Interface, *rest.Config) {
	t.Helper()

	kubeConfig, err := clientcmd.BuildConfigFromFlags("", config.kubeconfig)
	mustNoErr(t, err, "build kube config")
	kube, err := kubernetes.NewForConfig(kubeConfig)
	mustNoErr(t, err, "kube client")

	db, err := sql.Open("pgx", config.databaseURL)
	mustNoErr(t, err, "open db")
	t.Cleanup(func() { _ = db.Close() })
	dbosCtx, err := dbos.NewDBOSContext(context.Background(), dbos.Config{AppName: appName, DatabaseURL: config.dbosURL})
	mustNoErr(t, err, "dbos context")

	handler, err := NewHandler(HandlerOptions{
		TokenVerifier: TokenVerifierFunc(func(_ context.Context, token string) (Claims, error) {
			if token != e2eDevToken {
				return Claims{}, errors.New("invalid dev token")
			}
			return Claims{Issuer: "https://dev.local", Subject: "dev-user", Username: "dev"}, nil
		}),
		IdentityCache: NewIdentityResolutionCache(IdentityCacheOptions{}),
		DB:            db,
		DBDialect:     repository.DialectPostgres,
		DBOSContext:   dbosCtx,
		KubeClient:    kube,
		EnvDeploy: provision.EnvDeployConfig{
			Registry:               config.registry,
			PlatformNamespace:      config.namespace,
			DeployerServiceAccount: config.serviceAccount,
		},
	})
	mustNoErr(t, err, "new handler")
	mustNoErr(t, dbos.Launch(dbosCtx), "dbos launch")
	t.Cleanup(func() { dbos.Shutdown(dbosCtx, 5*time.Second) })

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv, db, kube, kubeConfig
}

// TestDeployEnvironmentRecordsAnUnpullableChart is the failure this hardening
// exists for. erun publishes its runtime chart only beside the runtime image it
// releases, so a version whose chart was never pushed — or a deploy registry that
// only ever holds the tenant's own app images — cannot be installed at all. The
// in-Job `erun deploy` already says so, naming the version and every coordinate
// its resolution ladder probed; what the control plane must do is put that in the
// environment's provisionError instead of recording that a Job exited.
//
// It needs a version whose <tenant>-devops IMAGE the cluster can pull but whose
// chart is not published, because the deploy has to get far enough to attempt the
// chart pull. Re-tagging the runtime image under an unpublished version is the
// cheapest way to produce one.
func TestDeployEnvironmentRecordsAnUnpullableChart(t *testing.T) {
	config := envDeployE2EFromEnv(t)
	version := os.Getenv("ERUN_E2E_ENV_DEPLOY_UNPUBLISHED_VERSION")
	if version == "" {
		t.Skip("ERUN_E2E_ENV_DEPLOY_UNPUBLISHED_VERSION is required: a version whose <tenant>-devops image is pullable but whose runtime chart is not published")
	}
	srv, _, kube, _ := startEnvDeployAPI(t, config, "erun-env-deploy-chart-gap-e2e")

	environmentID := e2eRegisterEnvironment(t, srv.URL, "chartgap")
	t.Cleanup(func() { deleteDeployJobs(t, kube, config.namespace, environmentID) })

	code, body := e2eRequest(t, srv.URL, http.MethodPost, "/v1/environments/"+environmentID+"/deploy",
		map[string]any{"version": version})
	if code != http.StatusAccepted {
		t.Fatalf("deploy: HTTP %d (want 202): %s", code, body)
	}

	reason := awaitEnvironmentFailed(t, srv.URL, environmentID)
	t.Logf("recorded provisionError:\n%s", reason)
	// The version and the registry are what an operator has to change, so an
	// error that names neither is the opaque Job exit this test exists to reject.
	for _, want := range []string{version, config.registry, "chart"} {
		if !strings.Contains(reason, want) {
			t.Fatalf("provisionError does not mention %q, so it is not actionable:\n%s", want, reason)
		}
	}
	if len(strings.TrimSpace(strings.TrimPrefix(reason, "deploy job failed for version "+version))) <= len(version) {
		t.Fatalf("provisionError carries no detail beyond the job outcome:\n%s", reason)
	}
}

// awaitEnvironmentFailed polls until the durable workflow reports a terminal
// state and returns the reason it recorded, failing if the deploy somehow
// succeeded.
func awaitEnvironmentFailed(t *testing.T, baseURL, environmentID string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Minute)
	for time.Now().Before(deadline) {
		code, body := e2eRequest(t, baseURL, http.MethodGet, "/v1/environments/"+environmentID, nil)
		if code != http.StatusOK {
			t.Fatalf("get environment: HTTP %d: %s", code, body)
		}
		var got struct {
			Status         string `json:"status"`
			ProvisionError string `json:"provisionError"`
		}
		mustNoErr(t, json.Unmarshal([]byte(body), &got), "parse environment response")
		switch got.Status {
		case "failed":
			return got.ProvisionError
		case "running":
			t.Fatal("the deploy succeeded, so its chart was published after all — pick a version with no published chart")
		}
		time.Sleep(3 * time.Second)
	}
	t.Fatal("timed out waiting for the deploy to reach a terminal state")
	return ""
}

// deleteDeployJobs clears the Jobs a scenario left in the platform namespace, so
// a failed deploy does not leave its pod sitting in the operator's cluster until
// the TTL reaps it.
func deleteDeployJobs(t *testing.T, kube kubernetes.Interface, namespace, environmentID string) {
	t.Helper()
	jobs, err := kube.BatchV1().Jobs(namespace).List(context.Background(), metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/managed-by=erun-deploy-executor",
	})
	if err != nil {
		t.Logf("listing deploy jobs to clean up environment %s: %v", environmentID, err)
		return
	}
	policy := metav1.DeletePropagationBackground
	for _, job := range jobs.Items {
		if job.Labels["erun.io/environment"] != "chartgap" {
			continue
		}
		if err := kube.BatchV1().Jobs(namespace).Delete(context.Background(), job.Name, metav1.DeleteOptions{PropagationPolicy: &policy}); err != nil {
			t.Logf("deleting deploy job %s: %v", job.Name, err)
		}
	}
}

// assertRedeployReruns is the reason the endpoint exists: deploying the same
// version again must actually run again. An environment-keyed Job and workflow
// would both be terminal by now, so a replay would report the old outcome
// without touching the cluster — this holds the second deploy to producing its
// own Job.
func assertRedeployReruns(t *testing.T, kube kubernetes.Interface, baseURL string, config envDeployE2E, environmentID string) {
	t.Helper()
	before := deployJobNames(t, kube, config.namespace)
	code, body := e2eRequest(t, baseURL, http.MethodPost, "/v1/environments/"+environmentID+"/deploy",
		map[string]any{"version": config.version})
	if code != http.StatusAccepted {
		t.Fatalf("re-deploy: HTTP %d (want 202): %s", code, body)
	}
	awaitEnvironmentRunning(t, baseURL, environmentID, config.version)
	after := deployJobNames(t, kube, config.namespace)
	if len(after) <= len(before) {
		t.Fatalf("re-deploy ran no new job: jobs before %v, after %v", before, after)
	}
}

func deployJobNames(t *testing.T, kube kubernetes.Interface, namespace string) []string {
	t.Helper()
	jobs, err := kube.BatchV1().Jobs(namespace).List(context.Background(), metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/managed-by=erun-deploy-executor",
	})
	mustNoErr(t, err, "list deploy jobs")
	names := make([]string, 0, len(jobs.Items))
	for _, job := range jobs.Items {
		names = append(names, job.Name)
	}
	return names
}

// assertDeployRejectedWhileInFlight holds the claim directly rather than racing
// two real deploys, so the conflict the endpoint must report is observed
// deterministically over HTTP.
func assertDeployRejectedWhileInFlight(t *testing.T, db *sql.DB, baseURL, environmentID, version string) {
	t.Helper()
	environments := repository.NewEnvironmentRepository(repository.NewTxManager(db, repository.DialectPostgres))
	ctx := e2eOperationsContext(t, db)
	claimed, err := environments.ClaimDeploy(ctx, environmentID, time.Hour)
	mustNoErr(t, err, "hold the claim")
	if !claimed {
		t.Fatal("could not hold the claim on a running environment")
	}
	code, body := e2eRequest(t, baseURL, http.MethodPost, "/v1/environments/"+environmentID+"/deploy",
		map[string]any{"version": version})
	if code != http.StatusConflict {
		t.Fatalf("deploy while one is in flight: HTTP %d (want 409): %s", code, body)
	}
	// Hand the claim back, so the test does not leave behind an environment that
	// looks wedged mid-deploy.
	mustNoErr(t, environments.UpdateProvisioningStatus(ctx, environmentID, repository.EnvironmentStatusUpdate{
		Status: "running",
	}), "release the held claim")
}

// e2eTenantName reads the tenant the bootstrap identity resolved to. It decides
// both the deploy namespace and the tenant runtime image, so the test asserts
// against the real one rather than assuming it.
func e2eTenantName(t *testing.T, baseURL string) string {
	t.Helper()
	code, body := e2eRequest(t, baseURL, http.MethodGet, "/v1/config", nil)
	if code != http.StatusOK {
		t.Fatalf("read config: HTTP %d: %s", code, body)
	}
	var config struct {
		Tenant struct {
			Name string `json:"name"`
		} `json:"tenant"`
	}
	mustNoErr(t, json.Unmarshal([]byte(body), &config), "parse config response")
	if config.Tenant.Name == "" {
		t.Fatalf("config carries no tenant name: %s", body)
	}
	return config.Tenant.Name
}

func e2eRegisterEnvironment(t *testing.T, baseURL, name string) string {
	t.Helper()
	code, body := e2eRequest(t, baseURL, http.MethodPost, "/v1/environments",
		map[string]any{"name": name, "type": "runtime"})
	if code != http.StatusCreated {
		t.Fatalf("register environment: HTTP %d (want 201): %s", code, body)
	}
	var created struct {
		EnvironmentID string `json:"environmentId"`
		Status        string `json:"status"`
	}
	mustNoErr(t, json.Unmarshal([]byte(body), &created), "parse register response")
	if created.Status != "registered" {
		t.Fatalf("registered status = %q, want registered", created.Status)
	}
	return created.EnvironmentID
}

// assertDeployClaimIsExclusive exercises the claim against the real database,
// which is where its correctness actually lives: the concurrency guard is one
// conditional UPDATE, and a stale claim must stay re-claimable so a control
// plane that crashed mid-deploy never locks an environment out. Run before the
// deploy, then handed back so the deploy under test starts from `registered`.
func assertDeployClaimIsExclusive(t *testing.T, db *sql.DB, environmentID string) {
	t.Helper()
	environments := repository.NewEnvironmentRepository(repository.NewTxManager(db, repository.DialectPostgres))
	ctx := e2eOperationsContext(t, db)

	claimed, err := environments.ClaimDeploy(ctx, environmentID, time.Hour)
	mustNoErr(t, err, "first claim")
	if !claimed {
		t.Fatal("first claim was refused on a registered environment")
	}
	claimed, err = environments.ClaimDeploy(ctx, environmentID, time.Hour)
	mustNoErr(t, err, "second claim")
	if claimed {
		t.Fatal("second claim succeeded, so two concurrent deploys could run into the same release")
	}
	// A zero window makes every claim stale, which is the wedged-deploy recovery.
	claimed, err = environments.ClaimDeploy(ctx, environmentID, 0)
	mustNoErr(t, err, "stale claim")
	if !claimed {
		t.Fatal("a stale claim was refused, so a crashed deploy would lock the environment out permanently")
	}
	mustNoErr(t, environments.UpdateProvisioningStatus(ctx, environmentID, repository.EnvironmentStatusUpdate{
		Status: "registered",
	}), "reset status")
}

// e2eOperationsContext rebuilds the request-scoped security context the
// repositories' row-level-security wiring needs, bound to the bootstrap tenant.
func e2eOperationsContext(t *testing.T, db *sql.DB) context.Context {
	t.Helper()
	var tenantID, tenantType string
	err := db.QueryRow(`SELECT tenant_id, type FROM tenants ORDER BY created_at ASC LIMIT 1`).Scan(&tenantID, &tenantType)
	mustNoErr(t, err, "read bootstrap tenant")
	return security.WithContext(context.Background(), security.Context{TenantID: tenantID, TenantType: tenantType})
}

// awaitEnvironmentRunning polls the environment until the durable workflow
// reports a terminal state, and holds it to naming the version it deployed.
func awaitEnvironmentRunning(t *testing.T, baseURL, environmentID, version string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Minute)
	for time.Now().Before(deadline) {
		code, body := e2eRequest(t, baseURL, http.MethodGet, "/v1/environments/"+environmentID, nil)
		if code != http.StatusOK {
			t.Fatalf("get environment: HTTP %d: %s", code, body)
		}
		var got struct {
			Status          string `json:"status"`
			ProvisionError  string `json:"provisionError"`
			DeployedVersion string `json:"deployedVersion"`
		}
		mustNoErr(t, json.Unmarshal([]byte(body), &got), "parse environment response")
		switch got.Status {
		case "running":
			if got.DeployedVersion != version {
				t.Fatalf("deployedVersion = %q, want the deployed %q", got.DeployedVersion, version)
			}
			return
		case "failed":
			t.Fatalf("deploy failed: %s", got.ProvisionError)
		}
		time.Sleep(3 * time.Second)
	}
	t.Fatal("timed out waiting for the deploy to reach running")
}

// assertRuntimeChartInstalled proves the deploy reached the cluster, not just
// the database: the per-env namespace exists, it carries the runtime workload,
// and Helm recorded a release under the runtime release name.
func assertRuntimeChartInstalled(t *testing.T, kube kubernetes.Interface, tenant string) {
	t.Helper()
	ctx := context.Background()
	namespace := tenant + "-prod"
	if _, err := kube.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{}); err != nil {
		t.Fatalf("per-env namespace %s: %v", namespace, err)
	}
	deployments, err := kube.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{})
	mustNoErr(t, err, "list deployments")
	if len(deployments.Items) == 0 {
		t.Fatalf("no workload landed in %s", namespace)
	}
	releases, err := kube.CoreV1().Secrets(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "owner=helm,name=" + tenant + "-devops",
	})
	mustNoErr(t, err, "list helm releases")
	if len(releases.Items) == 0 {
		t.Fatalf("helm recorded no %s-devops release in %s", tenant, namespace)
	}
}
