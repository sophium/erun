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
// platform's authoritative zone via the PowerDNS API. Injected so dry-run never
// touches the network and tests stay deterministic; the default implementation
// is live-only.
type DNSRecordUpserterFunc func(params DNSRecordUpsertParams) error

// IngressApplierFunc applies the Host-routing Ingress that fronts the exposed
// Service. Injected for the same reason as DNSRecordUpserterFunc.
type IngressApplierFunc func(params IngressApplyParams) error

// DNSRecordUpsertParams is the per-record write the exposure flow performs
// against PowerDNS (the per-env wildcard A record). PlatformNamespace and
// KubernetesContext locate the platform's PowerDNS pod (the singleton that owns
// the services zone), which the default implementation reaches by exec.
type DNSRecordUpsertParams struct {
	Zone              string
	Name              string
	Type              string
	TTL               int
	Value             string
	PlatformNamespace string
	KubernetesContext string
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
}

// ExposeServiceParams are the inputs to exposing one in-namespace Service at a
// stable public hostname. ProjectRoot locates the platform config; TargetIP is
// the env's ingress IP the per-env wildcard A record points at (127.0.0.1 for a
// VM-based local cluster, a node/LAN IP, or the public LB IP for remote).
type ExposeServiceParams struct {
	Tenant      string
	Environment string
	Service     string
	ProjectRoot string
	TargetIP    string
	ServicePort int
}

// ExposeServiceResult is the resolved exposure plan: the public hostname, the
// per-env wildcard record, and the ingress that will front the Service.
type ExposeServiceResult struct {
	Tenant            string `json:"tenant"`
	Environment       string `json:"environment"`
	Service           string `json:"service"`
	Namespace         string `json:"namespace"`
	KubernetesContext string `json:"kubernetesContext,omitempty"`
	Hostname          string `json:"hostname"`
	ServicesZone      string `json:"servicesZone"`
	WildcardName      string `json:"wildcardName"`
	TargetIP          string `json:"targetIP"`
	IngressName       string `json:"ingressName"`
	ServicePort       int    `json:"servicePort"`
	// PlatformNamespace is the namespace the platform's PowerDNS singleton runs
	// in (derived from platform.Env); the per-env wildcard record is written by
	// exec'ing pdnsutil there.
	PlatformNamespace string `json:"platformNamespace"`
	// PlatformContext is the kube context of the platform env's own cluster,
	// which the DNS write must target — it may differ from KubernetesContext (the
	// target env's cluster). Empty when the platform env declares no explicit
	// context (the current kube context is then used).
	PlatformContext string `json:"platformContext,omitempty"`
}

const defaultExposeServicePort = 80

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

	result, err := resolveExposeServicePlan(params, store)
	if err != nil {
		return ExposeServiceResult{}, err
	}

	dnsParams := exposeDNSParams(result)
	ingressParams := exposeIngressParams(result)

	// Trace the real commands (not synthetic verbs) so a dry-run is a faithful,
	// side-effect-free plan: the per-env wildcard write execs pdnsutil in the
	// platform's PowerDNS pod (its own namespace/context), and the Ingress is
	// applied into the target env's namespace.
	ctx.Trace(fmt.Sprintf("expose: %s -> service %s.%s.svc:%d", result.Hostname, result.Service, result.Namespace, result.ServicePort))
	ctx.Trace(fmt.Sprintf("expose: per-env wildcard %s A %s ttl %d (zone %s)", result.WildcardName, result.TargetIP, dnsParams.TTL, result.ServicesZone))
	ctx.Trace(fmt.Sprintf("expose: platform powerdns namespace %s", result.PlatformNamespace))
	ctx.TraceCommand("", "kubectl", powerDNSUpsertArgs(dnsParams)...)
	ctx.TraceCommand("", "kubectl", ingressApplyArgs(ingressParams)...)
	if ctx.DryRun {
		return result, nil
	}

	// The Ingress lands in the target env's cluster; require that context here.
	// The DNS write targets the platform cluster (dnsParams.KubernetesContext)
	// and is left to kubectl's resolution (explicit platform context or current).
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

// exposeDNSParams builds the per-env wildcard A-record write from the resolved
// plan. It targets the platform's PowerDNS (its namespace and own kube context),
// not the target env's cluster.
func exposeDNSParams(result ExposeServiceResult) DNSRecordUpsertParams {
	return DNSRecordUpsertParams{
		Zone:              result.ServicesZone,
		Name:              result.WildcardName,
		Type:              "A",
		TTL:               defaultExposeWildcardTTL,
		Value:             result.TargetIP,
		PlatformNamespace: result.PlatformNamespace,
		KubernetesContext: result.PlatformContext,
	}
}

// exposeIngressParams builds the Host-routing Ingress apply from the resolved
// plan, targeting the env's own namespace and cluster context.
func exposeIngressParams(result ExposeServiceResult) IngressApplyParams {
	return IngressApplyParams{
		KubernetesContext: result.KubernetesContext,
		Namespace:         result.Namespace,
		Name:              result.IngressName,
		Host:              result.Hostname,
		ServiceName:       result.Service,
		ServicePort:       result.ServicePort,
	}
}

// validateExposeTarget trims and validates the required identity inputs,
// returning the cleaned tenant/environment/service.
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
	// The service name becomes a DNS label in the public hostname, an Ingress
	// name (expose-<service>), and the routed Service name, so validate it up
	// front — before any DNS record is written — rather than failing mid-flight.
	if !isDNSLabel(service) || len(service) > 63 {
		return "", "", "", fmt.Errorf("service name %q must be a DNS-1035 label: lowercase letters, digits, and hyphens, not starting or ending with a hyphen, at most 63 characters", service)
	}
	return tenant, environment, service, nil
}

// resolveExposeServicePlan validates inputs, resolves the platform and env, and
// assembles the side-effect-free ExposeServiceResult plus the resolved platform
// the execution phase needs. It does no tracing or mutation, so RunExposeService
// keeps ownership of the dry-run trace order.
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
	result := ExposeServiceResult{
		Tenant:            tenant,
		Environment:       environment,
		Service:           service,
		Namespace:         envLabel,
		KubernetesContext: strings.TrimSpace(envConfig.KubernetesContext),
		Hostname:          fmt.Sprintf("%s.%s.%s", service, envLabel, platform.ServicesZone),
		ServicesZone:      platform.ServicesZone,
		WildcardName:      fmt.Sprintf("*.%s.%s", envLabel, platform.ServicesZone),
		TargetIP:          targetIP,
		IngressName:       fmt.Sprintf("expose-%s", service),
		ServicePort:       servicePort,
		PlatformNamespace: normalizeNamespaceName(platform.Env),
		PlatformContext:   resolvePlatformContext(store, platform.Env),
	}
	return result, nil
}

// resolvePlatformContext finds the kube context of the platform env's own
// cluster so the DNS write targets the platform's PowerDNS rather than the
// target env's cluster (which may be a different cluster entirely). platformEnv
// is a "<tenant>-<env>" namespace label; tenant names carry no hyphen, so the
// first hyphen splits it unambiguously. Best-effort: if the platform env config
// is not loadable, returns "" and kubectl falls back to the current context.
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
