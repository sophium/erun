package eruncommon

import (
	"fmt"
	"strings"
)

// ExposeStore is the read surface RunExposeService needs to resolve the target
// environment (its namespace and kubernetes context).
type ExposeStore interface {
	LoadEnvConfig(string, string) (EnvConfig, string, error)
}

// DNSRecordUpserterFunc writes (creates or replaces) a single DNS record in the
// platform's authoritative zone via the PowerDNS API.
type DNSRecordUpserterFunc func(params DNSRecordUpsertParams) error

// DNSRecordDeleterFunc removes a single DNS record from the platform's
// authoritative zone, symmetric with DNSRecordUpserterFunc.
type DNSRecordDeleterFunc func(params DNSRecordDeleteParams) error

// IngressApplierFunc applies the Host-routing Ingress that fronts the exposed
// Service.
type IngressApplierFunc func(params IngressApplyParams) error

// DNSRecordUpsertParams is the per-env wildcard A-record write the exposure flow
// performs against the platform's PowerDNS singleton, which owns the services zone.
type DNSRecordUpsertParams struct {
	Zone               string
	Name               string
	Type               string
	TTL                int
	Value              string
	PlatformNamespace  string
	PowerDNSDeployment string
	KubernetesContext  string
}

// DNSRecordDeleteParams is the per-env wildcard A-record removal the unexpose
// flow performs against the platform's PowerDNS singleton, symmetric
// with DNSRecordUpsertParams.
type DNSRecordDeleteParams struct {
	Zone               string
	Name               string
	Type               string
	PlatformNamespace  string
	PowerDNSDeployment string
	KubernetesContext  string
}

// IngressApplyParams is the Host-routing Ingress the exposure flow applies into
// the env namespace.
type IngressApplyParams struct {
	KubernetesContext string
	Namespace         string
	Name              string
	Host              string
	ServiceName       string
	ServicePort       int
	IngressClass      string
	// TLSSecretName, when set, adds a tls: block referencing the per-env wildcard
	// cert Secret so the host serves https. Empty leaves the Ingress http-only.
	TLSSecretName string
}

// ExposeServiceParams are the inputs to exposing one in-namespace Service at a
// stable public hostname. TargetIP is the env's ingress IP the per-env wildcard A
// record points at (127.0.0.1 for a VM-based local cluster, a node/LAN IP, or the
// public LB IP for remote).
type ExposeServiceParams struct {
	Tenant      string
	Environment string
	Service     string
	ProjectRoot string
	TargetIP    string
	ServicePort int
	// BackendService is the in-namespace Service the Ingress routes to. Empty
	// keeps the <tenant>-<service> derivation, which is what a chart erun
	// scaffolded renders. A caller that picked a real Service from the
	// namespace names it here instead: a repo's own chart renders its own
	// Service name, and routing to a derived one that does not exist produces
	// a hostname that resolves and an ingress that 503s.
	BackendService string
	// NoTLS serves http instead of https. TLS is on by default: the Ingress
	// references the env's per-env wildcard cert Secret.
	NoTLS bool
	// IngressClass is the ingress controller class (default "traefik").
	IngressClass string
	// TLSSecretName overrides the per-env wildcard cert Secret name (default
	// "<tenant>-<env>-wildcard-tls", the name the cluster-edge module issues into
	// the env namespace).
	TLSSecretName string
	// SkipIfUnconfigured turns the hard failures for a missing/incomplete
	// platform block into a traced no-op success. It exists for a caller that
	// composes expose after another primitive (deploy) without knowing whether
	// the target project is a platform deployment at all — an explicit,
	// interactive `erun expose` still fails fast on a misconfigured project.
	SkipIfUnconfigured bool
	// ServicesZone and PlatformNamespace, when both set, override the platform
	// coordinates expose would otherwise read from ProjectRoot's .erun/config.yaml
	// (platform.serviceszone and platform.env respectively) — the PowerDNS
	// Deployment name is then derived from PlatformNamespace the same way it is
	// derived from platform.env. A caller that already has this information (the
	// hosted deploy Job, which runs a sourceless container with no git checkout to
	// resolve a project from) supplies it directly, the same way
	// TargetIP already carries the operator-composed --ip rather than resolving
	// one from a project. Left empty (the default), expose resolves the platform
	// block from ProjectRoot exactly as it always has.
	ServicesZone      string
	PlatformNamespace string
	// TLS provisions the env's own per-env wildcard TLS certificate through
	// erun's DNS-01 broker, so the wildcard Secret the Ingress above
	// references actually gets populated. A zero value skips TLS provisioning
	// outright: the Ingress still applies exactly as it always has.
	TLS TLSCertParams
	// ErunAlias names which configured erun-type cloud alias to route the DNS
	// write through when the platform route is used. Empty
	// resolves the sole configured erun-type alias, matching every other
	// `erun platform`/`erun review` command's own --erun-alias convention;
	// only needed to disambiguate when more than one is configured.
	ErunAlias string
}

// TLSCertParams provisions a namespaced cert-manager Issuer + Certificate into
// the env's own namespace, authorized by a per-env DNS-01 broker token that
// only ever authorizes ACME challenge writes within that env's own subzone —
// so one tenant's environment can never prove control of another's hostnames
// even though every environment's certificate is issued through the same
// central broker (see erun-backend-api/internal/dns01broker). Any field left
// empty skips provisioning outright: expose still applies the Ingress/DNS as
// before, the TLS secretName it references just never gets populated.
type TLSCertParams struct {
	// DNS01TokenPath points at a file holding the per-env broker token
	// (mcptoken.Signer.SignDNS01) the provisioned Issuer's webhook solver
	// presents to the broker. A path, like --mcp-auth-public-key, so the raw
	// token never rides through a CLI flag's argv or a process listing —
	// only read (at apply time) from the file it names.
	DNS01TokenPath string
	// DNS01BrokerURL is the broker's base URL the cluster's cert-manager
	// DNS-01 webhook shim forwards challenges to.
	DNS01BrokerURL string
	// DNS01WebhookGroupName is the API group the webhook shim registers
	// under. Empty defaults to "acme.erun.io", the shim's own default.
	DNS01WebhookGroupName string
	// ACMEEmail is the ACME account contact email. Required for provisioning
	// to run at all — an empty value is what marks TLS as unconfigured.
	ACMEEmail string
	// ACMEServer is the ACME directory URL. Empty defaults to Let's Encrypt
	// production.
	ACMEServer string
}

func (p TLSCertParams) configured() bool {
	return strings.TrimSpace(p.DNS01TokenPath) != "" && strings.TrimSpace(p.DNS01BrokerURL) != "" && strings.TrimSpace(p.ACMEEmail) != ""
}

const (
	// defaultACMEServer/defaultDNS01WebhookGroupName mirror
	// terraform-erun-cluster-edge's chart-issuer/chart-dns01-webhook defaults,
	// so a caller that leaves them unset gets the same behavior the one-time
	// platform edge setup does.
	defaultACMEServer            = "https://acme-v02.api.letsencrypt.org/directory"
	defaultDNS01WebhookGroupName = "acme.erun.io"
)

func (p TLSCertParams) acmeServer() string {
	if s := strings.TrimSpace(p.ACMEServer); s != "" {
		return s
	}
	return defaultACMEServer
}

func (p TLSCertParams) webhookGroupName() string {
	if g := strings.TrimSpace(p.DNS01WebhookGroupName); g != "" {
		return g
	}
	return defaultDNS01WebhookGroupName
}

// ExposeServiceResult is the resolved exposure plan: the public hostname, the
// per-env wildcard record, and the ingress that will front the Service.
type ExposeServiceResult struct {
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
	// Service is the logical service name — the DNS label in the public hostname.
	Service string `json:"service"`
	// BackendService is the in-namespace Service the Ingress routes to. Platform
	// Services are tenant-scoped by their component charts (<tenant>-<service>,
	// e.g. frs-api), so the backend is derived that way rather than assuming the
	// public label names the Service. This lets the public host stay the clean
	// logical label (api.frs-prod.…) while routing to the real Service (frs-api).
	BackendService    string `json:"backendService"`
	Namespace         string `json:"namespace"`
	KubernetesContext string `json:"kubernetesContext,omitempty"`
	Hostname          string `json:"hostname"`
	ServicesZone      string `json:"servicesZone"`
	WildcardName      string `json:"wildcardName"`
	TargetIP          string `json:"targetIP"`
	IngressName       string `json:"ingressName"`
	ServicePort       int    `json:"servicePort"`
	// Scheme is "https" when the Ingress references a per-env TLS secret, else
	// "http". IngressClass + TLSSecretName are the resolved Ingress wiring.
	Scheme        string `json:"scheme"`
	IngressClass  string `json:"ingressClass"`
	TLSSecretName string `json:"tlsSecretName,omitempty"`
	// TLSEnabled is the resolved answer, not merely the operator's request: it
	// is true only when something will actually populate TLSSecretName.
	// Requesting TLS (not passing --no-tls) with no DNS-01 broker configured
	// resolves to false here, same as --no-tls itself — TLSDisabledReason says
	// which. Writing a tls.secretName nothing provisions is the defect this
	// distinction exists to prevent: traefik would serve its own self-signed
	// certificate while the Ingress claimed https.
	TLSEnabled bool `json:"tlsEnabled"`
	// TLSDisabledReason names why TLSEnabled is false: "--no-tls" for an
	// explicit request, or that no DNS-01 broker is configured to provision
	// the certificate. Empty when TLSEnabled is true.
	TLSDisabledReason string `json:"tlsDisabledReason,omitempty"`
	// PlatformNamespace is the namespace the platform's PowerDNS singleton runs
	// in, where the per-env wildcard record is written.
	PlatformNamespace string `json:"platformNamespace"`
	// PlatformPowerDNSDeployment is the PowerDNS Deployment name the DNS-record
	// exec targets. The chart scopes it to the platform env's tenant
	// (<tenant>-powerdns), so it is derived from platform.Env rather than
	// hardcoded to erun-powerdns.
	PlatformPowerDNSDeployment string `json:"platformPowerdnsDeployment"`
	// PlatformContext is the kube context of the platform env's own cluster,
	// which the DNS write must target — it may differ from KubernetesContext (the
	// target env's cluster). Empty when the platform env declares no explicit
	// context (the current kube context is then used).
	PlatformContext string `json:"platformContext,omitempty"`
}

const defaultExposeServicePort = 80

// defaultExposeIngressClass is the ingress controller class the Host-routing
// Ingress binds to. k3s (which frs clusters run) ships Traefik.
const defaultExposeIngressClass = "traefik"

// defaultExposeWildcardTTL is the TTL for the per-env wildcard A record. Short
// enough that re-pointing an env (IP change) propagates quickly.
const defaultExposeWildcardTTL = 60

// RunExposeService resolves and (unless dry-run) performs the work to expose
// Service <service> in <tenant>-<env> at <service>.<tenant>-<env>.<servicesZone>:
// it ensures the per-env wildcard A record in the platform's services zone and
// applies a Host-routing Ingress into the env namespace. The per-env wildcard
// covers every service in the env, so exposing additional services only adds an
// Ingress. Every action and decision is traced before execution so a dry-run is
// a complete, side-effect-free plan.
//
// The DNS write goes through one of two paths: a direct
// `pdnsutil` exec into the platform cluster's PowerDNS pod when the caller
// has that access (the hosted deploy Job, signaled by an explicit
// ServicesZone/PlatformNamespace override), or the platform's own API when
// the caller instead has an erun platform alias configured (a developer's
// local cluster, most concretely) — see resolveExposeDNSUpserter.
// upsertDNSRecord, when non-nil, overrides the decision outright (tests).
func RunExposeService(ctx Context, params ExposeServiceParams, store ExposeStore, cloudStore CloudReadStore, deps CloudDependencies, upsertDNSRecord DNSRecordUpserterFunc, applyIngress IngressApplierFunc) (ExposeServiceResult, error) {
	store, applyIngress = normalizeExposeDependencies(store, applyIngress)

	if params.SkipIfUnconfigured && !exposePlatformConfigured(params) {
		ctx.Trace("expose: skipped, no platform block configured")
		return ExposeServiceResult{}, nil
	}

	result, upsertDNSRecord, dnsProvider, err := resolveExposePlanAndDNSWriter(ctx, params, store, cloudStore, deps, upsertDNSRecord)
	if err != nil {
		return ExposeServiceResult{}, err
	}

	dnsParams := exposeDNSParams(result)
	ingressParams := exposeIngressParams(result)
	tlsParams := exposeTLSParams(result, params.TLS)
	// TLSEnabled already means "a DNS-01 broker is configured to provision
	// this" (resolveExposeTLSPlan), so provisioning is simply whether the
	// resolved plan enabled TLS at all.
	provisionTLS := result.TLSEnabled

	// Trace the real commands, not synthetic verbs, so the dry-run plan is
	// faithful to the live run.
	traceExposePlan(ctx, result, dnsParams, ingressParams, dnsProvider)
	if provisionTLS {
		traceTLSCertPlan(ctx, tlsParams)
	}
	if ctx.DryRun {
		return result, nil
	}

	if err := applyExposeWrites(ctx, result, dnsParams, ingressParams, upsertDNSRecord, applyIngress); err != nil {
		return result, err
	}
	if provisionTLS {
		if err := applyTLSCertPlan(tlsParams); err != nil {
			return result, fmt.Errorf("provision tls certificate for %s: %w", tlsParams.WildcardHost, err)
		}
	}
	return result, nil
}

// resolveExposePlanAndDNSWriter resolves the plan and, from it, which DNS
// write path to use -- split out of RunExposeService purely to keep that
// function's own branch count down; see resolveExposeDNSUpserter for the
// actual decision.
func resolveExposePlanAndDNSWriter(ctx Context, params ExposeServiceParams, store ExposeStore, cloudStore CloudReadStore, deps CloudDependencies, upsertDNSRecord DNSRecordUpserterFunc) (ExposeServiceResult, DNSRecordUpserterFunc, CloudProviderConfig, error) {
	result, err := resolveExposeServicePlan(params, store)
	if err != nil {
		return ExposeServiceResult{}, nil, CloudProviderConfig{}, err
	}
	hasDirectOverride := strings.TrimSpace(params.ServicesZone) != "" || strings.TrimSpace(params.PlatformNamespace) != ""
	upsertDNSRecord, dnsProvider, err := resolveExposeDNSUpserter(ctx, result.Environment, params.ErunAlias, cloudStore, deps, hasDirectOverride, upsertDNSRecord)
	if err != nil {
		return ExposeServiceResult{}, nil, CloudProviderConfig{}, err
	}
	return result, upsertDNSRecord, dnsProvider, nil
}

func traceExposePlan(ctx Context, result ExposeServiceResult, dnsParams DNSRecordUpsertParams, ingressParams IngressApplyParams, dnsProvider CloudProviderConfig) {
	ctx.Trace(fmt.Sprintf("expose: %s -> service %s.%s.svc:%d", result.Hostname, result.BackendService, result.Namespace, result.ServicePort))
	ctx.Trace(fmt.Sprintf("expose: per-env wildcard %s A %s ttl %d (zone %s)", result.WildcardName, result.TargetIP, dnsParams.TTL, result.ServicesZone))
	ctx.Trace(fmt.Sprintf("expose: ingress class %s", result.IngressClass))
	if result.TLSEnabled {
		ctx.Trace(fmt.Sprintf("expose: tls secret %s (namespace %s) -> https", result.TLSSecretName, result.Namespace))
	} else {
		ctx.Trace(fmt.Sprintf("expose: http-only (%s)", result.TLSDisabledReason))
	}
	traceExposeDNSWritePlan(ctx, result, dnsParams, dnsProvider)
	ctx.TraceCommand("", "kubectl", kubectlApplyStdinArgs(ingressParams.Namespace, ingressParams.KubernetesContext)...)
	// The Ingress manifest is piped to `kubectl apply -f -` on stdin, so trace
	// its body too — the argv alone hides the exact resource the real run applies.
	ctx.TraceBlock("expose: ingress manifest", renderHostRoutingIngress(ingressParams))
}

// traceExposeDNSWritePlan traces exactly which of the two DNS write paths
// will run and what it will do, so a dry-run names the record it would write
// before it ever writes it (root AGENTS.md's "DNS changes are externally
// visible and slow to undo").
func traceExposeDNSWritePlan(ctx Context, result ExposeServiceResult, dnsParams DNSRecordUpsertParams, dnsProvider CloudProviderConfig) {
	if dnsProvider.Alias == "" {
		ctx.Trace(fmt.Sprintf("expose: platform powerdns namespace %s", result.PlatformNamespace))
		ctx.TraceCommand("", "kubectl", powerDNSUpsertArgs(dnsParams)...)
		return
	}
	tracePlatformCall(ctx, dnsProvider, "GET", "/v1/environments", "resolve environment id for "+result.Environment)
	tracePlatformCall(ctx, dnsProvider, "PUT", "/v1/environments/{environment_id}/hostname", "targetIp="+result.TargetIP)
}

// applyExposeWrites performs the DNS and Ingress side effects. The Ingress
// lands in the target env's cluster, so require that context here; the DNS
// write targets the platform cluster and is left to kubectl's resolution
// (explicit platform context or current).
func applyExposeWrites(ctx Context, result ExposeServiceResult, dnsParams DNSRecordUpsertParams, ingressParams IngressApplyParams, upsertDNSRecord DNSRecordUpserterFunc, applyIngress IngressApplierFunc) error {
	if err := ctx.RequireKubernetesContext(result.KubernetesContext); err != nil {
		return fmt.Errorf("expose %s/%s: %w", result.Tenant, result.Environment, err)
	}
	if err := upsertDNSRecord(dnsParams); err != nil {
		return fmt.Errorf("upsert wildcard DNS record %s: %w", result.WildcardName, err)
	}
	if err := applyIngress(ingressParams); err != nil {
		return fmt.Errorf("apply ingress %s: %w", result.IngressName, err)
	}
	return nil
}

func exposeDNSParams(result ExposeServiceResult) DNSRecordUpsertParams {
	return DNSRecordUpsertParams{
		Zone:               result.ServicesZone,
		Name:               result.WildcardName,
		Type:               "A",
		TTL:                defaultExposeWildcardTTL,
		Value:              result.TargetIP,
		PlatformNamespace:  result.PlatformNamespace,
		PowerDNSDeployment: result.PlatformPowerDNSDeployment,
		KubernetesContext:  result.PlatformContext,
	}
}

// TLSCertApplyParams is the resolved per-env TLS provisioning plan: the
// namespaced Issuer + Certificate + token Secret expose applies into the
// env's own namespace when TLSCertParams is configured.
type TLSCertApplyParams struct {
	KubernetesContext string
	Namespace         string
	ServicesZone      string
	// EnvLabel is the env's own DNS-01 subzone label ("<tenant>-<env>"),
	// which equals Namespace but is named separately since the two describe
	// different things (a Kubernetes namespace vs. a DNS subzone label).
	EnvLabel string
	// WildcardHost is the Certificate's dnsName, "*.<envLabel>.<zone>".
	WildcardHost string
	// SecretName is the Ingress TLS Secret this Certificate must populate.
	SecretName      string
	TokenSecretName string
	IssuerName      string
	CertificateName string
	Cert            TLSCertParams
}

func exposeTLSParams(result ExposeServiceResult, cert TLSCertParams) TLSCertApplyParams {
	envLabel := result.Namespace
	return TLSCertApplyParams{
		KubernetesContext: result.KubernetesContext,
		Namespace:         result.Namespace,
		ServicesZone:      result.ServicesZone,
		EnvLabel:          envLabel,
		WildcardHost:      result.WildcardName,
		SecretName:        result.TLSSecretName,
		TokenSecretName:   envLabel + "-dns01-token",
		IssuerName:        envLabel + "-wildcard-issuer",
		CertificateName:   envLabel + "-wildcard",
		Cert:              cert,
	}
}

func exposeIngressParams(result ExposeServiceResult) IngressApplyParams {
	p := IngressApplyParams{
		KubernetesContext: result.KubernetesContext,
		Namespace:         result.Namespace,
		Name:              result.IngressName,
		Host:              result.Hostname,
		ServiceName:       result.BackendService,
		ServicePort:       result.ServicePort,
		IngressClass:      result.IngressClass,
	}
	if result.TLSEnabled {
		p.TLSSecretName = result.TLSSecretName
	}
	return p
}

func validateExposeTarget(params ExposeServiceParams) (tenant, environment, service string, err error) {
	tenant = strings.TrimSpace(params.Tenant)
	environment = strings.TrimSpace(params.Environment)
	service = strings.TrimSpace(params.Service)
	if tenant == "" || environment == "" || service == "" {
		return "", "", "", fmt.Errorf("tenant, environment, and service are required")
	}
	if err := ValidateTenantName(tenant); err != nil {
		return "", "", "", err
	}
	// The service name becomes a DNS label in the public hostname, so validate it
	// up front — before any DNS record is written — rather than failing mid-flight.
	if !isDNSLabel(service) || len(service) > 63 {
		return "", "", "", fmt.Errorf("service name %q must be a DNS-1035 label: lowercase letters, digits, and hyphens, not starting or ending with a hyphen, at most 63 characters", service)
	}
	return tenant, environment, service, nil
}

// exposePlatformConfigured reports whether expose has enough platform
// information to resolve a hostname, either from the caller's explicit
// ServicesZone/PlatformNamespace override or from ProjectRoot's platform
// block. SkipIfUnconfigured only asks "is this a platform deployment at all",
// so an explicit override — which by definition names a real platform deploy —
// always counts, with no project needed to confirm it.
func exposePlatformConfigured(params ExposeServiceParams) bool {
	if strings.TrimSpace(params.ServicesZone) != "" && strings.TrimSpace(params.PlatformNamespace) != "" {
		return true
	}
	return ProjectHasExposablePlatform(params.ProjectRoot)
}

// ProjectHasExposablePlatform reports whether projectRoot declares enough of a
// platform block for expose to resolve a hostname: the same three conditions
// resolveExposeServicePlan otherwise fails on (missing block, missing base
// domain, missing platform.env). Mirrors those checks rather than calling
// resolveExposeServicePlan, since that also validates the store's env config —
// SkipIfUnconfigured only asks "is this a platform deployment at all". Exported
// so a caller can decide up front whether an environment is even eligible for
// exposure, before offering the action at all (e.g. the desktop's Ports tab).
func ProjectHasExposablePlatform(projectRoot string) bool {
	platform := resolveProjectPlatform(projectRoot)
	return !platform.IsZero() && strings.TrimSpace(platform.ServicesZone) != "" && strings.TrimSpace(platform.Env) != ""
}

// resolveExposeServicePlan does no tracing or mutation, so RunExposeService keeps
// ownership of the dry-run trace order.
func resolveExposeServicePlan(params ExposeServiceParams, store ExposeStore) (ExposeServiceResult, error) {
	tenant, environment, service, err := validateExposeTarget(params)
	if err != nil {
		return ExposeServiceResult{}, err
	}
	servicesZone, platformNamespace, platformPowerDNS, platformContext, err := resolveExposePlatformCoordinates(params, store)
	if err != nil {
		return ExposeServiceResult{}, err
	}
	envConfig, _, err := store.LoadEnvConfig(tenant, environment)
	if err != nil {
		return ExposeServiceResult{}, err
	}
	targetIP := strings.TrimSpace(params.TargetIP)
	if targetIP == "" {
		return ExposeServiceResult{}, fmt.Errorf("a target IP is required (the env's ingress IP the wildcard record points at, e.g. 127.0.0.1 for a local cluster)")
	}
	servicePort := params.ServicePort
	if servicePort <= 0 {
		servicePort = defaultExposeServicePort
	}
	envLabel := KubernetesNamespaceName(tenant, environment)
	tlsEnabled, ingressClass, tlsSecret, scheme, tlsDisabledReason := resolveExposeTLSPlan(params, envLabel)
	result := ExposeServiceResult{
		Tenant:                     tenant,
		Environment:                environment,
		Service:                    service,
		BackendService:             resolveExposeBackendService(params.BackendService, tenant, service),
		Namespace:                  envLabel,
		KubernetesContext:          strings.TrimSpace(envConfig.KubernetesContext),
		Hostname:                   fmt.Sprintf("%s.%s.%s", service, envLabel, servicesZone),
		ServicesZone:               servicesZone,
		WildcardName:               fmt.Sprintf("*.%s.%s", envLabel, servicesZone),
		TargetIP:                   targetIP,
		IngressName:                fmt.Sprintf("expose-%s", service),
		ServicePort:                servicePort,
		Scheme:                     scheme,
		IngressClass:               ingressClass,
		TLSSecretName:              tlsSecret,
		TLSEnabled:                 tlsEnabled,
		TLSDisabledReason:          tlsDisabledReason,
		PlatformNamespace:          platformNamespace,
		PlatformPowerDNSDeployment: platformPowerDNS,
		PlatformContext:            platformContext,
	}
	return result, nil
}

// resolveExposePlatformCoordinates resolves the platform coordinates expose
// needs — the services zone tenant hostnames live under, the namespace running
// the platform's PowerDNS singleton, the Deployment name the DNS write execs
// into, and (best-effort) the platform cluster's own kube context — from either
// an explicit override or ProjectRoot's platform block. See
// ExposeServiceParams.ServicesZone/PlatformNamespace: a caller with no project
// to resolve (the deploy Job) supplies these directly instead.
func resolveExposePlatformCoordinates(params ExposeServiceParams, store ExposeStore) (zone, namespace, powerDNSDeployment, platformContext string, err error) {
	zone = strings.TrimSpace(params.ServicesZone)
	namespace = strings.TrimSpace(params.PlatformNamespace)
	if zone != "" || namespace != "" {
		if zone == "" || namespace == "" {
			return "", "", "", "", fmt.Errorf("a services zone and a platform namespace override are both required when either is set")
		}
		platTenant, _, ok := splitTenantEnv(namespace)
		if !ok {
			return "", "", "", "", fmt.Errorf("platform namespace override %q must be a \"<tenant>-<env>\" namespace label", namespace)
		}
		// No platform.env to resolve a distinct platform-cluster context from, so
		// the DNS write falls back to kubectl's current context — correct for the
		// single-cluster hosted deployment a deploy Job (the caller this override
		// exists for) always runs in.
		return zone, normalizeNamespaceName(namespace), TenantResourcePrefix(platTenant) + "-powerdns", "", nil
	}

	platform := resolveProjectPlatform(params.ProjectRoot)
	if platform.IsZero() || strings.TrimSpace(platform.ServicesZone) == "" {
		return "", "", "", "", fmt.Errorf("a platform block with a base domain is required in .erun/config.yaml (the services zone tenant hostnames live under)")
	}
	// Fail fast on a malformed platform block (matching deploy's contract) before
	// deriving any hostnames or zone names from it.
	if err := platform.Validate(); err != nil {
		return "", "", "", "", err
	}
	// platform.env is optional for the deploy/PowerDNS chart (it deploys into the
	// release namespace), but expose derives the PowerDNS pod's namespace from it
	// to exec the DNS write. Without it the write would run `kubectl -n "" exec`
	// and silently target the current/default namespace, so require it here.
	if strings.TrimSpace(platform.Env) == "" {
		return "", "", "", "", fmt.Errorf("platform.env is required in .erun/config.yaml (the platform environment that runs the PowerDNS singleton)")
	}
	// The PowerDNS Deployment is scoped to the platform env's tenant by its chart
	// (<tenant>-powerdns); platform.Env is that env's "<tenant>-<env>" namespace
	// label, so its tenant prefix gives the Deployment name the exec targets.
	platTenant, _, _ := splitTenantEnv(platform.Env)
	return platform.ServicesZone, normalizeNamespaceName(platform.Env), TenantResourcePrefix(platTenant) + "-powerdns", resolvePlatformContext(store, platform.Env), nil
}

// resolveExposeBackendService picks the Service the Ingress routes to: the one
// the caller named, or the <tenant>-<service> convention when it named none.
func resolveExposeBackendService(backendService, tenant, service string) string {
	if backendService = strings.TrimSpace(backendService); backendService != "" {
		return backendService
	}
	return fmt.Sprintf("%s-%s", TenantResourcePrefix(tenant), service)
}

// resolveExposeTLSPlan resolves the Ingress TLS wiring: https by default,
// referencing the env's own per-env wildcard cert Secret
// ("<tenant>-<env>-wildcard-tls", the name the DNS-01 broker path in
// TLSCertParams provisions it under). TLS resolves to disabled — the Ingress
// omits the tls: block and traefik serves plain http — for either of two
// reasons: the operator asked for that explicitly (--no-tls), or nothing can
// actually populate the secret because no DNS-01 broker is configured.
// Writing tls.secretName regardless of whether TLSCertParams is configured is
// exactly the defect this second case exists to prevent: the Ingress would
// claim https while traefik falls back to its own self-signed certificate.
// disabledReason is empty when TLS is enabled, else names which case applied,
// for the trace and the resolved result alike. No env-type branching — the
// primitive always resolves the same way.
func resolveExposeTLSPlan(params ExposeServiceParams, envLabel string) (tlsEnabled bool, ingressClass, tlsSecret, scheme, disabledReason string) {
	ingressClass = strings.TrimSpace(params.IngressClass)
	if ingressClass == "" {
		ingressClass = defaultExposeIngressClass
	}

	switch {
	case params.NoTLS:
		disabledReason = "--no-tls"
	case !params.TLS.configured():
		disabledReason = "no DNS-01 broker configured to provision the certificate"
	}
	tlsEnabled = disabledReason == ""

	scheme = "http"
	if tlsEnabled {
		scheme = "https"
		tlsSecret = strings.TrimSpace(params.TLSSecretName)
		if tlsSecret == "" {
			tlsSecret = fmt.Sprintf("%s-wildcard-tls", envLabel)
		}
	}
	return tlsEnabled, ingressClass, tlsSecret, scheme, disabledReason
}

// resolvePlatformContext finds the kube context of the platform env's own
// cluster so the DNS write targets the platform's PowerDNS rather than the
// target env's cluster (which may be a different cluster entirely). Best-effort:
// if the platform env config is not loadable, returns "" and kubectl falls back
// to the current context.
func resolvePlatformContext(store ExposeStore, platformEnv string) string {
	platTenant, platEnvName, ok := splitTenantEnv(platformEnv)
	if !ok {
		return ""
	}
	envConfig, _, err := store.LoadEnvConfig(platTenant, platEnvName)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(envConfig.KubernetesContext)
}

// splitTenantEnv splits a "<tenant>-<env>" namespace label on its first hyphen.
// Tenant names contain no hyphens (ValidateTenantName), so the first hyphen is
// the unambiguous tenant/env boundary; env may itself contain hyphens.
func splitTenantEnv(label string) (tenant, env string, ok bool) {
	label = strings.TrimSpace(label)
	i := strings.IndexByte(label, '-')
	if i <= 0 || i >= len(label)-1 {
		return "", "", false
	}
	return label[:i], label[i+1:], true
}

func normalizeExposeDependencies(store ExposeStore, applyIngress IngressApplierFunc) (ExposeStore, IngressApplierFunc) {
	if store == nil {
		store = ConfigStore{}
	}
	if applyIngress == nil {
		applyIngress = applyHostRoutingIngress
	}
	return store, applyIngress
}
