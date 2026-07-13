package erunmcp

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

type ExposeInput struct {
	Tenant       string `json:"tenant,omitempty" jsonschema:"tenant name; defaults to the MCP runtime context tenant"`
	Environment  string `json:"environment,omitempty" jsonschema:"environment name; defaults to the MCP runtime context environment"`
	Service      string `json:"service" jsonschema:"required name of the in-namespace Service to expose at a public hostname"`
	ProjectRoot  string `json:"projectRoot,omitempty" jsonschema:"project root holding the platform config (.erun/config.yaml); defaults to the runtime repo path"`
	IP           string `json:"ip" jsonschema:"required ingress IP the per-env wildcard record points at (e.g. 127.0.0.1 for a local cluster, the public LB IP for remote)"`
	Port         int    `json:"port,omitempty" jsonschema:"Service port to route to (default 80)"`
	NoTLS        bool   `json:"noTls,omitempty" jsonschema:"serve http instead of https; by default the Ingress references the env's per-env wildcard cert Secret (<tenant>-<env>-wildcard-tls) so the host serves https"`
	IngressClass string `json:"ingressClass,omitempty" jsonschema:"ingress controller class (default traefik)"`
	TLSSecret    string `json:"tlsSecret,omitempty" jsonschema:"override the per-env wildcard cert Secret name (default <tenant>-<env>-wildcard-tls)"`
	Preview      bool   `json:"preview,omitempty" jsonschema:"when true, resolve and print the planned actions without executing them"`
	Verbosity    int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
}

func exposeTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ExposeInput) (*mcp.CallToolResult, CommandOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ExposeInput) (*mcp.CallToolResult, CommandOutput, error) {
		tenant := firstNonEmpty(strings.TrimSpace(input.Tenant), strings.TrimSpace(runtime.Context.Tenant))
		environment := firstNonEmpty(strings.TrimSpace(input.Environment), strings.TrimSpace(runtime.Context.Environment))
		projectRoot := firstNonEmpty(strings.TrimSpace(input.ProjectRoot), strings.TrimSpace(runtime.Context.RepoPath))

		exposeStore, ok := any(runtime.Store).(eruncommon.ExposeStore)
		if !ok {
			exposeStore = eruncommon.ConfigStore{}
		}

		output, err := runRuntimeCommand(runtime, input.Preview, input.Verbosity, func(runCtx eruncommon.Context, _ string) error {
			_, err := eruncommon.RunExposeService(runCtx, eruncommon.ExposeServiceParams{
				Tenant:        tenant,
				Environment:   environment,
				Service:       strings.TrimSpace(input.Service),
				ProjectRoot:   projectRoot,
				TargetIP:      strings.TrimSpace(input.IP),
				ServicePort:   input.Port,
				NoTLS:         input.NoTLS,
				IngressClass:  strings.TrimSpace(input.IngressClass),
				TLSSecretName: strings.TrimSpace(input.TLSSecret),
			}, exposeStore, nil, nil)
			return err
		})
		return nil, output, err
	}
}
