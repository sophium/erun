package erunmcp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
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
	mux.Handle(cfg.Path, handler)
	return mux
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

	mcp.AddTool(server, &mcp.Tool{
		Name:        "version",
		Description: "Return build metadata for the current erun binary",
	}, versionTool(info))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list",
		Description: "List configured tenants and environments, defaults, and the effective target for the current runtime directory",
	}, listTool(runtime))
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
		Name:        "cloud_set",
		Description: "Set the cloud provider alias for a tenant environment, with preview support",
	}, cloudSetTool(runtime))
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
	mcp.AddTool(server, &mcp.Tool{
		Name:        "init",
		Description: "Run `erun init` using the shared init flow; when more input is needed, return a structured interaction request for the caller to answer in a follow-up tool call",
	}, initTool(runtime))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "build",
		Description: "Run Docker build operations from the runtime repo root in the resolved tenant/environment context",
	}, buildTool(runtime))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "push",
		Description: "Run Docker push operations from the runtime repo root in the resolved tenant/environment context",
	}, pushTool(runtime))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "deploy",
		Description: "Run `erun devops k8s deploy COMPONENT` from the runtime repo root in the resolved tenant/environment context",
	}, deployTool(runtime))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "doctor",
		Description: "Inspect the resolved DevOps runtime Docker state and optionally prune unused images, build cache, or stopped containers",
	}, doctorTool(runtime))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "delete",
		Description: "Delete an environment from ERun configuration and remove its remote runtime namespace after explicit tenant-environment confirmation",
	}, deleteTool(runtime))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "diff",
		Description: "Return the current git diff from the runtime repo root as raw text plus structured file, hunk, line, and tree data",
	}, diffTool(runtime))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "raw",
		Description: "Run an arbitrary command from the runtime repo root and return captured stdout, stderr, and trace output",
	}, rawTool(runtime))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "release",
		Description: "Plan and execute a project release from the runtime repo root using .erun/config.yaml branch policy",
	}, releaseTool(runtime))

	return server
}
