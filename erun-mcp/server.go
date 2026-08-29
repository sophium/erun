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

// RunHTTP serves the MCP edge and the Prometheus metrics listener
// concurrently until ctx is cancelled. Either one failing (a bind error, most
// often) cancels the other and its error wins, rather than the metrics
// listener silently dying while the MCP edge keeps the process alive for the
// rest of the pod's lifetime.
func RunHTTP(ctx context.Context, info eruncommon.BuildInfo, cfg HTTPConfig, metricsCfg MetricsConfig, runtime RuntimeConfig) error {
	cfg, err := normalizeHTTPConfig(cfg)
	if err != nil {
		return err
	}
	metricsCfg, err = normalizeMetricsConfig(metricsCfg)
	if err != nil {
		return err
	}
	runtime = normalizeRuntimeConfig(runtime)
	recorder := newMetricsRecorder(runtime.Context.Tenant, runtime.Context.Environment)

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go startIdleMetricsTicker(runCtx, runtime, recorder)
	go startTrafficWindowTicker(runCtx, runtime, recorder)

	results := make(chan error, 2)
	go func() { results <- runMCPHTTP(runCtx, info, cfg, runtime, recorder) }()
	go func() { results <- runMetricsHTTP(runCtx, metricsCfg, recorder) }()

	first := <-results
	cancel()
	second := <-results
	if first != nil {
		return first
	}
	return second
}

func runMCPHTTP(ctx context.Context, info eruncommon.BuildInfo, cfg HTTPConfig, runtime RuntimeConfig, recorder *metricsRecorder) error {
	server := &http.Server{
		Addr:              listenAddress(cfg),
		Handler:           newHTTPHandler(info, cfg, runtime, recorder),
		ReadHeaderTimeout: 5 * time.Second,
	}

	shutdownErr := make(chan error, 1)
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		shutdownErr <- server.Shutdown(shutdownCtx)
	}()

	err := server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return <-shutdownErr
	}
	return err
}

func newHTTPHandler(info eruncommon.BuildInfo, cfg HTTPConfig, runtime RuntimeConfig, recorder *metricsRecorder) http.Handler {
	cfg, _ = normalizeHTTPConfig(cfg)

	// One server per distinct capability set, resolved per request: a caller
	// sees exactly the tools it may call, and `tools/list` is the same answer as
	// what it is allowed to do rather than a menu with locked entries.
	servers := newCapabilityServerCache(func(identity authIdentity) *mcp.Server {
		return newServer(info, runtime, identity, recorder)
	})
	handler := mcp.NewStreamableHTTPHandler(servers.serverFor, &mcp.StreamableHTTPOptions{
		JSONResponse:   true,
		SessionTimeout: 5 * time.Minute,
	})

	mux := http.NewServeMux()
	// Auth is the outermost layer, so even idle probes must carry a valid token
	// before any tool runs. Traffic metering sits just inside it so a rejected
	// call's tiny response still counts, but wraps the real MCP traffic that
	// erun_traffic_window_bytes exists to measure.
	mux.Handle(cfg.Path, authHTTPMiddleware(mcpAuthConfigFromEnv(), trafficMeteringMiddleware(recorder, activityHTTPMiddleware(runtime, handler))))
	return mux
}

func activityHTTPMiddleware(runtime RuntimeConfig, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get(eruncommon.MCPIdleProbeHeader) == "true" {
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

func newServer(info eruncommon.BuildInfo, runtime RuntimeConfig, identity authIdentity, metrics *metricsRecorder) *mcp.Server {
	info = eruncommon.NormalizeBuildInfo(info)
	runtime = normalizeRuntimeConfig(runtime)

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "erun",
		Version: info.Version,
	}, nil)

	reg := toolRegistrar{server: server, identity: identity, metrics: metrics}
	registerReadModelTools(reg, info, runtime)
	registerIdleStopTools(reg, runtime)
	registerWhipTools(reg, runtime)
	registerJobTools(reg, runtime)
	registerCloudTools(reg, runtime)
	registerContextTools(reg, runtime)
	registerPlatformTools(reg, runtime)
	registerReviewTools(reg, runtime)
	registerDeliveryTools(reg, runtime)
	registerInspectionTools(reg, runtime)
	registerRemovedTools(reg)

	return server
}

// registerRemovedTools gives every retired tool name in erun-common's
// removed-tool table a stub, instead of leaving the name unregistered. An
// unregistered name fails at the SDK's own routing layer with a bare "unknown
// tool", which names a problem and no action the caller can take; the stub
// registered here instead fails with the guidance naming the tool that took
// over its capability. The input type accepts any arguments so a caller still
// shaped for the retired tool's own schema reaches this handler rather than
// failing schema validation first.
func registerRemovedTools(reg toolRegistrar) {
	for name, guidance := range eruncommon.MCPRemovedTools() {
		addTool(reg, &mcp.Tool{
			Name:        name,
			Description: fmt.Sprintf("Removed; use %s instead.", guidance),
		}, removedTool(name, guidance))
	}
}

func removedTool(name, guidance string) func(context.Context, *mcp.CallToolRequest, map[string]any) (*mcp.CallToolResult, any, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, _ map[string]any) (*mcp.CallToolResult, any, error) {
		return nil, nil, fmt.Errorf("%s was removed; use %s instead", name, guidance)
	}
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
			"Re-taking the same id renews it. Pass the detached job's pid so the lease is reclaimed if it dies; a lease also expires on its own, so it can never pin the env awake forever. " +
			"Set exclusive=true before any mutating work (git checkout, staging, committing) in a target environment: at most one exclusive holder is allowed per scope (default 'worktree'), so a second agent job or orchestrator already working the same worktree is refused and named in the error, while a job in a different scope - a separate clone in the same pod - is unaffected. An exclusive take is also refused while an operator's own SSH session is active in the environment, since the operator never takes a lease of their own.",
	}, activityLeaseTakeTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "activity_lease_release",
		Description: "Release a lease taken by activity_lease_take once the work is done, so the env can go idle again. Releasing an unknown or already-expired lease succeeds. Pass exclusive=true and the same scope to release an exclusive claim; only the id that took it can release it.",
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

// registerJobTools registers the exec_job_* family (#1246, following the
// rename #1186 already decided: `job` becomes a sub-family of `exec`) plus
// exec_agent, the job-starting tool that belongs beside them for the same
// reason #1186 gave -- the tool that starts a job and the tools that query it
// should live in one family. job_start itself is gone rather than aliased:
// its command mode and agent mode had incompatible schemas, so a single
// compatibility shim would have meant shipping a tool whose input shape lied
// about what it accepted. Its command mode is exec_raw's wait:false path and
// its agent mode is exec_agent, both already registered elsewhere.
func registerJobTools(reg toolRegistrar, runtime RuntimeConfig) {
	addTool(reg, &mcp.Tool{
		Name: "exec_agent",
		Description: "Run an AI tool (claude or codex) as a detached job and return its handle. " +
			"erun invokes it in its streaming mode, so exec_job_output returns events while it works and exec_job_status reports its current activity rather than only running -- running the tool through exec_raw instead would report nothing at all until it exits. " +
			"The job also holds an activity lease for its lifetime, so the env reports as busy and idle-stop leaves it alone. " +
			"Then use exec_job_await (bounded) and exec_job_output (incremental) rather than holding this call open. " +
			"The id defaults to the name; re-using the id of a job that is still running is refused, while re-using a finished one replaces it. " +
			"env sets additional environment for just this run (e.g. raising CLAUDE_CODE_MAX_OUTPUT_TOKENS for a run that will write large files) — see the env parameter description for what it refuses and why it is not for secrets.",
	}, agentTool(runtime))
	addTool(reg, &mcp.Tool{
		Name: "exec_job_attach",
		Description: "Give work you started another way a job handle and an activity lease, so it is visible and protected from idle-stop. " +
			"erun tracks the pid you name and nothing else: the job reads as running while that process lives and as unknown once it is gone. " +
			"It can never report an exit status, because nothing erun ran was waiting on that process to observe one — start work through exec_raw or exec_agent when you need the outcome.",
	}, jobAttachTool(runtime))
	addTool(reg, &mcp.Tool{
		Name: "exec_job_status",
		Description: "Return one job's state and outcome, or every retained job newest-first. " +
			"The answer is always definite: running, exited with a captured exit code, or explicitly unknown with the reason (its supervisor is gone without an outcome, most often because the runtime pod was replaced). " +
			"It is never a truncated or partial answer, so it is safe to act on: unknown is exactly as terminal as exited — never a reason to keep waiting, and never inferred as 'not finished yet' just because it is not a success. Finished jobs stay readable for 24 hours, so an orchestrator reconnecting after the work ended can still learn what happened. " +
			"aliveAgeMs is the milliseconds since the supervisor's last ~1s beat, computed by erun in its own clock so a caller never subtracts a pod timestamp from its own — the two clocks are not the same clock. Once it exceeds 5000, treat the job as failed (an unknown outcome, never a success, never a tool error) even if state has not caught up to say so yet; a silent-but-healthy command never trips this, because the beat has nothing to do with the work's own output. " +
			"An agent job also carries progress — current activity (the last tool and its target), turns, tools run, and the last thing the agent said — normalized by erun from the tool's own event stream, so the shape is the same across AI tools. Poll this to report an in-pod agent's progress; do not scrape the agent's private transcript. " +
			"A job started as a background task (build, deploy, doctor, and the rest of the job-envelope tools called with wait: false) carries its typed result on this record's result field once exited, in the same shape the tool would have returned synchronously.",
	}, jobStatusTool(runtime))
	addTool(reg, &mcp.Tool{
		Name: "exec_job_await",
		Description: "Wait a bounded time (default 30s, max 600s) for a job to finish. " +
			"The call always returns inside the timeout — either the outcome or timedOut=true with the job still running — so no connection is held open for the work's lifetime and a dropped stream is never confused with a dead job. " +
			"timedOut is reported separately from every outcome, so 'not finished yet' can never be read as a failure — but it is not the only non-outcome case: a job whose supervisor died reads back as state=unknown with timedOut=false, its own third case, distinct from both success and 'still running'. Never re-await a job already reporting unknown expecting a different answer. Call it again only while the job is genuinely still running. " +
			"The returned job's aliveAgeMs (see exec_job_status) is the faster of the two signals: it crosses the 5000ms caller threshold before state necessarily catches up, so a caller in a hurry can act on it directly instead of waiting for the next reconcile.",
	}, jobAwaitTool(runtime))
	addTool(reg, &mcp.Tool{
		Name: "exec_job_output",
		Description: "Read a page of a job's captured output, including while it is still running, so progress is visible before the work exits. " +
			"Pass the previous read's nextOffset back as offset to continue where you left off. " +
			"complete is true only when the job has finished and you have read to the end; the job's own outputTruncated says whether output was dropped at the cap. " +
			"The returned job also carries aliveAgeMs (see exec_job_status) — a silent-but-healthy command never advances outputBytes for minutes at a time, so use aliveAgeMs, not output growth, to tell quiet-but-alive apart from actually dead. " +
			"A background task job (see exec_job_status) has no output log of its own; read its typed result off the returned job instead.",
	}, jobOutputTool(runtime))
	addTool(reg, &mcp.Tool{
		Name: "exec_job_cancel",
		Description: "Signal a running job's work (TERM by default, or INT/HUP/KILL). " +
			"The target comes from the job record, never from a command-line pattern, so a cancel can only reach the work it names and never a process that merely looks like it. " +
			"The job's supervisor is deliberately left alone, so it survives to record the outcome; the cancelled job then reads back as a normal exited job carrying the signal. " +
			"A background task job (build, deploy, doctor, and the rest) has no subprocess of its own to signal and is refused; wait for it or let it finish.",
	}, jobCancelTool(runtime))

	// Deprecated aliases for the five renamed job tools, kept callable for one
	// release (the same window #1186 used for the exec_* renames). job_start
	// has no equivalent alias: see the function comment above for why.
	addTool(reg, &mcp.Tool{
		Name:        "job_attach",
		Description: "Deprecated: use exec_job_attach. Retained for one release; this name will be removed.",
	}, jobAttachTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "job_status",
		Description: "Deprecated: use exec_job_status. Retained for one release; this name will be removed.",
	}, jobStatusTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "job_await",
		Description: "Deprecated: use exec_job_await. Retained for one release; this name will be removed.",
	}, jobAwaitTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "job_output",
		Description: "Deprecated: use exec_job_output. Retained for one release; this name will be removed.",
	}, jobOutputTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "job_cancel",
		Description: "Deprecated: use exec_job_cancel. Retained for one release; this name will be removed.",
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
		Name:        "cloud_init_erun",
		Description: "Initialize a hosted erun platform cloud provider alias: discovers the platform's own config (OIDC issuer, CLI client id) from its unauthenticated GET /v1/platform endpoint and saves the alias — no instance's name is hardcoded. Call cloud_login afterward to sign in (Device Authorization Grant, falling back to Authorization Code + PKCE). Supports preview.",
	}, cloudInitERunTool(runtime))
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

func registerPlatformTools(reg toolRegistrar, runtime RuntimeConfig) {
	addTool(reg, &mcp.Tool{
		Name:        "platform_whoami",
		Description: "Resolve the caller's identity against a hosted erun platform (erun-backend-api) over the erun-type cloud alias erun cloud init erun / erun cloud login set up. Supports preview.",
	}, platformWhoamiTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "platform_tenant_create",
		Description: "Register a new tenant on the erun platform. Requires an operations-tenant caller. A real, immediate write, not a preview, unless preview is set.",
	}, platformTenantCreateTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "platform_tenant_list",
		Description: "List tenants visible to the caller on the erun platform: every tenant for an operations-tenant caller, or just the caller's own tenant otherwise. Supports preview.",
	}, platformTenantListTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "platform_user_enroll",
		Description: "Enroll a user in a tenant on the erun platform. tenantId targets another tenant and is honored only for an operations-tenant caller. Supports preview.",
	}, platformUserEnrollTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "platform_user_list",
		Description: "List a tenant's users on the erun platform. tenantId targets another tenant and is honored only for an operations-tenant caller. Supports preview.",
	}, platformUserListTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "platform_env_list",
		Description: "List the caller's tenant's hosted environments on the erun platform. Supports preview.",
	}, platformEnvListTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "platform_env_get",
		Description: "Fetch one hosted environment by id from the erun platform. Supports preview.",
	}, platformEnvGetTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "platform_env_register",
		Description: "Register a hosted environment on the erun platform. A real, immediate write, not a preview, unless preview is set. For a runtime environment with runtimeVersion set and a deploy executor configured on the platform, this also starts a server-side deploy: poll platform_env_get to watch status move registered -> provisioning -> running/failed.",
	}, platformEnvRegisterTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "platform_env_deploy",
		Description: "Start a server-side deploy of an already-registered environment on the erun platform. Fails with a conflict if a deploy is already in progress. Supports preview.",
	}, platformEnvDeployTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "platform_env_stop",
		Description: "Scale a hosted environment's runtime to zero on the erun platform, the server-side equivalent of `erun stop`. Supports preview.",
	}, platformEnvStopTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "platform_env_delete",
		Description: "Start deleting a hosted environment and tearing down its remote namespace on the erun platform, the server-side equivalent of `erun delete`. Not recoverable. The teardown itself runs in the background — a namespace stuck on an unsatisfiable finalizer can take a while, so this returns as soon as the delete is accepted, with the environment's status \"deleting\". Call platform_env_get to watch it converge to gone (not found) or \"deletion-blocked\" (its deleteError names why); calling delete again against a blocked or still-deleting environment retries it. This call never prompts: pass confirm=true to actually delete. Supports preview (confirm is ignored when preview is true).",
	}, platformEnvDeleteTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "platform_context_list",
		Description: "List the caller's tenant's cloud contexts (managed clusters) on the erun platform. Supports preview.",
	}, platformContextListTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "platform_context_get",
		Description: "Fetch one cloud context by id from the erun platform. Supports preview.",
	}, platformContextGetTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "platform_context_create",
		Description: "Bootstrap a cloud context (managed cluster) on the erun platform: without planOnly this launches a real cloud VM and provisions k3s on it, billing the tenant's cloud account until stopped. planOnly asks the platform to resolve and return the bootstrap plan without creating anything — a real API call, distinct from preview, which skips the network call entirely.",
	}, platformContextCreateTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "platform_provision",
		Description: "Resolve and return the ordered plan for provisioning a hosted environment on the erun platform — tenant, quota, context bootstrap or reuse, namespace, register, deploy — without executing any of it or writing to the database. Pass either contextName (with contextAlias and contextRegion) to bootstrap a new cluster, or kubernetesContext to reuse an existing one.",
	}, platformProvisionTool(runtime))
}

func registerReviewTools(reg toolRegistrar, runtime RuntimeConfig) {
	addTool(reg, &mcp.Tool{
		Name:        "review_list",
		Description: "List reviews on the erun platform, narrowed by any combination of targetBranch, sourceBranch, status, authorUserId, and reviewerUserId. mine and waitingOnMe resolve to the caller's own user id via whoami and cannot be combined with the equivalent explicit id filter. Supports preview.",
	}, reviewListTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "review_show",
		Description: "Fetch one review on the erun platform together with its comment threads and recorded builds. Supports preview.",
	}, reviewShowTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "review_create",
		Description: "Open a review on the erun platform. name is the eventual squash-merge message and must be unique per tenant; a colliding name fails with a conflict. sourceBranch must already exist on the remote (push it first with exec_push), since the review references it by name and the platform can only ever fetch what has actually landed there. A real, immediate write, not a preview, unless preview is set.",
	}, reviewCreateTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "review_comment",
		Description: "Comment on a line of a review on the erun platform, or reply to an existing comment with parentCommentId. A real, immediate write, not a preview, unless preview is set.",
	}, reviewCommentTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "review_resolve",
		Description: "Resolve a comment thread on a review by closing its root comment. commentId must be the thread's root comment (the first comment posted at a file/line, not one created with parentCommentId set); addressing a reply fails, naming the root comment to retry against. A real, immediate write, not a preview, unless preview is set.",
	}, reviewResolveTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "review_unresolve",
		Description: "Reopen a comment thread on a review by marking its root comment OPEN again. commentId must be the thread's root comment (the first comment posted at a file/line, not one created with parentCommentId set); addressing a reply fails, naming the root comment to retry against. A real, immediate write, not a preview, unless preview is set.",
	}, reviewUnresolveTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "review_close",
		Description: "Close a review on the erun platform without merging it. A real, immediate write, not a preview, unless preview is set.",
	}, reviewCloseTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "review_record-build",
		Description: "Record a build against a review on the erun platform. This is the only way to transition a review off OPEN: a successful build moves it to READY (and on to MERGE if it was already the merge queue's head), a failed one moves it to FAILED. There is no separate tool to set a review's status directly. commitId must be the full 40-character commit hash the build ran against, and version the version it minted (from the build tool's result) — required even when successful is false, since release resolves the version before the build step runs. A real, immediate write, not a preview, unless preview is set.",
	}, reviewRecordBuildTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "review_queue_list",
		Description: "List a target branch's merge queue on the erun platform, in queue order. Supports preview.",
	}, reviewMergeQueueListTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "review_queue_advance",
		Description: "Advance a target branch's merge queue head to MERGE on the erun platform, which starts that review's merge-gate build. Refuses with the unresolved comment thread count when the queue head still has open threads — resolve them (review_resolve) or use review_queue_override_advance. A real, immediate mutation of shared control-plane state, not a preview, unless preview is set.",
	}, reviewMergeQueueAdvanceTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "review_queue_override-advance",
		Description: "Bypass review_queue_advance's unresolved-thread gate and advance a target branch's merge queue head to MERGE anyway. reason is required and is recorded in the platform's audit trail alongside the caller's identity — this is a deliberate, accountable escape hatch, not a routine way to advance the queue. A real, immediate mutation of shared control-plane state, not a preview, unless preview is set.",
	}, reviewMergeQueueOverrideAdvanceTool(runtime))
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
		Name:        "observe",
		Description: "Report the resolved environment's Kubernetes state, read-only: pods (each container's name, running image, and resource limits), ResourceQuota/LimitRange usage, Ingress hosts and TLS secret names, Certificate readiness, and the runtime helm release (chart/app version, revision, status, and the image overrides and runtime resource limits erun itself set). When a Certificate is not Ready, its CertificateRequest -> Order -> Challenge chain is walked automatically for the failure reason, so a stuck issuance (for example a webhook solver's RBAC denial) surfaces in this one call. The release is diffed against the running containers and against the env config's recorded runtimeversion/runtimeimage/runtimepod, so a drifted deploy (an out-of-band image, a resized pod the release never recorded) is named rather than left for the caller to spot. Optionally check named Secrets for a key's presence without reading their values. Every call is a kubectl get or a helm status; nothing here can mutate the cluster.",
	}, observeTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "usage",
		Description: "Report the resolved environment's live CPU, memory, and disk usage, read straight from the runtime container's cgroup v2 accounting and a statfs of the workspace mount -- no metrics-server required, so it works on clusters where `kubectl top` reports \"Metrics API not available\". Memory is reported against the container's own limit (current, peak high-water mark, and a real OOM-kill count) and CPU utilisation against its quota over a sample window; a named warning fires when memory, memory's peak, or disk usage cross a fixed threshold. Every field reports its own unavailability (cgroup v1, an unlimited limit, an unreadable file) rather than failing the call. Also carries this environment's standing sizing recommendation (`sizing`), when retained usage history has accumulated one -- the same raise/lower/hold verdict and evidence window `erun list` reports, so a caller does not need a separate `resize --preview` just to see it.",
	}, usageTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "resize",
		Description: "Change the runtime pod's CPU/memory limits and roll it out, without re-running init to change two numbers. Pass cpu/memory explicitly, or applyRecommendation to size from this environment's own standing sizing recommendation (resolved from usage history retained in this pod) instead of retyping a value already computed. A resize whose resolved size matches the current one is a no-op. Moves only the runtime container's throttle/OOM ceiling and its namespace quota draw -- never the scheduler's fixed request, the erun-dind sidecar, or any PVC. Because a resize rolls the pod (Recreate strategy) and would kill any live session inside it, it first checks this environment's activity leases and refuses, naming every holder, unless overrideLease is set. Supports preview.",
	}, resizeTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "delete",
		Description: "Delete an environment from ERun configuration and remove its remote runtime namespace after explicit tenant-environment confirmation",
	}, deleteTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "expose",
		Description: "Expose an in-namespace Service at a stable public hostname under the platform's services zone (requires a platform block in .erun/config.yaml): ensure the per-environment wildcard DNS record points at the env's ingress IP and apply a Host-routing Ingress. The Ingress references a per-env wildcard TLS Secret by default; nothing populates it unless dns01TokenFile, dns01BrokerUrl, and acmeEmail are also set, in which case it also provisions a namespaced cert-manager Issuer + Certificate through erun's DNS-01 broker. Supports preview.",
	}, exposeTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "unexpose",
		Description: "Remove an environment's per-env wildcard DNS record — the DNS-side counterpart to expose, run at environment teardown so records don't accumulate for environments that no longer exist. Touches only the platform DNS zone; the Ingress that referenced the record lives in the environment's own namespace and is torn down with it. Requires a platform block in .erun/config.yaml unless servicesZone and platformNamespace are both set. Supports preview.",
	}, unexposeTool(runtime))
	addTool(reg, &mcp.Tool{
		Name: "pin",
		Description: "Re-pin every erun version reference for this environment in one motion: the Terraform module ?ref, an erun image reference set directly in Terraform variables (e.g. the cluster-edge module's dns01_webhook_image), each umbrella chart's erun chart dependencies, the build-env image tag, and the environment's own runtime version. " +
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
		Name:        "exec_diff",
		Description: "Return the current git diff from the runtime repo root as raw text plus structured file, hunk, line, and tree data",
	}, diffTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "exec_raw",
		Description: "Run an arbitrary command from the runtime repo root and return captured stdout, stderr, and trace output",
	}, rawTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "exec_write",
		Description: "Write content to a path in the runtime repo's working tree, taking the content as data — never through a shell, so nothing in it is reinterpreted. Refuses if the resolved path would land outside the repo root. Reports the resolved path and byte count written. Set preview to trace the write without performing it.",
	}, writeTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "exec_commit",
		Description: "Stage every change (or, with paths set, only those paths) in the runtime repo's working tree and commit it with a message taken as data — never through a shell. branch must match the tree's actual current branch; the commit is refused, loudly, when it does not, rather than landing on whichever branch HEAD happens to be on. When paths is set, the commit is refused just as loudly if the tree has changes outside the declared paths, so an unrelated writer's edits can never be absorbed into it. Reports the branch, commit id, and files committed. Set preview to verify the branch and trace the files that would be committed without committing.",
	}, commitTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "exec_push",
		Description: "Push the runtime repo's working tree's current branch to a remote. branch must match the tree's actual current branch; the push is refused, loudly, when it does not, rather than pushing whichever branch HEAD happens to be on. A real, immediate mutation of shared remote state — a branch a hosted review or another reviewer can only ever fetch once it has actually landed there. Set preview to verify the branch and trace the push without running it.",
	}, execPushTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "exec_merge",
		Description: "Fetch targetBranch from a remote and merge it into the runtime repo's working tree's current branch with an explicit merge commit — never a rebase, since review comments anchor to a commit id and a rewrite would orphan every thread on an open review. A conflicted merge is reported as a distinct, named outcome; the worktree is left exactly as git left it, mid-merge, for the caller to resolve or abort. A real, immediate mutation of the working tree. Set preview to trace the fetch and merge without running them.",
	}, execMergeTool(runtime))

	// Deprecated aliases for the four exec tools, kept callable for one release
	// (#1186). `erun exec` was the only command group on the surface whose tools
	// dropped their prefix, and it is the group whose members differ most in
	// blast radius -- exec_diff only reads, exec_raw runs arbitrary argv -- so
	// the rename is worth the alias window rather than a hard break. Each alias
	// shares its replacement's handler and resolves to the same descriptor and
	// capability, so nothing about authorization or auditing changes with the
	// name a caller happens to use.
	addTool(reg, &mcp.Tool{
		Name:        "diff",
		Description: "Deprecated: use exec_diff. Retained for one release; this name will be removed.",
	}, diffTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "raw",
		Description: "Deprecated: use exec_raw. Retained for one release; this name will be removed.",
	}, rawTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "write",
		Description: "Deprecated: use exec_write. Retained for one release; this name will be removed.",
	}, writeTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "commit",
		Description: "Deprecated: use exec_commit. Retained for one release; this name will be removed.",
	}, commitTool(runtime))
	addTool(reg, &mcp.Tool{
		Name:        "outputs_list",
		Description: "List the files and folders an agent produced in the runtime pod's outputs directory ($ERUN_OUTPUTS_DIR, default /home/erun/.erun/outputs), newest-first. Read-only.",
	}, outputsListTool())
	addTool(reg, &mcp.Tool{
		Name:        "outputs_download",
		Description: "Read one entry from the runtime pod's outputs directory and return its bytes inline as base64 (a folder as a tar.gz archive). The server runs in the pod, so it returns the content directly for the caller to save. On a macOS host an unsigned macOS binary is ad-hoc signed first, because the system kills an unsigned one on exec without printing anything; the signing field reports it. Set preview to return name/type/size without the bytes.",
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
