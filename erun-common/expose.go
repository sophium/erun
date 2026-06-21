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

	tenant := strings.TrimSpace(params.Tenant)
	environment := strings.TrimSpace(params.Environment)
	service := strings.TrimSpace(params.Service)
	if tenant == "" || environment == "" || service == "" {
		return ExposeServiceResult{}, fmt.Errorf("tenant, environment, and service are required")
	}
	if err := ValidateTenantName(tenant); err != nil {
		return ExposeServiceResult{}, err
	}

	platform := resolveProjectPlatform(params.ProjectRoot)
	if platform.IsZero() || strings.TrimSpace(platform.ServicesZone) == "" {
		return ExposeServiceResult{}, fmt.Errorf("expose requires a platform block with a base domain in .erun/config.yaml (the services zone tenant hostnames live under)")
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
	}

	ctx.Trace(fmt.Sprintf("expose: %s -> service %s.%s.svc:%d", result.Hostname, service, result.Namespace, servicePort))
	ctx.Trace(fmt.Sprintf("expose: per-env wildcard %s A %s (zone %s)", result.WildcardName, targetIP, platform.ServicesZone))
	ctx.TraceCommand("", "powerdns-upsert-record", platform.ServicesZone, result.WildcardName, "A", targetIP)
	ctx.TraceCommand("", "kubectl", "apply", "ingress/"+result.IngressName, "-n", result.Namespace)
	if ctx.DryRun {
		return result, nil
	}

	if err := ctx.RequireKubernetesContext(result.KubernetesContext); err != nil {
		return result, fmt.Errorf("expose %s/%s: %w", tenant, environment, err)
	}
	if err := upsertDNSRecord(DNSRecordUpsertParams{
		Zone:              platform.ServicesZone,
		Name:              result.WildcardName,
		Type:              "A",
		TTL:               defaultExposeWildcardTTL,
		Value:             targetIP,
		PlatformNamespace: normalizeNamespaceName(platform.Env),
		KubernetesContext: result.KubernetesContext,
	}); err != nil {
		return result, fmt.Errorf("upsert wildcard DNS record %s: %w", result.WildcardName, err)
	}
	if err := applyIngress(IngressApplyParams{
		KubernetesContext: result.KubernetesContext,
		Namespace:         result.Namespace,
		Name:              result.IngressName,
		Host:              result.Hostname,
		ServiceName:       service,
		ServicePort:       servicePort,
	}); err != nil {
		return result, fmt.Errorf("apply ingress %s: %w", result.IngressName, err)
	}
	return result, nil
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
