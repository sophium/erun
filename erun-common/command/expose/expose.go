package expose

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/kube"
	"github.com/sophium/erun/erun-common/primitive/deploy/helmspec"
	"github.com/sophium/erun/erun-common/primitive/shared/opentarget"
)

// DNSRecordUpserterFunc creates or replaces one record in the platform's PowerDNS zone.
type DNSRecordUpserterFunc func(params DNSRecordUpsertParams) error

// DNSRecordDeleterFunc removes one record from the platform's PowerDNS zone.
type DNSRecordDeleterFunc func(params DNSRecordDeleteParams) error

// IngressApplierFunc applies the Host-routing Ingress that fronts the exposed Service.
type IngressApplierFunc func(params IngressApplyParams) error

// DNSRecordUpsertParams is the per-env wildcard A-record write.
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

// DNSRecordDeleteParams is the per-env wildcard A-record removal.
type DNSRecordDeleteParams struct {
	Zone               string
	Name               string
	Type               string
	PlatformNamespace  string
	PowerDNSDeployment string
	KubernetesContext  string
}

// IngressApplyParams is the Host-routing Ingress applied into the env namespace.
type IngressApplyParams struct {
	KubernetesContext string
	Namespace         string
	Name              string
	Host              string
	ServiceName       string
	ServicePort       int
	IngressClass      string
	// TLSSecretName, when empty, leaves the Ingress http-only.
	TLSSecretName string
}

// ExposeServiceParams are the inputs to exposing one Service at a public
// hostname; TargetIP is where the per-env wildcard A record points.
type ExposeServiceParams struct {
	Tenant      string
	Environment string
	Service     string
	ProjectRoot string
	TargetIP    string
	ServicePort int
	// BackendService overrides the <tenant>-<service> derivation for a chart
	// that renders its own Service name; a wrong name yields an ingress that 503s.
	BackendService string
	NoTLS          bool
	IngressClass   string
	// TLSSecretName defaults to "<tenant>-<env>-wildcard-tls".
	TLSSecretName string
	// SkipIfUnconfigured makes a missing platform block a no-op, for callers
	// like deploy that compose expose without knowing the project's shape.
	SkipIfUnconfigured bool
	// ServicesZone and PlatformNamespace, when both set, replace the project's
	// platform block, for callers with no checkout such as the hosted deploy Job.
	ServicesZone      string
	PlatformNamespace string
	// TLS provisions the per-env wildcard certificate; a zero value skips it.
	TLS TLSCertParams
	// ErunAlias picks the erun cloud alias for the platform DNS route; empty
	// resolves the sole one.
	ErunAlias string
}

// TLSCertParams provisions a namespaced Issuer + Certificate authorized by a
// per-env DNS-01 broker token scoped to that env's subzone, so one env cannot
// prove control of another's hostnames. Any empty field skips provisioning.
type TLSCertParams struct {
	// DNS01TokenPath is a file, not a flag value, so the token never appears in argv.
	DNS01TokenPath string
	DNS01BrokerURL string
	// DNS01WebhookGroupName defaults to "acme.erun.io".
	DNS01WebhookGroupName string
	// ACMEEmail empty marks TLS as unconfigured.
	ACMEEmail string
	// ACMEServer defaults to Let's Encrypt production.
	ACMEServer string
}

func (p TLSCertParams) configured() bool {
	return strings.TrimSpace(p.DNS01TokenPath) != "" && strings.TrimSpace(p.DNS01BrokerURL) != "" && strings.TrimSpace(p.ACMEEmail) != ""
}

const (
	// These mirror terraform-erun-cluster-edge's defaults.
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

// ExposeServiceResult is the resolved exposure plan.
type ExposeServiceResult struct {
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
	Service     string `json:"service"`
	// BackendService is <tenant>-<service> (e.g. frs-api) while the public
	// label stays the logical service name.
	BackendService    string `json:"backendService"`
	Namespace         string `json:"namespace"`
	KubernetesContext string `json:"kubernetesContext,omitempty"`
	Hostname          string `json:"hostname"`
	ServicesZone      string `json:"servicesZone"`
	WildcardName      string `json:"wildcardName"`
	TargetIP          string `json:"targetIP"`
	IngressName       string `json:"ingressName"`
	ServicePort       int    `json:"servicePort"`
	Scheme            string `json:"scheme"`
	IngressClass      string `json:"ingressClass"`
	TLSSecretName     string `json:"tlsSecretName,omitempty"`
	// TLSEnabled is true only when something will populate TLSSecretName;
	// otherwise traefik would serve its self-signed cert while claiming https.
	TLSEnabled bool `json:"tlsEnabled"`
	// TLSDisabledReason is "--no-tls" or the missing DNS-01 broker.
	TLSDisabledReason string `json:"tlsDisabledReason,omitempty"`
	PlatformNamespace string `json:"platformNamespace"`
	// PlatformPowerDNSDeployment is <tenant>-powerdns, derived from platform.Env.
	PlatformPowerDNSDeployment string `json:"platformPowerdnsDeployment"`
	// PlatformContext may differ from KubernetesContext; empty uses the current context.
	PlatformContext string `json:"platformContext,omitempty"`
}

const defaultExposeServicePort = 80

const defaultExposeIngressClass = "traefik"

const defaultExposeWildcardTTL = 60

// RunExposeService ensures the per-env wildcard A record and applies a
// Host-routing Ingress for <service>.<tenant>-<env>.<servicesZone>. Dry-run
// traces the complete plan without side effects.
func RunExposeService(ctx core.Context, params ExposeServiceParams, store seam.ExposeStore, cloudStore config.CloudReadStore, deps cloud.CloudDependencies, upsertDNSRecord DNSRecordUpserterFunc, applyIngress IngressApplierFunc) (ExposeServiceResult, error) {
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
	provisionTLS := result.TLSEnabled

	// Trace the real commands so the dry-run plan matches the live run.
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

func resolveExposePlanAndDNSWriter(ctx core.Context, params ExposeServiceParams, store seam.ExposeStore, cloudStore config.CloudReadStore, deps cloud.CloudDependencies, upsertDNSRecord DNSRecordUpserterFunc) (ExposeServiceResult, DNSRecordUpserterFunc, model.CloudProviderConfig, error) {
	result, err := resolveExposeServicePlan(params, store)
	if err != nil {
		return ExposeServiceResult{}, nil, model.CloudProviderConfig{}, err
	}
	hasDirectOverride := strings.TrimSpace(params.ServicesZone) != "" || strings.TrimSpace(params.PlatformNamespace) != ""
	upsertDNSRecord, dnsProvider, err := resolveExposeDNSUpserter(ctx, result.Environment, params.ErunAlias, cloudStore, deps, hasDirectOverride, upsertDNSRecord)
	if err != nil {
		return ExposeServiceResult{}, nil, model.CloudProviderConfig{}, err
	}
	return result, upsertDNSRecord, dnsProvider, nil
}

func traceExposePlan(ctx core.Context, result ExposeServiceResult, dnsParams DNSRecordUpsertParams, ingressParams IngressApplyParams, dnsProvider model.CloudProviderConfig) {
	ctx.Trace(fmt.Sprintf("expose: %s -> service %s.%s.svc:%d", result.Hostname, result.BackendService, result.Namespace, result.ServicePort))
	ctx.Trace(fmt.Sprintf("expose: per-env wildcard %s A %s ttl %d (zone %s)", result.WildcardName, result.TargetIP, dnsParams.TTL, result.ServicesZone))
	ctx.Trace(fmt.Sprintf("expose: ingress class %s", result.IngressClass))
	if result.TLSEnabled {
		ctx.Trace(fmt.Sprintf("expose: tls secret %s (namespace %s) -> https", result.TLSSecretName, result.Namespace))
	} else {
		ctx.Trace(fmt.Sprintf("expose: http-only (%s)", result.TLSDisabledReason))
	}
	traceExposeDNSWritePlan(ctx, result, dnsParams, dnsProvider)
	ctx.TraceCommand("", "kubectl", kube.KubectlApplyStdinArgs(ingressParams.Namespace, ingressParams.KubernetesContext)...)
	// The manifest goes over stdin, so trace its body too.
	ctx.TraceBlock("expose: ingress manifest", renderHostRoutingIngress(ingressParams))
}

// traceExposeDNSWritePlan names the record before writing it: DNS changes are
// externally visible and slow to undo.
func traceExposeDNSWritePlan(ctx core.Context, result ExposeServiceResult, dnsParams DNSRecordUpsertParams, dnsProvider model.CloudProviderConfig) {
	if dnsProvider.Alias == "" {
		ctx.Trace(fmt.Sprintf("expose: platform powerdns namespace %s", result.PlatformNamespace))
		ctx.TraceCommand("", "kubectl", powerDNSUpsertArgs(dnsParams)...)
		return
	}
	cloud.TracePlatformCall(ctx, dnsProvider, "GET", "/v1/environments", "resolve environment id for "+result.Environment)
	cloud.TracePlatformCall(ctx, dnsProvider, "PUT", "/v1/environments/{environment_id}/hostname", "targetIp="+result.TargetIP)
}

// applyExposeWrites requires the target env's context for the Ingress; the
// DNS write targets the platform cluster.
func applyExposeWrites(ctx core.Context, result ExposeServiceResult, dnsParams DNSRecordUpsertParams, ingressParams IngressApplyParams, upsertDNSRecord DNSRecordUpserterFunc, applyIngress IngressApplierFunc) error {
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

// TLSCertApplyParams is the resolved per-env TLS provisioning plan.
type TLSCertApplyParams struct {
	KubernetesContext string
	Namespace         string
	ServicesZone      string
	// EnvLabel is the DNS-01 subzone label; it equals Namespace but means something else.
	EnvLabel        string
	WildcardHost    string
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
	if err := config.ValidateTenantName(tenant); err != nil {
		return "", "", "", err
	}
	// Validate before any DNS record is written: the name becomes a DNS label.
	if !config.IsDNSLabel(service) || len(service) > 63 {
		return "", "", "", fmt.Errorf("service name %q must be a DNS-1035 label: lowercase letters, digits, and hyphens, not starting or ending with a hyphen, at most 63 characters", service)
	}
	return tenant, environment, service, nil
}

// An explicit ServicesZone/PlatformNamespace override always counts as configured.
func exposePlatformConfigured(params ExposeServiceParams) bool {
	if strings.TrimSpace(params.ServicesZone) != "" && strings.TrimSpace(params.PlatformNamespace) != "" {
		return true
	}
	return ProjectHasExposablePlatform(params.ProjectRoot)
}

// ProjectHasExposablePlatform reports whether projectRoot declares enough of a
// platform block for expose to resolve a hostname.
func ProjectHasExposablePlatform(projectRoot string) bool {
	platform := helmspec.ResolveProjectPlatform(projectRoot)
	return !platform.IsZero() && strings.TrimSpace(platform.ServicesZone) != "" && strings.TrimSpace(platform.Env) != ""
}

// resolveExposeServicePlan does no tracing, so RunExposeService owns the trace order.
func resolveExposeServicePlan(params ExposeServiceParams, store seam.ExposeStore) (ExposeServiceResult, error) {
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
	envLabel := config.KubernetesNamespaceName(tenant, environment)
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

func resolveExposePlatformCoordinates(params ExposeServiceParams, store seam.ExposeStore) (zone, namespace, powerDNSDeployment, platformContext string, err error) {
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
		// Without platform.env the DNS write uses the current context, which is
		// right for the single-cluster deploy Job this override serves.
		return zone, config.NormalizeNamespaceName(namespace), opentarget.TenantResourcePrefix(platTenant) + "-powerdns", "", nil
	}

	platform := helmspec.ResolveProjectPlatform(params.ProjectRoot)
	if platform.IsZero() || strings.TrimSpace(platform.ServicesZone) == "" {
		return "", "", "", "", fmt.Errorf("a platform block with a base domain is required in .erun/config.yaml (the services zone tenant hostnames live under)")
	}
	if err := platform.Validate(); err != nil {
		return "", "", "", "", err
	}
	// Without platform.env the DNS exec would silently target the current namespace.
	if strings.TrimSpace(platform.Env) == "" {
		return "", "", "", "", fmt.Errorf("platform.env is required in .erun/config.yaml (the platform environment that runs the PowerDNS singleton)")
	}
	platTenant, _, _ := splitTenantEnv(platform.Env)
	return platform.ServicesZone, config.NormalizeNamespaceName(platform.Env), opentarget.TenantResourcePrefix(platTenant) + "-powerdns", resolvePlatformContext(store, platform.Env), nil
}

func resolveExposeBackendService(backendService, tenant, service string) string {
	if backendService = strings.TrimSpace(backendService); backendService != "" {
		return backendService
	}
	return fmt.Sprintf("%s-%s", opentarget.TenantResourcePrefix(tenant), service)
}

// resolveExposeTLSPlan disables TLS for --no-tls or when no DNS-01 broker can
// populate the secret; disabledReason names which.
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

// resolvePlatformContext is best-effort: "" falls back to the current context.
func resolvePlatformContext(store seam.ExposeStore, platformEnv string) string {
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

// splitTenantEnv splits on the first hyphen: tenant names contain none.
func splitTenantEnv(label string) (tenant, env string, ok bool) {
	label = strings.TrimSpace(label)
	i := strings.IndexByte(label, '-')
	if i <= 0 || i >= len(label)-1 {
		return "", "", false
	}
	return label[:i], label[i+1:], true
}

func normalizeExposeDependencies(store seam.ExposeStore, applyIngress IngressApplierFunc) (seam.ExposeStore, IngressApplierFunc) {
	if store == nil {
		store = config.ConfigStore{}
	}
	if applyIngress == nil {
		applyIngress = applyHostRoutingIngress
	}
	return store, applyIngress
}
