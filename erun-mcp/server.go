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
	DefaultPath = "/mcp"
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

	server := newServer(info, runtime)
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return server
	}, &mcp.StreamableHTTPOptions{
		JSONResponse:   true,
		SessionTimeout: 5 * time.Minute,
	})

	mux := http.NewServeMux()
	mux.Handle(cfg.Path, activityHTTPMiddleware(runtime, handler))
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

func newServer(info eruncommon.BuildInfo, runtime RuntimeConfig) *mcp.Server {
	info = eruncommon.NormalizeBuildInfo(info)
	runtime = normalizeRuntimeConfig(runtime)

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "erun",
		Version: info.Version,
	}, nil)

	registerReadModelTools(server, info, runtime)
	registerIdleStopTools(server, runtime)
	registerCloudTools(server, runtime)
	registerContextTools(server, runtime)
	registerDeliveryTools(server, runtime)
	registerInspectionTools(server, runtime)

	return server
}

// registerReadModelTools registers the read-only build/config introspection
// tools.
func registerReadModelTools(server *mcp.Server, info eruncommon.BuildInfo, runtime RuntimeConfig) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "version",
		Description: "Return build metadata for the current erun binary",
	}, versionTool(info))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list",
		Description: "List configured tenants and environments, defaults, and the effective target for the current runtime directory",
	}, listTool(runtime))
}

// registerIdleStopTools registers the idle status and auto-stop audit tools.
func registerIdleStopTools(server *mcp.Server, runtime RuntimeConfig) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "idle",
		Description: "Return environment idle stop timeout and marker status without recording activity",
	}, idleTool(runtime))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "idle_stop_cancel",
		Description: "Dismiss the pending auto-stop grace warning for the env without touching AWS state. No-op when no warning is armed.",
	}, idleStopCancelTool(runtime))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "idle_stop_history",
		Description: "Return the last N (cap 10) auto-stop audit entries for the env, newest first. Each row carries the per-marker idle/active breakdown captured when the auto-stop grace was armed.",
	}, idleStopHistoryTool(runtime))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "idle_stop_record",
		Description: "Record a host-driven stop entry in stop-history.json (source=host-manual). Called by the desktop's Stop button after the AWS stop succeeds, so the History tab can also explain 'you clicked Stop' alongside the in-pod monitor's auto-stops.",
	}, idleStopRecordTool(runtime))
}

// registerCloudTools registers the cloud-provider-alias and AWS-credential
// tools.
func registerCloudTools(server *mcp.Server, runtime RuntimeConfig) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "cloud_list",
		Description: "List configured root-level cloud provider aliases and token status",
	}, cloudListTool(runtime))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "cloud_init_aws",
		Description: "Initialize an AWS SSO cloud provider alias in root ERun config, with preview support",
	}, cloudInitAWSTool(runtime))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "cloud_login",
		Description: "Login to a configured cloud provider alias, with preview support",
	}, cloudLoginTool(runtime))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "cloud_oidc",
		Description: "Refresh the OIDC issuer for a configured cloud provider alias, with preview support",
	}, cloudOIDCTool(runtime))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "cloud_set",
		Description: "Set the cloud provider alias for a tenant environment, with preview support",
	}, cloudSetTool(runtime))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "cloud_inject_aws_credentials",
		Description: "Write temporary AWS credentials into the runtime pod's ~/.aws/credentials under the erun-host profile",
	}, cloudInjectAWSCredentialsTool())
	mcp.AddTool(server, &mcp.Tool{
		Name:        "cloud_clear_aws_credentials",
		Description: "Remove the erun-host profile from the runtime pod's ~/.aws/credentials",
	}, cloudClearAWSCredentialsTool())
}

// registerContextTools registers the managed-cloud Kubernetes context tools.
func registerContextTools(server *mcp.Server, runtime RuntimeConfig) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "context_list",
		Description: "List managed ERun cloud Kubernetes contexts",
	}, contextListTool(runtime))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "context_init",
		Description: "Initialize a managed cloud k3s Kubernetes context, with preview support",
	}, contextInitTool(runtime))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "context_stop",
		Description: "Stop a managed ERun cloud Kubernetes context, with preview support",
	}, contextStopTool(runtime))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "context_start",
		Description: "Start a managed ERun cloud Kubernetes context, with preview support",
	}, contextStartTool(runtime))
}

// registerDeliveryTools registers the init → build → push → deploy lifecycle
// tools plus upgrade, doctor, and delete.
func registerDeliveryTools(server *mcp.Server, runtime RuntimeConfig) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "init",
		Description: "Run `erun init` using the shared init flow; when more input is needed, return a structured interaction request for the caller to answer in a follow-up tool call",
	}, initTool(runtime))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "build",
		Description: "Build the project's container images for the resolved tenant/environment. The build step of the build → release → push → deploy flow: set deploy to push and roll them out, or release to stamp and publish the release version first.",
	}, buildTool(runtime))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "push",
		Description: "Build and push the project's container images to the BUILD-marked registry for the resolved tenant/environment. The push step of the build → release → push → deploy flow.",
	}, pushTool(runtime))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "deploy",
		Description: "Roll the project's charts out to the resolved tenant/environment: build and push the images they need, mirror them from the FROM to the TO registry when both roles are marked, then run the rollout with the cluster pulling from the DEPLOY registry. The deploy step of the build → release → push → deploy flow.",
	}, deployTool(runtime))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "upgrade",
		Description: "Redeploy the resolved environment to the latest version for its release channel when it is opted into Upgrade all (autoupgrade) and lags. Snapshot-channel environments adopt a stable release once one is published on top of the latest snapshot. High blast radius: rolls out a new runtime image and restarts pods. Set preview to resolve and return the plan (channel, current → target) without deploying.",
	}, upgradeTool(runtime))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "doctor",
		Description: "Diagnose and repair the resolved environment: report why a deploy may have failed (helm release status and runtime pods, read-only); recover a failing runtime release by clearing a stuck pending helm lock or rolling back to the last successful revision; prune unused Docker images, build cache, or stopped containers; and optionally restore or repair the root erun config from a backup or by re-initializing orphaned cloud provider aliases",
	}, doctorTool(runtime))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "delete",
		Description: "Delete an environment from ERun configuration and remove its remote runtime namespace after explicit tenant-environment confirmation",
	}, deleteTool(runtime))
}

// registerInspectionTools registers the repo-state and source-contribution
// tools that operate from the runtime repo root.
func registerInspectionTools(server *mcp.Server, runtime RuntimeConfig) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "diff",
		Description: "Return the current git diff from the runtime repo root as raw text plus structured file, hunk, line, and tree data",
	}, diffTool(runtime))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "raw",
		Description: "Run an arbitrary command from the runtime repo root and return captured stdout, stderr, and trace output",
	}, rawTool(runtime))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "outputs_list",
		Description: "List the files and folders an agent produced in the runtime pod's outputs directory ($ERUN_OUTPUTS_DIR, default /home/erun/.erun/outputs), newest-first. Read-only.",
	}, outputsListTool())
	mcp.AddTool(server, &mcp.Tool{
		Name:        "outputs_download",
		Description: "Read one entry from the runtime pod's outputs directory and return its bytes inline as base64 (a folder as a tar.gz archive). The server runs in the pod, so it returns the content directly for the caller to save. Set preview to return name/type/size without the bytes.",
	}, outputsDownloadTool())
	mcp.AddTool(server, &mcp.Tool{
		Name:        "release",
		Description: "Plan and execute a project release from the runtime repo root using .erun/config.yaml branch policy",
	}, releaseTool(runtime))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "contribute_clone",
		Description: "Clone the ERun source repository into $HOME/git/erun inside the environment so contribute-mode tabs can build and run a local ERun checkout",
	}, contributeCloneTool(runtime))
}
