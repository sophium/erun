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
	Service      string `json:"service" jsonschema:"required logical service name; becomes the hostname label and routes to the tenant-scoped in-namespace Service <tenant>-<service> (e.g. api -> frs-api)"`
	ProjectRoot  string `json:"projectRoot,omitempty" jsonschema:"project root holding the platform config (.erun/config.yaml); defaults to the runtime repo path"`
	IP           string `json:"ip" jsonschema:"required ingress IP the per-env wildcard record points at (e.g. 127.0.0.1 for a local cluster, the public LB IP for remote)"`
	Port         int    `json:"port,omitempty" jsonschema:"Service port to route to (default 80)"`
	NoTLS        bool   `json:"noTls,omitempty" jsonschema:"serve http instead of https; by default the Ingress references the env's per-env wildcard cert Secret (<tenant>-<env>-wildcard-tls) so the host serves https"`
	IngressClass string `json:"ingressClass,omitempty" jsonschema:"ingress controller class (default traefik)"`
	TLSSecret    string `json:"tlsSecret,omitempty" jsonschema:"override the per-env wildcard cert Secret name (default <tenant>-<env>-wildcard-tls)"`
	Preview      bool   `json:"preview,omitempty" jsonschema:"when true, resolve and print the planned actions without executing them"`
	Verbosity    int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
	// SkipIfUnconfigured mirrors the CLI's --skip-if-unconfigured: succeed as a
	// no-op instead of failing when the project declares no platform block, for
	// an Agent composing expose after deploy without knowing whether the target
	// is a platform deployment.
	SkipIfUnconfigured bool `json:"skipIfUnconfigured,omitempty" jsonschema:"succeed as a no-op instead of failing when the project declares no platform block"`
	// ServicesZone/PlatformNamespace mirror the CLI's --services-zone/
	// --platform-namespace: an explicit override for the platform coordinates
	// expose would otherwise read from ProjectRoot, for a caller with no project
	// to resolve.
	ServicesZone      string `json:"servicesZone,omitempty" jsonschema:"override the platform services zone tenant hostnames live under, so expose needs no project (requires platformNamespace too)"`
	PlatformNamespace string `json:"platformNamespace,omitempty" jsonschema:"override the namespace running the platform's PowerDNS singleton, so expose needs no project (requires servicesZone too)"`
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
				Tenant:             tenant,
				Environment:        environment,
				Service:            strings.TrimSpace(input.Service),
				ProjectRoot:        projectRoot,
				TargetIP:           strings.TrimSpace(input.IP),
				ServicePort:        input.Port,
				NoTLS:              input.NoTLS,
				IngressClass:       strings.TrimSpace(input.IngressClass),
				TLSSecretName:      strings.TrimSpace(input.TLSSecret),
				SkipIfUnconfigured: input.SkipIfUnconfigured,
				ServicesZone:       strings.TrimSpace(input.ServicesZone),
				PlatformNamespace:  strings.TrimSpace(input.PlatformNamespace),
			}, exposeStore, nil, nil)
			return err
		})
		return nil, output, err
	}
}
