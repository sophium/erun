package expose

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/command/expose"
	"github.com/sophium/erun/erun-mcp/tools/shared"
)

type UnexposeInput struct {
	Tenant             string `json:"tenant,omitempty" jsonschema:"tenant name; defaults to the server tenant context, and must match it: this server only acts on its own environment"`
	Environment        string `json:"environment,omitempty" jsonschema:"environment name; defaults to the server environment context, and must match it: this server only acts on its own environment"`
	ProjectRoot        string `json:"projectRoot,omitempty" jsonschema:"project root holding the platform config (.erun/config.yaml); defaults to the runtime repo path"`
	Preview            bool   `json:"preview,omitempty" jsonschema:"when true, resolve and print the planned action without executing it"`
	Verbosity          int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
	SkipIfUnconfigured bool   `json:"skipIfUnconfigured,omitempty" jsonschema:"succeed as a no-op instead of failing when the project declares no platform block"`
	ServicesZone       string `json:"servicesZone,omitempty" jsonschema:"override the platform services zone tenant hostnames live under, so unexpose needs no project (requires platformNamespace too)"`
	PlatformNamespace  string `json:"platformNamespace,omitempty" jsonschema:"override the namespace running the platform's PowerDNS singleton, so unexpose needs no project (requires servicesZone too)"`
	ErunAlias          string `json:"erunAlias,omitempty" jsonschema:"erun platform cloud alias to route the DNS delete through when direct PowerDNS access is unavailable (defaults to the sole configured erun-type alias)"`
	shared.JobEnvelopeInput
}

func UnexposeTool(runtime shared.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, UnexposeInput) (*mcp.CallToolResult, shared.JobEnvelopeOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input UnexposeInput) (*mcp.CallToolResult, shared.JobEnvelopeOutput, error) {
		tenant, environment, err := shared.ResolveLocalTarget(runtime, input.Tenant, input.Environment)
		if err != nil {
			return nil, shared.JobEnvelopeOutput{}, err
		}
		projectRoot := shared.FirstNonEmpty(strings.TrimSpace(input.ProjectRoot), strings.TrimSpace(runtime.Context.RepoPath))

		exposeStore, ok := any(runtime.Store).(expose.ExposeStore)
		if !ok {
			exposeStore = config.ConfigStore{}
		}

		execute := shared.SimpleJobExecute(runtime, input.Verbosity, func(runCtx core.Context, _ string) error {
			runCtx.MCPTool = "unexpose"
			_, err := expose.RunUnexposeService(runCtx, expose.UnexposeParams{
				Tenant:             tenant,
				Environment:        environment,
				ProjectRoot:        projectRoot,
				SkipIfUnconfigured: input.SkipIfUnconfigured,
				ServicesZone:       strings.TrimSpace(input.ServicesZone),
				PlatformNamespace:  strings.TrimSpace(input.PlatformNamespace),
				ErunAlias:          strings.TrimSpace(input.ErunAlias),
			}, exposeStore, runtime.Store, shared.CloudDependencies(), nil)
			return err
		})
		envelope, err := shared.RunJobEnvelope(runtime, "unexpose", input.JobEnvelopeInput, input.Preview, execute)
		return nil, envelope, err
	}
}
