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

type VersionOutput struct {
	Version string `json:"version"`
	Commit  string `json:"commit,omitempty"`
	Date    string `json:"date,omitempty"`
}

func RunHTTP(ctx context.Context, info eruncommon.BuildInfo, cfg HTTPConfig) error {
	cfg, err := NormalizeHTTPConfig(cfg)
	if err != nil {
		return err
	}

	server := &http.Server{
		Addr:              ListenAddress(cfg),
		Handler:           NewHTTPHandler(info, cfg),
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

func NewHTTPHandler(info eruncommon.BuildInfo, cfg HTTPConfig) http.Handler {
	cfg, _ = NormalizeHTTPConfig(cfg)

	server := NewServer(info)
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

func NormalizeHTTPConfig(cfg HTTPConfig) (HTTPConfig, error) {
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

func ListenAddress(cfg HTTPConfig) string {
	return net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
}

func EndpointURL(cfg HTTPConfig) string {
	cfg, _ = NormalizeHTTPConfig(cfg)
	return "http://" + ListenAddress(cfg) + cfg.Path
}

func NewServer(info eruncommon.BuildInfo) *mcp.Server {
	info = eruncommon.NormalizeBuildInfo(info)

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "erun",
		Version: info.Version,
	}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "version",
		Description: "Return build metadata for the current erun binary",
	}, versionTool(info))

	return server
}

func versionTool(info eruncommon.BuildInfo) func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, VersionOutput, error) {
	output := buildVersionOutput(info)
	return func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, VersionOutput, error) {
		return nil, output, nil
	}
}

func buildVersionOutput(info eruncommon.BuildInfo) VersionOutput {
	info = eruncommon.NormalizeBuildInfo(info)
	return VersionOutput{
		Version: info.Version,
		Commit:  info.Commit,
		Date:    info.Date,
	}
}
