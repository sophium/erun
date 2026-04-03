package cmd

import (
	"fmt"

	eruncommon "github.com/sophium/erun/erun-common"
	erunmcp "github.com/sophium/erun/erun-mcp"
	"github.com/spf13/cobra"
)

func NewMCPCmd(_ Dependencies) *cobra.Command {
	cfg := erunmcp.HTTPConfig{}

	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Run ERun as an MCP server over HTTP",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := erunmcp.NormalizeHTTPConfig(cfg)
			if err != nil {
				return err
			}
			cmd.Printf("ERun MCP listening on %s\n", erunmcp.EndpointURL(cfg))
			return erunmcp.RunHTTP(cmd.Context(), eruncommon.NormalizeBuildInfo(CurrentBuildInfo()), cfg)
		},
	}

	cmd.Flags().StringVar(&cfg.Host, "host", erunmcp.DefaultHost, "Host interface to bind the MCP HTTP server to")
	cmd.Flags().IntVar(&cfg.Port, "port", erunmcp.DefaultPort, "Port to bind the MCP HTTP server to")
	cmd.Flags().StringVar(&cfg.Path, "path", erunmcp.DefaultPath, "HTTP path to serve the MCP endpoint from")
	cmd.Example = fmt.Sprintf("  erun mcp --host %s --port %d --path %s", erunmcp.DefaultHost, erunmcp.DefaultPort, erunmcp.DefaultPath)
	return cmd
}
