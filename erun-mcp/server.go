package erunmcp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

const (
	DefaultHost = "127.0.0.1"
	DefaultPort = eruncommon.MCPServicePort
	DefaultPath = eruncommon.MCPServerPath
)

type HTTPConfig struct {
	Host string
	Port int
	Path string
}

func RunHTTP(ctx context.Context, info eruncommon.BuildInfo, cfg HTTPConfig, runtime RuntimeConfig) error {
	cfg, err := normalizeHTTPConfig(cfg)
	if err != nil {
		return err
	}

	server := &http.Server{
		Addr:              listenAddress(cfg),
		Handler:           newHTTPHandler(info, cfg, runtime),
		ReadHeaderTimeout: 5 * time.Second,
	}

	shutdownErr := make(chan error, 1)
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		shutdownErr <- server.Shutdown(shutdownCtx)
	}()

	err = server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return <-shutdownErr
	}
	return err
}

func newHTTPHandler(info eruncommon.BuildInfo, cfg HTTPConfig, runtime RuntimeConfig) http.Handler {
	cfg, _ = normalizeHTTPConfig(cfg)

	// One server per distinct capability set, resolved per request: a caller
	// sees exactly the tools it may call, and `tools/list` is the same answer as
	// what it is allowed to do rather than a menu with locked entries.
	servers := newCapabilityServerCache(func(identity authIdentity) *mcp.Server {
		return newServer(info, runtime, identity)
	})
	handler := mcp.NewStreamableHTTPHandler(servers.serverFor, &mcp.StreamableHTTPOptions{
		JSONResponse:   true,
		SessionTimeout: 5 * time.Minute,
	})

	mux := http.NewServeMux()
	// Auth is the outermost layer, so even idle probes must carry a valid token
	// before any tool runs.
	mux.Handle(cfg.Path, authHTTPMiddleware(mcpAuthConfigFromEnv(), activityHTTPMiddleware(runtime, handler)))
	return mux
}

func activityHTTPMiddleware(runtime RuntimeConfig, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("X-Erun-Idle-Probe") == "true" {
			next.ServeHTTP(w, req)
			return
		}
		recordRuntimeActivity(runtime, eruncommon.ActivityKindMCP, false)
		if requestLooksLikeCodex(req) {
			recordRuntimeActivity(runtime, eruncommon.ActivityKindCodex, false)
		}
		next.ServeHTTP(w, req)
	})
}

func recordRuntimeActivity(runtime RuntimeConfig, kind string, seen bool) {
	_ = eruncommon.RecordEnvironmentActivity(eruncommon.EnvironmentActivityParams{
		Tenant:      runtime.Context.Tenant,
		Environment: runtime.Context.Environment,
		Kind:        kind,
		Seen:        seen,
	})
}

func requestLooksLikeCodex(req *http.Request) bool {
	userAgent := strings.ToLower(req.UserAgent())
	if strings.Contains(userAgent, "codex") {
		return true
	}
	for _, value := range req.Header.Values("X-Erun-Client") {
		if strings.Contains(strings.ToLower(value), "codex") {
			return true
		}
	}
	return false
}

func normalizeHTTPConfig(cfg HTTPConfig) (HTTPConfig, error) {
	if cfg.Host == "" {
		cfg.Host = DefaultHost
	}
	if cfg.Port == 0 {
		cfg.Port = DefaultPort
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return HTTPConfig{}, fmt.Errorf("invalid MCP HTTP port %d", cfg.Port)
	}
	if cfg.Path == "" {
		cfg.Path = DefaultPath
	}
	if cfg.Path[0] != '/' {
		cfg.Path = "/" + cfg.Path
	}
	return cfg, nil
}

func listenAddress(cfg HTTPConfig) string {
	return net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
}

func endpointURL(cfg HTTPConfig) string {
	cfg, _ = normalizeHTTPConfig(cfg)
	return "http://" + listenAddress(cfg) + cfg.Path
}

func newServer(info eruncommon.BuildInfo, runtime RuntimeConfig, identity authIdentity) *mcp.Server {
	info = eruncommon.NormalizeBuildInfo(info)
	runtime = normalizeRuntimeConfig(runtime)

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "erun",
		Version: info.Version,
	}, nil)

	reg := toolRegistrar{server: server, identity: identity}
	registerReadModelTools(reg, info, runtime)
	registerIdleStopTools(reg, runtime)
	registerJobTools(reg, runtime)
	registerCloudTools(reg, runtime)
	registerContextTools(reg, runtime)
	registerDeliveryTools(reg, runtime)
	registerInspectionTools(reg, runtime)

	return server
}

func registerReadModelTools(reg toolRegistrar, info eruncommon.BuildInfo, runtime RuntimeConfig) {
	addTool(reg, &mcp.Tool{
		Name:        "version",
		Description: "Return build metadata for the current erun binary",
	}, versionTool(info))
	addTool(reg, &mcp.Tool{
		Name:        "list",
		Description: "List configured tenants and environments, defaults, and the effective target for the current runtime directory",
	}, listTool(runtime))
}

func registerIdleStopTools(reg toolRegistrar, runtime RuntimeConfig) {
	addTool(reg, &mcp.Tool{
		Name:        "idle",
		Description: "Return environment idle stop timeout and marker status without recording activity. Includes the leases currently holding the env busy, so a caller can see what is deferring auto-stop.",
	}, idleTool(runtime))
	addTool(reg, &mcp.Tool{
		Name: "activity_lease_take",
		Description: "Hold a named lease for the lifetime of long work in this env, so it reports as busy and idle-stop leaves it alone. " +
			"Take one before detaching a build, test suite, or agent run: a detached job makes no MCP calls while it runs, so without a lease the env reads as untouched and auto-stop would kill exactly the work worth protecting. " +
			"Re-taking the same id renews it. Pass the detached job's pid so the lease is reclaimed if it dies; a lease also expires on its own, so it can never pin the env awake forever.",
	}, activityLeaseTakeTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "activity_lease_release",
		Description: "Release a lease taken by activity_lease_take once the work is done, so the env can go idle again. Releasing an unknown or already-expired lease succeeds.",
	}, activityLeaseReleaseTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "activity_lease_list",
		Description: "List the leases currently holding this env busy. Reading the list also reclaims leases that expired or whose holder process is gone, so what it returns is what is actually deferring auto-stop.",
	}, activityLeaseListTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "idle_stop_cancel",
		Description: "Dismiss the pending auto-stop grace warning for the env without touching AWS state. No-op when no warning is armed.",
	}, idleStopCancelTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "idle_stop_history",
		Description: "Return the last N (cap 10) auto-stop audit entries for the env, newest first. Each row carries the per-marker idle/active breakdown captured when the auto-stop grace was armed.",
	}, idleStopHistoryTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "idle_stop_record",
		Description: "Record a host-driven stop entry in stop-history.json (source=host-manual). Called by the desktop's Stop button after the AWS stop succeeds, so the History tab can also explain 'you clicked Stop' alongside the in-pod monitor's auto-stops.",
	}, idleStopRecordTool(runtime))
}

func registerJobTools(reg toolRegistrar, runtime RuntimeConfig) {
	addTool(reg, &mcp.Tool{
		Name: "job_start",
		Description: "Run a long command in this env as a detached job and return its handle. " +
			"Reach for this instead of `raw` for anything you will need to come back to — a build, a test suite, an agent run. " +
			"erun detaches the work, captures its merged stdout and stderr, and records the exit status by waiting on the process inside the env, so nothing has to be wrapped in setsid/nohup/a redirect and no sentinel token or shell expansion sits between the work and its result. " +
			"The job also holds an activity lease for its lifetime, so the env reports as busy and idle-stop leaves it alone. " +
			"Then use job_await (bounded) and job_output (incremental) rather than holding this call open. " +
			"The id defaults to the name; re-using the id of a job that is still running is refused, while re-using a finished one replaces it. " +
			"For an agent run pass agent (claude or codex) plus prompt instead of command: erun invokes the tool in its streaming mode, so job_output returns events while the agent works and job_status reports what it is doing. Running the tool yourself through command would report nothing at all until it exits.",
	}, jobStartTool(runtime))
	addTool(reg, &mcp.Tool{
		Name: "job_attach",
		Description: "Give work you started another way a job handle and an activity lease, so it is visible and protected from idle-stop. " +
			"erun tracks the pid you name and nothing else: the job reads as running while that process lives and as unknown once it is gone. " +
			"It can never report an exit status, because nothing erun ran was waiting on that process to observe one — start work through job_start when you need the outcome.",
	}, jobAttachTool(runtime))
	addTool(reg, &mcp.Tool{
		Name: "job_status",
		Description: "Return one job's state and outcome, or every retained job newest-first. " +
			"The answer is always definite: running, exited with a captured exit code, or explicitly unknown with the reason (its supervisor is gone without an outcome, most often because the runtime pod was replaced). " +
			"It is never a truncated or partial answer, so it is safe to act on. Finished jobs stay readable for 24 hours, so an orchestrator reconnecting after the work ended can still learn what happened. " +
			"An agent job also carries progress — current activity (the last tool and its target), turns, tools run, and the last thing the agent said — normalized by erun from the tool's own event stream, so the shape is the same across AI tools. Poll this to report an in-pod agent's progress; do not scrape the agent's private transcript.",
	}, jobStatusTool(runtime))
	addTool(reg, &mcp.Tool{
		Name: "job_await",
		Description: "Wait a bounded time (default 30s, max 600s) for a job to finish. " +
			"The call always returns inside the timeout — either the outcome or timedOut=true with the job still running — so no connection is held open for the work's lifetime and a dropped stream is never confused with a dead job. " +
			"timedOut is reported separately from every outcome, so 'not finished yet' can never be read as a failure. Call it again to keep waiting.",
	}, jobAwaitTool(runtime))
	addTool(reg, &mcp.Tool{
		Name: "job_output",
		Description: "Read a page of a job's captured output, including while it is still running, so progress is visible before the work exits. " +
			"Pass the previous read's nextOffset back as offset to continue where you left off. " +
			"complete is true only when the job has finished and you have read to the end; the job's own outputTruncated says whether output was dropped at the cap.",
	}, jobOutputTool(runtime))
	addTool(reg, &mcp.Tool{
		Name: "job_cancel",
		Description: "Signal a running job's work (TERM by default, or INT/HUP/KILL). " +
			"The target comes from the job record, never from a command-line pattern, so a cancel can only reach the work it names and never a process that merely looks like it. " +
			"The job's supervisor is deliberately left alone, so it survives to record the outcome; the cancelled job then reads back as a normal exited job carrying the signal.",
	}, jobCancelTool(runtime))
}

func registerCloudTools(reg toolRegistrar, runtime RuntimeConfig) {
	addTool(reg, &mcp.Tool{
		Name:        "cloud_list",
		Description: "List configured root-level cloud provider aliases and token status",
	}, cloudListTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "cloud_init_aws",
		Description: "Initialize an AWS SSO cloud provider alias in root ERun config, with preview support",
	}, cloudInitAWSTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "cloud_init_cloudflare",
		Description: "Initialize a Cloudflare cloud provider alias from a delegated API token (Zone + DNS edit, plus any other scopes the operator will use such as Cloudflare Pages for static sites). The token is verified against the Cloudflare API and held in a local secret store referenced from erun config, never written into erun-config.yaml; environments that attach the alias receive it as CLOUDFLARE_API_TOKEN. Supports preview.",
	}, cloudInitCloudflareTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "cloud_login",
		Description: "Login to a configured cloud provider alias, with preview support",
	}, cloudLoginTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "cloud_oidc",
		Description: "Refresh the OIDC issuer for a configured cloud provider alias, with preview support",
	}, cloudOIDCTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "cloud_set",
		Description: "Set the cloud provider alias for a tenant environment, with preview support",
	}, cloudSetTool(runtime))
	addTool(reg, &mcp.Tool{
		Name: "cloud_inject_aws_credentials",
		Description: "Write temporary AWS credentials into the runtime pod's ~/.aws/credentials under the erun-host profile, " +
			"replacing that profile in place. The credential values are tool arguments, so DO NOT call this from anything " +
			"that records its arguments — an agent transcript, a session log, an audit trail. It exists for the desktop's " +
			"credential refresher, which holds the values in memory. To refresh an environment's host credentials from a " +
			"script, an agent, or a terminal, run `erun cloud refresh <tenant> <environment>` instead: it reads the " +
			"operator's own profile and streams the secret to the pod on stdin, so nothing sensitive passes through the caller.",
	}, cloudInjectAWSCredentialsTool())
	addTool(reg, &mcp.Tool{
		Name:        "cloud_clear_aws_credentials",
		Description: "Remove the erun-host profile from the runtime pod's ~/.aws/credentials",
	}, cloudClearAWSCredentialsTool())
}

func registerContextTools(reg toolRegistrar, runtime RuntimeConfig) {
	addTool(reg, &mcp.Tool{
		Name:        "context_list",
		Description: "List managed ERun cloud Kubernetes contexts",
	}, contextListTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "context_init",
		Description: "Initialize a managed cloud k3s Kubernetes context, with preview support",
	}, contextInitTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "context_stop",
		Description: "Stop a managed ERun cloud Kubernetes context, with preview support",
	}, contextStopTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "context_start",
		Description: "Start a managed ERun cloud Kubernetes context, with preview support",
	}, contextStartTool(runtime))
}

func registerDeliveryTools(reg toolRegistrar, runtime RuntimeConfig) {
	addTool(reg, &mcp.Tool{
		Name:        "init",
		Description: "Run `erun init` using the shared init flow; when more input is needed, return a structured interaction request for the caller to answer in a follow-up tool call",
	}, initTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "build",
		Description: "Build the project's container images for the resolved tenant/environment. The build step of the build → release → push → deploy flow: set deploy to push and roll them out, or release to stamp and publish the release version first.",
	}, buildTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "push",
		Description: "Build and push the project's container images to the BUILD-marked registry for the resolved tenant/environment. The push step of the build → release → push → deploy flow.",
	}, pushTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "deploy",
		Description: "Roll the project's charts out to the resolved tenant/environment: build and push the images they need, mirror them from the FROM to the TO registry when both roles are marked, then run the rollout with the cluster pulling from the DEPLOY registry. The deploy step of the build → release → push → deploy flow. Waits for the rollout to become ready (default 5m, the env's deploy.timeout, or the timeout input) and watches the new pods, keeping the wait while an image is still pulling and aborting early on a real container failure.",
	}, deployTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "publish",
		Description: "Mirror an already-built version's images from the FROM registry to each TO registry for the resolved tenant/environment, without building or deploying. Use it to hand a version you have tested (e.g. built against a local cluster registry) to other users by copying that exact multi-arch image to the shared TO registry. Requires a version (produced by build then push) and a FROM source plus at least one TO destination marked in the registry list.",
	}, publishTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "upgrade",
		Description: "Redeploy the resolved environment to the latest version for its release channel when it is opted into Upgrade all (autoupgrade) and lags. Snapshot-channel environments adopt a stable release once one is published on top of the latest snapshot. High blast radius: rolls out a new runtime image and restarts pods. Set preview to resolve and return the plan (channel, current → target) without deploying.",
	}, upgradeTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "doctor",
		Description: "Diagnose and repair the resolved environment: report why a deploy may have failed (helm release status and runtime pods, read-only); recover a failing runtime release by clearing a stuck pending helm lock or rolling back to the last successful revision; prune unused Docker images, build cache, or stopped containers; and optionally restore or repair the root erun config from a backup or by re-initializing orphaned cloud provider aliases",
	}, doctorTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "delete",
		Description: "Delete an environment from ERun configuration and remove its remote runtime namespace after explicit tenant-environment confirmation",
	}, deleteTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "expose",
		Description: "Expose an in-namespace Service at a stable public hostname under the platform's services zone (requires a platform block in .erun/config.yaml): ensure the per-environment wildcard DNS record points at the env's ingress IP and apply a Host-routing Ingress. Supports preview.",
	}, exposeTool(runtime))
	addTool(reg, &mcp.Tool{
		Name: "pin",
		Description: "Re-pin every erun version reference for this environment in one motion: the Terraform module ?ref, each umbrella chart's erun chart dependencies, the build-env image tag, and the environment's own runtime version. " +
			"They only work when they agree, and nothing else keeps them in step. Idempotent and a no-op once aligned. Refuses a version that is not published. Set revert to go back to the version recorded before the last re-pin. " +
			"Set preview to return the full plan — every site, old and new — without writing. Edits the source of truth only: realizing the version (terraform apply, deploy) stays a separate explicit step.",
	}, pinTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "terraform",
		Description: "Run a hosted platform's per-environment Terraform from terraform-<tenant>/<environment>/ (or <tenant>-devops/terraform-<tenant>/<environment>/, or the paths.terraform base from .erun/config.yaml) for the resolved tenant/environment: resolve the env folder, pick up the symlinked common.tf, and run its main.tf with <environment>.tfvars. State and the provider cache live on the durable home directory (not the playbook tree), so they survive a runtime pod restart. operation is apply (init → fmt → plan → apply), plan (read-only), or destroy. apply/destroy mutate real cloud and cluster state and require confirm to equal the environment name. Injects TF_VAR_cloudflare_api_token from CLOUDFLARE_API_TOKEN when present. Set preview to resolve and return the terraform commands without executing them.",
	}, terraformTool(runtime))
}

func registerInspectionTools(reg toolRegistrar, runtime RuntimeConfig) {
	addTool(reg, &mcp.Tool{
		Name:        "diff",
		Description: "Return the current git diff from the runtime repo root as raw text plus structured file, hunk, line, and tree data",
	}, diffTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "raw",
		Description: "Run an arbitrary command from the runtime repo root and return captured stdout, stderr, and trace output",
	}, rawTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "outputs_list",
		Description: "List the files and folders an agent produced in the runtime pod's outputs directory ($ERUN_OUTPUTS_DIR, default /home/erun/.erun/outputs), newest-first. Read-only.",
	}, outputsListTool())
	addTool(reg, &mcp.Tool{
		Name:        "outputs_download",
		Description: "Read one entry from the runtime pod's outputs directory and return its bytes inline as base64 (a folder as a tar.gz archive). The server runs in the pod, so it returns the content directly for the caller to save. Set preview to return name/type/size without the bytes.",
	}, outputsDownloadTool())
	addTool(reg, &mcp.Tool{
		Name:        "release",
		Description: "Cut a project release from the runtime repo root using .erun/config.yaml branch policy. Stamps the release version into the charts and packaging metadata and commits and tags it locally, then builds and publishes that version's images and helm charts and reads each one back from the registry, and only then pushes the tag, prepares the next patch version, and pushes the branches. A release that completes means deploy can resolve the image and the chart at that version; a release that cannot publish fails while nothing is public. Set preview to resolve and return the plan without executing it.",
	}, releaseTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "contribute_clone",
		Description: "Clone the ERun source repository into $HOME/git/erun inside the environment so contribute-mode tabs can build and run a local ERun checkout",
	}, contributeCloneTool(runtime))
}
