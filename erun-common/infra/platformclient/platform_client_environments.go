package platformclient

import (
	"context"
	"net/http"
	"net/url"

	"github.com/sophium/erun/erun-common/base/model"
)

func (c *PlatformClient) ListEnvironments(ctx context.Context) ([]model.PlatformEnvironment, error) {
	var environments []model.PlatformEnvironment
	err := c.do(ctx, http.MethodGet, "/v1/environments", nil, true, &environments)
	return environments, err
}

func (c *PlatformClient) GetEnvironment(ctx context.Context, environmentID string) (model.PlatformEnvironment, error) {
	var environment model.PlatformEnvironment
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

// CreateEnvironment registers an environment and may start a server-side deploy;
// poll GetEnvironment for its Status.
func (c *PlatformClient) CreateEnvironment(ctx context.Context, params model.PlatformCreateEnvironmentParams) (model.PlatformEnvironment, error) {
	var environment model.PlatformEnvironment
	err := c.do(ctx, http.MethodPost, "/v1/environments", params, true, &environment)
	return environment, err
}

// PreviewCreateEnvironment returns CreateEnvironment's plan without creating anything.
// It always succeeds; QuotaOk says whether register would be allowed.
func (c *PlatformClient) PreviewCreateEnvironment(ctx context.Context, params model.PlatformCreateEnvironmentParams) (model.PlatformProvisionResult, error) {
	body := struct {
		model.PlatformCreateEnvironmentParams
		Preview bool `json:"preview"`
	}{PlatformCreateEnvironmentParams: params, Preview: true}
	var result model.PlatformProvisionResult
	err := c.do(ctx, http.MethodPost, "/v1/environments", body, true, &result)
	return result, err
}

// DeployEnvironment returns ErrPlatformConflict when a deploy is in flight.
func (c *PlatformClient) DeployEnvironment(ctx context.Context, environmentID string, params model.PlatformDeployEnvironmentParams) (model.PlatformEnvironment, error) {
	var environment model.PlatformEnvironment
	err := c.do(ctx, http.MethodPost, "/v1/environments/"+url.PathEscape(environmentID)+"/deploy", params, true, &environment)
	return environment, err
}

// StopEnvironment scales a runtime environment's Deployment to zero.
func (c *PlatformClient) StopEnvironment(ctx context.Context, environmentID string) (model.PlatformEnvironment, error) {
	var environment model.PlatformEnvironment
	err := c.do(ctx, http.MethodPost, "/v1/environments/"+url.PathEscape(environmentID)+"/stop", nil, true, &environment)
	return environment, err
}

// DeleteEnvironment claims the delete and returns before teardown finishes, since
// a stuck finalizer can wedge indefinitely; poll GetEnvironment.
func (c *PlatformClient) DeleteEnvironment(ctx context.Context, environmentID string) (model.PlatformEnvironment, error) {
	var environment model.PlatformEnvironment
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

func (c *PlatformClient) ListContexts(ctx context.Context) ([]model.PlatformContext, error) {
	var contexts []model.PlatformContext
	err := c.do(ctx, http.MethodGet, "/v1/contexts", nil, true, &contexts)
	return contexts, err
}

func (c *PlatformClient) GetContext(ctx context.Context, contextID string) (model.PlatformContext, error) {
	var cloudContext model.PlatformContext
	err := c.do(ctx, http.MethodGet, "/v1/contexts/"+url.PathEscape(contextID), nil, true, &cloudContext)
	return cloudContext, err
}

// CreateContext registers a cloud context and may start its bootstrap.
func (c *PlatformClient) CreateContext(ctx context.Context, params model.PlatformCreateContextParams) (model.PlatformCreateContextResult, error) {
	var result model.PlatformCreateContextResult
	err := c.do(ctx, http.MethodPost, "/v1/contexts", params, true, &result)
	return result, err
}

// Provision returns the full provisioning plan without executing or persisting it.
func (c *PlatformClient) Provision(ctx context.Context, params model.PlatformProvisionParams) (model.PlatformProvisionResult, error) {
	var result model.PlatformProvisionResult
	err := c.do(ctx, http.MethodPost, "/v1/provision", params, true, &result)
	return result, err
}
