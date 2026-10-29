package expose

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/seam/envstore"
	"github.com/sophium/erun/erun-common/command/expose"
	"github.com/sophium/erun/erun-mcp/tools/shared/clouddeps"
	"github.com/sophium/erun/erun-mcp/tools/shared/jobenvelope"
	"github.com/sophium/erun/erun-mcp/tools/shared/runcmd"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimecfg"
	"github.com/sophium/erun/erun-mcp/tools/shared/toolio"
)

type ExposeInput struct {
	Tenant      string `json:"tenant,omitempty" jsonschema:"tenant name; defaults to the server tenant context, and must match it: this server only acts on its own environment"`
	Environment string `json:"environment,omitempty" jsonschema:"environment name; defaults to the server environment context, and must match it: this server only acts on its own environment"`
	Service     string `json:"service" jsonschema:"required logical service name; becomes the hostname label and routes to the tenant-scoped in-namespace Service <tenant>-<service> (e.g. api -> frs-api), unless backendService names the Service explicitly"`
	// BackendService overrides the derived <tenant>-<service> name for repos with
	// their own chart; a wrong name yields an ingress that 503s.
	BackendService     string `json:"backendService,omitempty" jsonschema:"in-namespace Service the Ingress routes to, when it is not <tenant>-<service>; the services tool lists the candidates"`
	ProjectRoot        string `json:"projectRoot,omitempty" jsonschema:"project root holding the platform config (.erun/config.yaml); defaults to the runtime repo path"`
	IP                 string `json:"ip" jsonschema:"required ingress IP the per-env wildcard record points at (e.g. 127.0.0.1 for a local cluster, the public LB IP for remote)"`
	Port               int    `json:"port,omitempty" jsonschema:"Service port to route to (default 80)"`
	NoTLS              bool   `json:"noTls,omitempty" jsonschema:"serve http instead of https; https is requested by default but only takes effect when dns01TokenFile/dns01BrokerUrl/acmeEmail are all set, since nothing else provisions the env's per-env wildcard cert Secret"`
	IngressClass       string `json:"ingressClass,omitempty" jsonschema:"ingress controller class (default traefik)"`
	TLSSecret          string `json:"tlsSecret,omitempty" jsonschema:"override the per-env wildcard cert Secret name (default <tenant>-<env>-wildcard-tls)"`
	Preview            bool   `json:"preview,omitempty" jsonschema:"when true, resolve and print the planned actions without executing them"`
	Verbosity          int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
	SkipIfUnconfigured bool   `json:"skipIfUnconfigured,omitempty" jsonschema:"succeed as a no-op instead of failing when the project declares no platform block"`
	ServicesZone       string `json:"servicesZone,omitempty" jsonschema:"override the platform services zone tenant hostnames live under, so expose needs no project (requires platformNamespace too)"`
	PlatformNamespace  string `json:"platformNamespace,omitempty" jsonschema:"override the namespace running the platform's PowerDNS singleton, so expose needs no project (requires servicesZone too)"`
	// Leaving any of DNS01TokenFile/DNS01BrokerURL/ACMEEmail empty makes the
	// Ingress http-only rather than referencing a Secret nothing populates.
	DNS01TokenFile        string `json:"dns01TokenFile,omitempty" jsonschema:"path to a file holding the per-env DNS-01 broker token; with dns01BrokerUrl and acmeEmail, provisions a namespaced cert-manager Issuer + Certificate"`
	DNS01BrokerURL        string `json:"dns01BrokerUrl,omitempty" jsonschema:"base URL of the DNS-01 broker the cluster's cert-manager webhook shim forwards challenges to (requires dns01TokenFile and acmeEmail)"`
	ACMEEmail             string `json:"acmeEmail,omitempty" jsonschema:"ACME account contact email for the provisioned per-env certificate (requires dns01TokenFile and dns01BrokerUrl)"`
	ACMEServer            string `json:"acmeServer,omitempty" jsonschema:"ACME directory URL for the provisioned per-env certificate (default Let's Encrypt production)"`
	DNS01WebhookGroupName string `json:"dns01WebhookGroupName,omitempty" jsonschema:"API group the cluster's cert-manager DNS-01 webhook shim registers under (default acme.erun.io)"`
	ErunAlias             string `json:"erunAlias,omitempty" jsonschema:"erun platform cloud alias to route the DNS write through when direct PowerDNS access is unavailable (defaults to the sole configured erun-type alias)"`
	jobenvelope.JobEnvelopeInput
}

func ExposeTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ExposeInput) (*mcp.CallToolResult, jobenvelope.JobEnvelopeOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ExposeInput) (*mcp.CallToolResult, jobenvelope.JobEnvelopeOutput, error) {
		tenant, environment, err := runtimecfg.ResolveLocalTarget(runtime, input.Tenant, input.Environment)
		if err != nil {
			return nil, jobenvelope.JobEnvelopeOutput{}, err
		}
		projectRoot := toolio.FirstNonEmpty(strings.TrimSpace(input.ProjectRoot), strings.TrimSpace(runtime.Context.RepoPath))

		exposeStore, ok := any(runtime.Store).(envstore.ExposeStore)
		if !ok {
			exposeStore = config.ConfigStore{}
		}

		execute := runcmd.SimpleJobExecute(runtime, input.Verbosity, func(runCtx core.Context, _ string) error {
			runCtx.MCPTool = "expose"
			_, err := expose.RunExposeService(runCtx, expose.ExposeServiceParams{
				Tenant:             tenant,
				Environment:        environment,
				Service:            strings.TrimSpace(input.Service),
				ProjectRoot:        projectRoot,
				TargetIP:           strings.TrimSpace(input.IP),
				BackendService:     strings.TrimSpace(input.BackendService),
				ServicePort:        input.Port,
				NoTLS:              input.NoTLS,
				IngressClass:       strings.TrimSpace(input.IngressClass),
				TLSSecretName:      strings.TrimSpace(input.TLSSecret),
				SkipIfUnconfigured: input.SkipIfUnconfigured,
				ServicesZone:       strings.TrimSpace(input.ServicesZone),
				PlatformNamespace:  strings.TrimSpace(input.PlatformNamespace),
				TLS: expose.TLSCertParams{
					DNS01TokenPath:        strings.TrimSpace(input.DNS01TokenFile),
					DNS01BrokerURL:        strings.TrimSpace(input.DNS01BrokerURL),
					DNS01WebhookGroupName: strings.TrimSpace(input.DNS01WebhookGroupName),
					ACMEEmail:             strings.TrimSpace(input.ACMEEmail),
					ACMEServer:            strings.TrimSpace(input.ACMEServer),
				},
				ErunAlias: strings.TrimSpace(input.ErunAlias),
			}, exposeStore, runtime.Store, clouddeps.CloudDependencies(), nil, nil)
			return err
		})
		envelope, err := jobenvelope.RunJobEnvelope(runtime, "expose", input.JobEnvelopeInput, input.Preview, execute)
		return nil, envelope, err
	}
}
