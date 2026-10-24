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

// RunHTTP serves the MCP edge and the Prometheus metrics listener
// concurrently until ctx is cancelled. Either one failing (a bind error, most
// often) cancels the other and its error wins, rather than the metrics
// listener silently dying while the MCP edge keeps the process alive for the
// rest of the pod's lifetime.
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

	// One server per distinct capability set, resolved per request: a caller
	// sees exactly the tools it may call, and `tools/list` is the same answer as
	// what it is allowed to do rather than a menu with locked entries.
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
	// CORS sits outside auth: a browser preflight carries no Authorization
	// header at all, so gating it on auth would fail every cross-origin caller
	// before the real request is even sent. The bearer token, checked just
	// inside it, is the real gate here -- same stance as the attach
	// handler's permissive CheckOrigin. Auth is the outermost *authorization*
	// layer, so even idle probes must carry a valid token before any tool
	// runs. Traffic metering sits just inside it so a rejected call's tiny
	// response still counts, but wraps the real MCP traffic that
	// erun_traffic_window_bytes exists to measure.
	mux.Handle(cfg.Path, corsMiddleware(shared.AuthHTTPMiddleware(authCfg, trafficMeteringMiddleware(recorder, activityHTTPMiddleware(runtime, handler)))))
	// The WebSocket attach edge is not wrapped in trafficMeteringMiddleware:
	// that wrapper's byteCountingResponseWriter does not implement
	// http.Hijacker, and the WebSocket upgrade needs to hijack the
	// connection. It shares the same auth middleware, so a caller still needs
	// a valid token before the handler ever inspects capabilities or upgrades.
	attach.RegisterAttachHandler(mux, cfg.Path+"/attach/{session}", authCfg, runtime)
	return mux
}

// crossOriginProtectionForAuthenticatedEdge exempts this edge's one served
// path from the MCP SDK's default same-origin CSRF guard
// (net/http.CrossOriginProtection, on by default since go-sdk v1.4.1 --
// NewStreamableHTTPHandler installs a zero-value one whenever the caller
// leaves this option nil). That guard exists to stop a forged cross-site
// request from riding a browser's *ambient* credentials -- a cookie the
// browser attaches automatically. This edge has none: authHTTPMiddleware
// already requires a bearer token in the Authorization header on every
// request before it ever reaches this handler, and a cross-site page cannot
// forge that header without already knowing the secret it protects -- at
// which point origin is not doing any protective work left to do. Left at
// its default, the guard 403s every legitimate cross-origin call this edge
// exists to serve (the hosted console calling an environment's exposed MCP
// hostname from a different origin) with "cross-origin request detected
// from Sec-Fetch-Site header", regardless of corsMiddleware's own
// reflected-Origin CORS headers below -- that middleware controls whether
// the browser lets the caller's script *read* the response; this guard runs
// deeper inside the SDK's own handler and rejects the request outright
// before corsMiddleware's headers are even relevant.
func crossOriginProtectionForAuthenticatedEdge(path string) *http.CrossOriginProtection {
	protection := http.NewCrossOriginProtection()
	protection.AddInsecureBypassPattern(path)
	return protection
}

// corsMiddleware unblocks a browser calling this edge directly from a
// different origin (the hosted console calling an env's exposed MCP
// hostname), which is otherwise refused by the browser itself before any
// request reaches this server. It reflects the caller's Origin rather than a
// fixed allowlist because a PaaS instance's console hostname is runtime
// config, not something this binary can bake in; the bearer token
// authHTTPMiddleware requires next is what actually gates the request.
// Mcp-Session-Id must be exposed explicitly -- fetch() hides response
// headers from the caller's script by default unless the server lists them.
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

// mcpSessionTimeout is how long the SDK keeps a session it has heard no POST
// from. It is the edge's own idle-reap budget, and it has to stay in step with
// the keepalive that renews a session a caller is still attached to
// (session_keepalive.go), so the two read from one value rather than two.
var mcpSessionTimeout = 5 * time.Minute

// mcpServerLogger receives the MCP SDK's own server-side diagnostics. It is a
// variable rather than a literal so a test can read what would otherwise go
// straight to the pod's stderr.
//
// Leaving it nil is not "no logging configured"; it is
// slog.New(slog.DiscardHandler) (mcp/ensureLogger), which silently drops every
// diagnostic the SDK raises — including the "method %q is invalid during
// session initialization" guard a client hits when it calls a tool on a
// session the edge no longer knows. That is how a pod log containing zero
// occurrences of the guard error was read as "those calls never arrived", when
// the calls had arrived and been refused: the silence was this logger, not the
// absence of the request.
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

// registerRemovedTools gives every retired tool name in erun-common's
// removed-tool table a stub, instead of leaving the name unregistered. An
// unregistered name fails at the SDK's own routing layer with a bare "unknown
// tool", which names a problem and no action the caller can take; the stub
// registered here instead fails with the guidance naming the tool that took
// over its capability. The input type accepts any arguments so a caller still
// shaped for the retired tool's own schema reaches this handler rather than
// failing schema validation first.
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
