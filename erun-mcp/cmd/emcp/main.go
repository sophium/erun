package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	eruncommon "github.com/sophium/erun/erun-common"
	erunmcp "github.com/sophium/erun/erun-mcp"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stderr, erunmcp.CurrentBuildInfo(), erunmcp.RunHTTP))
}

type serverRunner func(context.Context, eruncommon.BuildInfo, erunmcp.HTTPConfig, erunmcp.RuntimeConfig) error

func run(args []string, stderr io.Writer, info eruncommon.BuildInfo, runServer serverRunner) int {
	flags := flag.NewFlagSet("emcp", flag.ContinueOnError)
	flags.SetOutput(stderr)

	cfg := erunmcp.HTTPConfig{}
	runtime := erunmcp.RuntimeConfig{}
	flags.StringVar(&cfg.Host, "host", erunmcp.DefaultHost, "Host interface to bind the MCP HTTP server to")
	flags.IntVar(&cfg.Port, "port", erunmcp.DefaultPort, "Port to bind the MCP HTTP server to")
	flags.StringVar(&cfg.Path, "path", erunmcp.DefaultPath, "HTTP path to serve the MCP endpoint from")
	flags.StringVar(&runtime.Context.Tenant, "tenant", "", "Resolved tenant context for MCP operations")
	flags.StringVar(&runtime.Context.Environment, "environment", "", "Resolved environment context for MCP operations")
	flags.StringVar(&runtime.Context.RepoPath, "repo-path", "", "Resolved repo path for MCP operations")
	flags.StringVar(&runtime.Context.KubernetesContext, "kubernetes-context", "", "Resolved kubernetes context for MCP operations")
	flags.StringVar(&runtime.Context.Namespace, "namespace", "", "Resolved kubernetes namespace for MCP operations")

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	_, _ = fmt.Fprintf(stderr, "erun mcp listening on %s:%d%s tenant=%q environment=%q namespace=%q\n", cfg.Host, cfg.Port, cfg.Path, runtime.Context.Tenant, runtime.Context.Environment, runtime.Context.Namespace)
	if err := runServer(ctx, info, cfg, runtime); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
