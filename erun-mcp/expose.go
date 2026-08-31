package erunmcp

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

type ExposeInput struct {
	Tenant       string `json:"tenant,omitempty" jsonschema:"tenant name; defaults to the server tenant context, and must match it: this server only acts on its own environment"`
	Environment  string `json:"environment,omitempty" jsonschema:"environment name; defaults to the server environment context, and must match it: this server only acts on its own environment"`
	Service      string `json:"service" jsonschema:"required logical service name; becomes the hostname label and routes to the tenant-scoped in-namespace Service <tenant>-<service> (e.g. api -> frs-api)"`
	ProjectRoot  string `json:"projectRoot,omitempty" jsonschema:"project root holding the platform config (.erun/config.yaml); defaults to the runtime repo path"`
	IP           string `json:"ip" jsonschema:"required ingress IP the per-env wildcard record points at (e.g. 127.0.0.1 for a local cluster, the public LB IP for remote)"`
	Port         int    `json:"port,omitempty" jsonschema:"Service port to route to (default 80)"`
	NoTLS        bool   `json:"noTls,omitempty" jsonschema:"serve http instead of https; https is requested by default but only takes effect when dns01TokenFile/dns01BrokerUrl/acmeEmail are all set, since nothing else provisions the env's per-env wildcard cert Secret"`
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
	// DNS01TokenFile/DNS01BrokerURL/ACMEEmail/ACMEServer/DNS01WebhookGroupName
	// provision the env's per-env wildcard TLS certificate through erun's
	// DNS-01 broker so the Ingress's wildcard TLS secret actually gets
	// populated. Leave any of the first three empty and the Ingress resolves
	// to http-only instead — the same posture NoTLS asks for explicitly —
	// rather than referencing a Secret nothing will ever populate.
	DNS01TokenFile        string `json:"dns01TokenFile,omitempty" jsonschema:"path to a file holding the per-env DNS-01 broker token; with dns01BrokerUrl and acmeEmail, provisions a namespaced cert-manager Issuer + Certificate"`
	DNS01BrokerURL        string `json:"dns01BrokerUrl,omitempty" jsonschema:"base URL of the DNS-01 broker the cluster's cert-manager webhook shim forwards challenges to (requires dns01TokenFile and acmeEmail)"`
	ACMEEmail             string `json:"acmeEmail,omitempty" jsonschema:"ACME account contact email for the provisioned per-env certificate (requires dns01TokenFile and dns01BrokerUrl)"`
	ACMEServer            string `json:"acmeServer,omitempty" jsonschema:"ACME directory URL for the provisioned per-env certificate (default Let's Encrypt production)"`
	DNS01WebhookGroupName string `json:"dns01WebhookGroupName,omitempty" jsonschema:"API group the cluster's cert-manager DNS-01 webhook shim registers under (default acme.erun.io)"`
	Wait                  *bool  `json:"wait,omitempty" jsonschema:"when true (the default this release), run synchronously and return the full result inline, exactly as before this input existed. Set false to start the work as a background job and get back {jobId, state: running} immediately instead -- poll exec_job_status/exec_job_await/exec_job_output for the outcome. This default flips to false in a future release, with true kept callable for one more release as the compatibility switch"`
}

func exposeTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ExposeInput) (*mcp.CallToolResult, JobEnvelopeOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ExposeInput) (*mcp.CallToolResult, JobEnvelopeOutput, error) {
		tenant, environment, err := resolveLocalTarget(runtime, input.Tenant, input.Environment)
		if err != nil {
			return nil, JobEnvelopeOutput{}, err
		}
		projectRoot := firstNonEmpty(strings.TrimSpace(input.ProjectRoot), strings.TrimSpace(runtime.Context.RepoPath))

		exposeStore, ok := any(runtime.Store).(eruncommon.ExposeStore)
		if !ok {
			exposeStore = eruncommon.ConfigStore{}
		}

		execute := simpleJobExecute(runtime, input.Verbosity, func(runCtx eruncommon.Context, _ string) error {
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
				TLS: eruncommon.TLSCertParams{
					DNS01TokenPath:        strings.TrimSpace(input.DNS01TokenFile),
					DNS01BrokerURL:        strings.TrimSpace(input.DNS01BrokerURL),
					DNS01WebhookGroupName: strings.TrimSpace(input.DNS01WebhookGroupName),
					ACMEEmail:             strings.TrimSpace(input.ACMEEmail),
					ACMEServer:            strings.TrimSpace(input.ACMEServer),
				},
			}, exposeStore, nil, nil)
			return err
		})
		envelope, err := runJobEnvelope(runtime, "expose", input.Wait, input.Preview, execute)
		return nil, envelope, err
	}
}
