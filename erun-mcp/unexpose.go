package erunmcp

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

type UnexposeInput struct {
	Tenant      string `json:"tenant,omitempty" jsonschema:"tenant name; defaults to the MCP runtime context tenant"`
	Environment string `json:"environment,omitempty" jsonschema:"environment name; defaults to the MCP runtime context environment"`
	ProjectRoot string `json:"projectRoot,omitempty" jsonschema:"project root holding the platform config (.erun/config.yaml); defaults to the runtime repo path"`
	Preview     bool   `json:"preview,omitempty" jsonschema:"when true, resolve and print the planned action without executing it"`
	Verbosity   int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
	// SkipIfUnconfigured mirrors the CLI's --skip-if-unconfigured: succeed as a
	// no-op instead of failing when the project declares no platform block,
	// for an Agent composing unexpose after delete without knowing whether the
	// target was ever exposed at all.
	SkipIfUnconfigured bool `json:"skipIfUnconfigured,omitempty" jsonschema:"succeed as a no-op instead of failing when the project declares no platform block"`
	// ServicesZone/PlatformNamespace mirror the CLI's --services-zone/
	// --platform-namespace: an explicit override for the platform coordinates
	// unexpose would otherwise read from ProjectRoot, for a caller with no
	// project to resolve.
	ServicesZone      string `json:"servicesZone,omitempty" jsonschema:"override the platform services zone tenant hostnames live under, so unexpose needs no project (requires platformNamespace too)"`
	PlatformNamespace string `json:"platformNamespace,omitempty" jsonschema:"override the namespace running the platform's PowerDNS singleton, so unexpose needs no project (requires servicesZone too)"`
}

func unexposeTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, UnexposeInput) (*mcp.CallToolResult, CommandOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input UnexposeInput) (*mcp.CallToolResult, CommandOutput, error) {
		tenant := firstNonEmpty(strings.TrimSpace(input.Tenant), strings.TrimSpace(runtime.Context.Tenant))
		environment := firstNonEmpty(strings.TrimSpace(input.Environment), strings.TrimSpace(runtime.Context.Environment))
		projectRoot := firstNonEmpty(strings.TrimSpace(input.ProjectRoot), strings.TrimSpace(runtime.Context.RepoPath))

		exposeStore, ok := any(runtime.Store).(eruncommon.ExposeStore)
		if !ok {
			exposeStore = eruncommon.ConfigStore{}
		}

		output, err := runRuntimeCommand(runtime, input.Preview, input.Verbosity, func(runCtx eruncommon.Context, _ string) error {
			_, err := eruncommon.RunUnexposeService(runCtx, eruncommon.UnexposeParams{
				Tenant:             tenant,
				Environment:        environment,
				ProjectRoot:        projectRoot,
				SkipIfUnconfigured: input.SkipIfUnconfigured,
				ServicesZone:       strings.TrimSpace(input.ServicesZone),
				PlatformNamespace:  strings.TrimSpace(input.PlatformNamespace),
			}, exposeStore, nil)
			return err
		})
		return nil, output, err
	}
}
