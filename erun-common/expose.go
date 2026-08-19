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
	TLSEnabled    bool   `json:"tlsEnabled"`
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
func RunExposeService(ctx Context, params ExposeServiceParams, store ExposeStore, upsertDNSRecord DNSRecordUpserterFunc, applyIngress IngressApplierFunc) (ExposeServiceResult, error) {
	store, upsertDNSRecord, applyIngress = normalizeExposeDependencies(store, upsertDNSRecord, applyIngress)

	if params.SkipIfUnconfigured && !projectHasExposablePlatform(params.ProjectRoot) {
		ctx.Trace("expose: skipped, no platform block configured")
		return ExposeServiceResult{}, nil
	}

	result, err := resolveExposeServicePlan(params, store)
	if err != nil {
		return ExposeServiceResult{}, err
	}

	dnsParams := exposeDNSParams(result)
	ingressParams := exposeIngressParams(result)

	// Trace the real commands, not synthetic verbs, so the dry-run plan is
	// faithful to the live run.
	ctx.Trace(fmt.Sprintf("expose: %s -> service %s.%s.svc:%d", result.Hostname, result.BackendService, result.Namespace, result.ServicePort))
	ctx.Trace(fmt.Sprintf("expose: per-env wildcard %s A %s ttl %d (zone %s)", result.WildcardName, result.TargetIP, dnsParams.TTL, result.ServicesZone))
	ctx.Trace(fmt.Sprintf("expose: ingress class %s", result.IngressClass))
	if result.TLSEnabled {
		ctx.Trace(fmt.Sprintf("expose: tls secret %s (namespace %s) -> https", result.TLSSecretName, result.Namespace))
	} else {
		ctx.Trace("expose: http-only (--no-tls)")
	}
	ctx.Trace(fmt.Sprintf("expose: platform powerdns namespace %s", result.PlatformNamespace))
	ctx.TraceCommand("", "kubectl", powerDNSUpsertArgs(dnsParams)...)
	ctx.TraceCommand("", "kubectl", ingressApplyArgs(ingressParams)...)
	// The Ingress manifest is piped to `kubectl apply -f -` on stdin, so trace
	// its body too — the argv alone hides the exact resource the real run applies.
	ctx.TraceBlock("expose: ingress manifest", renderHostRoutingIngress(ingressParams))
	if ctx.DryRun {
		return result, nil
	}

	// The Ingress lands in the target env's cluster, so require that context here.
	// The DNS write targets the platform cluster and is left to kubectl's
	// resolution (explicit platform context or current).
	if err := ctx.RequireKubernetesContext(result.KubernetesContext); err != nil {
		return result, fmt.Errorf("expose %s/%s: %w", result.Tenant, result.Environment, err)
	}
	if err := upsertDNSRecord(dnsParams); err != nil {
		return result, fmt.Errorf("upsert wildcard DNS record %s: %w", result.WildcardName, err)
	}
	if err := applyIngress(ingressParams); err != nil {
		return result, fmt.Errorf("apply ingress %s: %w", result.IngressName, err)
	}
	return result, nil
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

// projectHasExposablePlatform reports whether projectRoot declares enough of a
// platform block for expose to resolve a hostname: the same three conditions
// resolveExposeServicePlan otherwise fails on (missing block, missing base
// domain, missing platform.env). Mirrors those checks rather than calling
// resolveExposeServicePlan, since that also validates the store's env config —
// SkipIfUnconfigured only asks "is this a platform deployment at all".
func projectHasExposablePlatform(projectRoot string) bool {
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
	platform := resolveProjectPlatform(params.ProjectRoot)
	if platform.IsZero() || strings.TrimSpace(platform.ServicesZone) == "" {
		return ExposeServiceResult{}, fmt.Errorf("expose requires a platform block with a base domain in .erun/config.yaml (the services zone tenant hostnames live under)")
	}
	// Fail fast on a malformed platform block (matching deploy's contract) before
	// deriving any hostnames or zone names from it.
	if err := platform.Validate(); err != nil {
		return ExposeServiceResult{}, err
	}
	// platform.env is optional for the deploy/PowerDNS chart (it deploys into the
	// release namespace), but expose derives the PowerDNS pod's namespace from it
	// to exec the DNS write. Without it the write would run `kubectl -n "" exec`
	// and silently target the current/default namespace, so require it here.
	if strings.TrimSpace(platform.Env) == "" {
		return ExposeServiceResult{}, fmt.Errorf("expose requires platform.env in .erun/config.yaml (the platform environment that runs the PowerDNS singleton)")
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
	// The PowerDNS Deployment is scoped to the platform env's tenant by its chart
	// (<tenant>-powerdns); platform.Env is that env's "<tenant>-<env>" namespace
	// label, so its tenant prefix gives the Deployment name the exec targets.
	platTenant, _, _ := splitTenantEnv(platform.Env)
	platformPowerDNS := TenantResourcePrefix(platTenant) + "-powerdns"
	tlsEnabled, ingressClass, tlsSecret, scheme := resolveExposeTLSPlan(params, envLabel)
	result := ExposeServiceResult{
		Tenant:                     tenant,
		Environment:                environment,
		Service:                    service,
		BackendService:             fmt.Sprintf("%s-%s", TenantResourcePrefix(tenant), service),
		Namespace:                  envLabel,
		KubernetesContext:          strings.TrimSpace(envConfig.KubernetesContext),
		Hostname:                   fmt.Sprintf("%s.%s.%s", service, envLabel, platform.ServicesZone),
		ServicesZone:               platform.ServicesZone,
		WildcardName:               fmt.Sprintf("*.%s.%s", envLabel, platform.ServicesZone),
		TargetIP:                   targetIP,
		IngressName:                fmt.Sprintf("expose-%s", service),
		ServicePort:                servicePort,
		Scheme:                     scheme,
		IngressClass:               ingressClass,
		TLSSecretName:              tlsSecret,
		TLSEnabled:                 tlsEnabled,
		PlatformNamespace:          normalizeNamespaceName(platform.Env),
		PlatformPowerDNSDeployment: platformPowerDNS,
		PlatformContext:            resolvePlatformContext(store, platform.Env),
	}
	return result, nil
}

// resolveExposeTLSPlan resolves the Ingress TLS wiring: https by default,
// referencing the env's per-env wildcard cert Secret
// ("<tenant>-<env>-wildcard-tls" the cluster-edge module issues). No env-type
// branching — the primitive always resolves the same way.
func resolveExposeTLSPlan(params ExposeServiceParams, envLabel string) (tlsEnabled bool, ingressClass, tlsSecret, scheme string) {
	tlsEnabled = !params.NoTLS
	ingressClass = strings.TrimSpace(params.IngressClass)
	if ingressClass == "" {
		ingressClass = defaultExposeIngressClass
	}
	tlsSecret = strings.TrimSpace(params.TLSSecretName)
	if tlsSecret == "" {
		tlsSecret = fmt.Sprintf("%s-wildcard-tls", envLabel)
	}
	scheme = "http"
	if tlsEnabled {
		scheme = "https"
	}
	return tlsEnabled, ingressClass, tlsSecret, scheme
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

func normalizeExposeDependencies(store ExposeStore, upsertDNSRecord DNSRecordUpserterFunc, applyIngress IngressApplierFunc) (ExposeStore, DNSRecordUpserterFunc, IngressApplierFunc) {
	if store == nil {
		store = ConfigStore{}
	}
	if upsertDNSRecord == nil {
		upsertDNSRecord = upsertPowerDNSRecord
	}
	if applyIngress == nil {
		applyIngress = applyHostRoutingIngress
	}
	return store, upsertDNSRecord, applyIngress
}
