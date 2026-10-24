package platformclient

import (
	"context"
	"net/http"
	"net/url"
)

func (c *PlatformClient) ListEnvironments(ctx context.Context) ([]PlatformEnvironment, error) {
	var environments []PlatformEnvironment
	err := c.do(ctx, http.MethodGet, "/v1/environments", nil, true, &environments)
	return environments, err
}

func (c *PlatformClient) GetEnvironment(ctx context.Context, environmentID string) (PlatformEnvironment, error) {
	var environment PlatformEnvironment
	err := c.do(ctx, http.MethodGet, "/v1/environments/"+url.PathEscape(environmentID), nil, true, &environment)
	return environment, err
}

// PlatformMachineIdentity is an environment's own client-credentials identity.
// ClientSecret is returned once and must go only to the environment's secret store.
type PlatformMachineIdentity struct {
	EnvironmentID   string `json:"environmentId"`
	EnvironmentName string `json:"environmentName"`
	Issuer          string `json:"issuer"`
	Subject         string `json:"subject"`
	ClientID        string `json:"clientId"`
	ClientSecret    string `json:"clientSecret"`
	UserID          string `json:"userId"`
	// AlreadyEnrolled reports an existing identity was returned.
	AlreadyEnrolled bool `json:"alreadyEnrolled"`
}

// MintMachineIdentity provisions or returns the identity; safe to call on every deploy.
func (c *PlatformClient) MintMachineIdentity(ctx context.Context, environmentID string) (PlatformMachineIdentity, error) {
	var identity PlatformMachineIdentity
	err := c.do(ctx, http.MethodPost, "/v1/environments/"+url.PathEscape(environmentID)+"/machine-identity", nil, true, &identity)
	return identity, err
}

// PlatformCreateEnvironmentParams is the environment-registration input.
type PlatformCreateEnvironmentParams struct {
	Name              string `json:"name"`
	Type              string `json:"type"`
	ContextID         string `json:"contextId,omitempty"`
	KubernetesContext string `json:"kubernetesContext,omitempty"`
	RuntimeVersion    string `json:"runtimeVersion,omitempty"`
	// Adopt records an existing environment without provisioning or deploying it;
	// it requires KubernetesContext and forbids RuntimeVersion and ContextID.
	Adopt bool `json:"adopt,omitempty"`
	// TenantID is honoured only for operations-tenant callers (others get 403).
	TenantID string `json:"tenantId,omitempty"`
}

// CreateEnvironment registers an environment and may start a server-side deploy;
// poll GetEnvironment for its Status.
func (c *PlatformClient) CreateEnvironment(ctx context.Context, params PlatformCreateEnvironmentParams) (PlatformEnvironment, error) {
	var environment PlatformEnvironment
	err := c.do(ctx, http.MethodPost, "/v1/environments", params, true, &environment)
	return environment, err
}

// PreviewCreateEnvironment returns CreateEnvironment's plan without creating anything.
// It always succeeds; QuotaOk says whether register would be allowed.
func (c *PlatformClient) PreviewCreateEnvironment(ctx context.Context, params PlatformCreateEnvironmentParams) (PlatformProvisionResult, error) {
	body := struct {
		PlatformCreateEnvironmentParams
		Preview bool `json:"preview"`
	}{PlatformCreateEnvironmentParams: params, Preview: true}
	var result PlatformProvisionResult
	err := c.do(ctx, http.MethodPost, "/v1/environments", body, true, &result)
	return result, err
}

// PlatformDeployEnvironmentParams.Version empty deploys the pinned RuntimeVersion.
type PlatformDeployEnvironmentParams struct {
	Version string `json:"version,omitempty"`
}

// DeployEnvironment returns ErrPlatformConflict when a deploy is in flight.
func (c *PlatformClient) DeployEnvironment(ctx context.Context, environmentID string, params PlatformDeployEnvironmentParams) (PlatformEnvironment, error) {
	var environment PlatformEnvironment
	err := c.do(ctx, http.MethodPost, "/v1/environments/"+url.PathEscape(environmentID)+"/deploy", params, true, &environment)
	return environment, err
}

// StopEnvironment scales a runtime environment's Deployment to zero.
func (c *PlatformClient) StopEnvironment(ctx context.Context, environmentID string) (PlatformEnvironment, error) {
	var environment PlatformEnvironment
	err := c.do(ctx, http.MethodPost, "/v1/environments/"+url.PathEscape(environmentID)+"/stop", nil, true, &environment)
	return environment, err
}

// DeleteEnvironment claims the delete and returns before teardown finishes, since
// a stuck finalizer can wedge indefinitely; poll GetEnvironment.
func (c *PlatformClient) DeleteEnvironment(ctx context.Context, environmentID string) (PlatformEnvironment, error) {
	var environment PlatformEnvironment
	err := c.do(ctx, http.MethodDelete, "/v1/environments/"+url.PathEscape(environmentID), nil, true, &environment)
	return environment, err
}

// PlatformSetEnvironmentHostnameParams accepts private and loopback IPs on purpose.
type PlatformSetEnvironmentHostnameParams struct {
	TargetIP string `json:"targetIp"`
}

// PlatformEnvironmentHostname mirrors the hostname route's response.
type PlatformEnvironmentHostname struct {
	Hostname string `json:"hostname"`
	TargetIP string `json:"targetIp,omitempty"`
}

// SetEnvironmentHostname has the platform write the wildcard DNS record, for
// callers without direct PowerDNS access.
func (c *PlatformClient) SetEnvironmentHostname(ctx context.Context, environmentID string, params PlatformSetEnvironmentHostnameParams) (PlatformEnvironmentHostname, error) {
	var result PlatformEnvironmentHostname
	err := c.do(ctx, http.MethodPut, "/v1/environments/"+url.PathEscape(environmentID)+"/hostname", params, true, &result)
	return result, err
}

// DeleteEnvironmentHostname removes the environment's wildcard hostname record.
func (c *PlatformClient) DeleteEnvironmentHostname(ctx context.Context, environmentID string) error {
	return c.do(ctx, http.MethodDelete, "/v1/environments/"+url.PathEscape(environmentID)+"/hostname", nil, true, nil)
}

func (c *PlatformClient) ListContexts(ctx context.Context) ([]PlatformContext, error) {
	var contexts []PlatformContext
	err := c.do(ctx, http.MethodGet, "/v1/contexts", nil, true, &contexts)
	return contexts, err
}

func (c *PlatformClient) GetContext(ctx context.Context, contextID string) (PlatformContext, error) {
	var cloudContext PlatformContext
	err := c.do(ctx, http.MethodGet, "/v1/contexts/"+url.PathEscape(contextID), nil, true, &cloudContext)
	return cloudContext, err
}

// PlatformCreateContextParams registers a cluster; Preview only returns the plan.
type PlatformCreateContextParams struct {
	Name               string `json:"name"`
	CloudProviderAlias string `json:"cloudProviderAlias"`
	Region             string `json:"region"`
	InstanceType       string `json:"instanceType,omitempty"`
	DiskType           string `json:"diskType,omitempty"`
	DiskSizeGB         int    `json:"diskSizeGb,omitempty"`
	Preview            bool   `json:"preview,omitempty"`
}

// PlatformCreateContextResult has a nil Context for a preview.
type PlatformCreateContextResult struct {
	Context *PlatformContext `json:"context,omitempty"`
	Plan    []string         `json:"plan"`
}

// CreateContext registers a cloud context and may start its bootstrap.
func (c *PlatformClient) CreateContext(ctx context.Context, params PlatformCreateContextParams) (PlatformCreateContextResult, error) {
	var result PlatformCreateContextResult
	err := c.do(ctx, http.MethodPost, "/v1/contexts", params, true, &result)
	return result, err
}

// PlatformProvisionParams sets exactly one of Context or KubernetesContext.
type PlatformProvisionParams struct {
	Environment       PlatformProvisionEnvironment `json:"environment"`
	Context           *PlatformProvisionContext    `json:"context,omitempty"`
	KubernetesContext string                       `json:"kubernetesContext,omitempty"`
}

type PlatformProvisionEnvironment struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type PlatformProvisionContext struct {
	Name               string `json:"name"`
	CloudProviderAlias string `json:"cloudProviderAlias"`
	Region             string `json:"region"`
	InstanceType       string `json:"instanceType,omitempty"`
	DiskType           string `json:"diskType,omitempty"`
	DiskSizeGB         int    `json:"diskSizeGb,omitempty"`
}

// PlatformProvisionResult reports a quota block in QuotaOk instead of failing.
type PlatformProvisionResult struct {
	Plan    []string `json:"plan"`
	QuotaOk bool     `json:"quotaOk"`
}

// Provision returns the full provisioning plan without executing or persisting it.
func (c *PlatformClient) Provision(ctx context.Context, params PlatformProvisionParams) (PlatformProvisionResult, error) {
	var result PlatformProvisionResult
	err := c.do(ctx, http.MethodPost, "/v1/provision", params, true, &result)
	return result, err
}
