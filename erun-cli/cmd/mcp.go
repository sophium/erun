package cmd

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/internal/bootstrap"
	"github.com/spf13/cobra"
)

type mcpVersionOutput struct {
	Version string `json:"version"`
	Commit  string `json:"commit,omitempty"`
	Date    string `json:"date,omitempty"`
}

type mcpInitInput struct {
	Tenant      string `json:"tenant,omitempty" jsonschema:"tenant name to initialize"`
	ProjectRoot string `json:"project_root,omitempty" jsonschema:"project root path; if omitted erun tries to detect the nearest git repository"`
	Environment string `json:"environment,omitempty" jsonschema:"environment name to initialize; defaults to dev"`
}

type mcpInitOutput struct {
	DefaultTenant       string `json:"default_tenant"`
	Tenant              string `json:"tenant"`
	ProjectRoot         string `json:"project_root"`
	Environment         string `json:"environment"`
	CreatedERunConfig   bool   `json:"created_erun_config"`
	CreatedTenantConfig bool   `json:"created_tenant_config"`
	CreatedEnvConfig    bool   `json:"created_env_config"`
}

func NewMCPCmd(deps Dependencies) *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Run ERun as an MCP server over stdio",
		RunE: func(cmd *cobra.Command, args []string) error {
			server := NewMCPServer(deps)
			return server.Run(cmd.Context(), &mcp.StdioTransport{})
		},
	}
}

func NewMCPServer(deps Dependencies) *mcp.Server {
	version, _, _ := BuildInfo()
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "erun",
		Version: version,
	}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "version",
		Description: "Return build metadata for the current erun binary",
	}, versionTool)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "init",
		Description: "Initialize erun configuration for a project without interactive prompts",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input mcpInitInput) (*mcp.CallToolResult, mcpInitOutput, error) {
		output, err := runInitTool(deps, input)
		return nil, output, err
	})

	return server
}

func versionTool(ctx context.Context, req *mcp.CallToolRequest, input struct{}) (*mcp.CallToolResult, mcpVersionOutput, error) {
	return nil, buildVersionOutput(), nil
}

func buildVersionOutput() mcpVersionOutput {
	version, commit, date := BuildInfo()
	return mcpVersionOutput{
		Version: version,
		Commit:  commit,
		Date:    date,
	}
}

func runInitTool(deps Dependencies, input mcpInitInput) (mcpInitOutput, error) {
	service := bootstrap.Service{
		Store:           deps.Store,
		FindProjectRoot: deps.FindProjectRoot,
	}

	result, err := service.Run(bootstrap.InitRequest{
		Tenant:      input.Tenant,
		ProjectRoot: input.ProjectRoot,
		Environment: input.Environment,
		AutoApprove: true,
	})
	if err != nil {
		return mcpInitOutput{}, fmt.Errorf("init tool failed: %w", err)
	}

	return mcpInitOutput{
		DefaultTenant:       result.ERunConfig.DefaultTenant,
		Tenant:              result.TenantConfig.Name,
		ProjectRoot:         result.TenantConfig.ProjectRoot,
		Environment:         result.EnvConfig.Name,
		CreatedERunConfig:   result.CreatedERunConfig,
		CreatedTenantConfig: result.CreatedTenantConfig,
		CreatedEnvConfig:    result.CreatedEnvConfig,
	}, nil
}
