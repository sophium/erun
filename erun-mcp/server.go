package erunmcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	mcppkg "github.com/sophium/erun/erun-common/infra/mcp"
	"github.com/sophium/erun/erun-common/infra/mcptools"
	"github.com/sophium/erun/erun-mcp/tools/attach"
	"github.com/sophium/erun/erun-mcp/tools/environment"
	"github.com/sophium/erun/erun-mcp/tools/list"
	"github.com/sophium/erun/erun-mcp/tools/shared"
	"github.com/sophium/erun/erun-mcp/tools/version"
	"github.com/sophium/erun/erun-mcp/tools/whip"
)

const (
	DefaultHost = "127.0.0.1"
	DefaultPort = config.MCPServicePort
	DefaultPath = mcppkg.MCPServerPath
)

type HTTPConfig struct {
	Host string
	Port int
	Path string
}

// RunHTTP serves the MCP edge and the metrics listener until ctx is cancelled;
// either one failing cancels the other.
func RunHTTP(ctx context.Context, info model.BuildInfo, cfg HTTPConfig, metricsCfg MetricsConfig, runtime shared.RuntimeConfig) error {
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

func runMCPHTTP(ctx context.Context, info model.BuildInfo, cfg HTTPConfig, runtime shared.RuntimeConfig, recorder *shared.MetricsRecorder) error {
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

func newHTTPHandler(info model.BuildInfo, cfg HTTPConfig, runtime shared.RuntimeConfig, recorder *shared.MetricsRecorder) http.Handler {
	cfg, _ = normalizeHTTPConfig(cfg)

	// One server per capability set, so tools/list shows exactly what the caller may call.
	servers := newCapabilityServerCache(func(identity shared.AuthIdentity) *mcp.Server {
		return newServer(info, runtime, identity, recorder)
	})
	streamable := mcp.NewStreamableHTTPHandler(servers.serverFor, &mcp.StreamableHTTPOptions{
		JSONResponse:          true,
		SessionTimeout:        mcpSessionTimeout,
		CrossOriginProtection: crossOriginProtectionForAuthenticatedEdge(cfg.Path),
	})
	handler := withSessionKeepAlive(streamable, cfg.Path, mcpSessionTimeout)

	authCfg := shared.McpAuthConfigFromEnv()
	mux := http.NewServeMux()
	// CORS sits outside auth because a preflight carries no Authorization header;
	// traffic metering sits inside auth so rejected calls still count.
	mux.Handle(cfg.Path, corsMiddleware(shared.AuthHTTPMiddleware(authCfg, trafficMeteringMiddleware(recorder, activityHTTPMiddleware(runtime, handler)))))
	// Not metered: byteCountingResponseWriter does not implement http.Hijacker,
	// which the WebSocket upgrade needs.
	attach.RegisterAttachHandler(mux, cfg.Path+"/attach/{session}", authCfg, runtime)
	return mux
}

// crossOriginProtectionForAuthenticatedEdge exempts path from the SDK's
// same-origin CSRF guard: every request already needs a bearer token, which a
// cross-site page cannot forge, and the guard would 403 the hosted console.
func crossOriginProtectionForAuthenticatedEdge(path string) *http.CrossOriginProtection {
	protection := http.NewCrossOriginProtection()
	protection.AddInsecureBypassPattern(path)
	return protection
}

// corsMiddleware reflects the caller's Origin, since the console hostname is
// runtime config; the bearer token is the real gate. Mcp-Session-Id must be
// exposed explicitly because fetch() hides unlisted response headers.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if origin := req.Header.Get("Origin"); origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Expose-Headers", "Mcp-Session-Id")
		}
		if req.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Accept, Mcp-Session-Id")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, req)
	})
}

func activityHTTPMiddleware(runtime shared.RuntimeConfig, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get(mcppkg.MCPIdleProbeHeader) == "true" {
			next.ServeHTTP(w, req)
			return
		}
		shared.RecordRuntimeActivity(runtime, config.ActivityKindMCP, false)
		if requestLooksLikeCodex(req) {
			shared.RecordRuntimeActivity(runtime, config.ActivityKindCodex, false)
		}
		next.ServeHTTP(w, req)
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

// mcpSessionTimeout is the SDK's idle-reap budget, shared with the session keepalive.
var mcpSessionTimeout = 5 * time.Minute

// mcpServerLogger receives the MCP SDK's diagnostics; nil would silently
// discard them.
var mcpServerLogger = slog.New(slog.NewTextHandler(os.Stderr, nil))

func newServer(info model.BuildInfo, runtime shared.RuntimeConfig, identity shared.AuthIdentity, metrics *shared.MetricsRecorder) *mcp.Server {
	info = model.NormalizeBuildInfo(info)
	runtime = normalizeRuntimeConfig(runtime)

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "erun",
		Version: info.Version,
	}, &mcp.ServerOptions{Logger: mcpServerLogger})

	reg := shared.ToolRegistrar{Server: server, Identity: identity, Metrics: metrics}
	registerReadModelTools(reg, info, runtime)
	registerIdleStopTools(reg, runtime)
	whip.RegisterWhipTools(reg, runtime)
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

// registerRemovedTools stubs each retired tool name so callers get guidance
// naming its replacement instead of a bare "unknown tool". The stub accepts any
// arguments so an old-shaped call reaches it instead of failing validation.
func registerRemovedTools(reg shared.ToolRegistrar) {
	for name, guidance := range mcptools.MCPRemovedTools() {
		shared.AddTool(reg, &mcp.Tool{
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

func registerReadModelTools(reg shared.ToolRegistrar, info model.BuildInfo, runtime shared.RuntimeConfig) {
	shared.AddTool(reg, &mcp.Tool{
		Name:        "version",
		Description: "Return build metadata for the current erun binary",
	}, version.VersionTool(info))
	shared.AddTool(reg, &mcp.Tool{
		Name: "list",
		Description: "List configured tenants and environments, defaults, and the effective target for the current runtime directory. " +
			"Pass versionDriftTenant to additionally report erun-version drift across that tenant's environments, and gateEnvironment to flag whether the environment driving that tenant's merge-queue gate is running an older erun version than any environment it gates.",
	}, list.ListTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name: "environment",
		Description: "Return this environment's read model: its list-style summary (name, type, RuntimeVersion, default/effective markers), a resolved lifecycle state (running, idle, deploy-failed, stopped, or unknown), its idle policy + activity snapshot, its cloud-context config, and a doctor deploy diagnosis -- one call composing list/idle/doctor instead of three. " +
			"Cloud-context power state is never refreshed live (no AWS credentials reach inside this pod), so a managed-cloud environment's state reads unknown rather than a guess unless something else already refreshed it. " +
			"Pass preview=true to skip the live helm/kubectl deploy diagnosis.",
	}, environment.EnvironmentTool(runtime))
}
