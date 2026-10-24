package platformclient

import (
	"context"
	"net/http"
	"net/url"
)

// ListEnvironments lists the caller's tenant's environments.
func (c *PlatformClient) ListEnvironments(ctx context.Context) ([]PlatformEnvironment, error) {
	var environments []PlatformEnvironment
	err := c.do(ctx, http.MethodGet, "/v1/environments", nil, true, &environments)
	return environments, err
}

// GetEnvironment fetches one environment by id.
func (c *PlatformClient) GetEnvironment(ctx context.Context, environmentID string) (PlatformEnvironment, error) {
	var environment PlatformEnvironment
	err := c.do(ctx, http.MethodGet, "/v1/environments/"+url.PathEscape(environmentID), nil, true, &environment)
	return environment, err
}

// PlatformMachineIdentity is one environment's own platform identity, as the
// API provisioned it: the client-credentials pair a token is minted from, the
// issuer that mints it, and what the resulting token resolves as.
//
// ClientSecret is the credential's confidential half, returned exactly once
// per provisioning call and placed straight into the environment's own secret
// store. It is never recorded anywhere in erun's own config: the alias entry
// written beside it carries only a reference.
type PlatformMachineIdentity struct {
	EnvironmentID   string `json:"environmentId"`
	EnvironmentName string `json:"environmentName"`
	Issuer          string `json:"issuer"`
	Subject         string `json:"subject"`
	ClientID        string `json:"clientId"`
	ClientSecret    string `json:"clientSecret"`
	UserID          string `json:"userId"`
	// AlreadyEnrolled is true when this environment already had an identity
	// and the call returned it rather than minting a new one.
	AlreadyEnrolled bool `json:"alreadyEnrolled"`
}

// MintMachineIdentity provisions (or returns) environmentID's own platform
// identity. Idempotent server-side, so a caller may call it on every deploy
// without minting a second identity.
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
	// Adopt records a row for an environment that already exists — its name,
	// type and kubernetes context, taken from wherever the caller already
	// runs it — instead of asking the platform to provision one. The
	// platform requires KubernetesContext and refuses RuntimeVersion/
	// ContextID when Adopt is set, and never starts a deploy for it.
	Adopt bool `json:"adopt,omitempty"`
	// TenantID places the row in a named tenant instead of the caller's own.
	// It is honoured only for an operations-tenant caller and refused with 403
	// for anyone else, so a client cannot use it to write into a tenant that
	// is not its own. The response's TenantID is the tenant that actually
	// resolved, which is what the hosted marker records.
	TenantID string `json:"tenantId,omitempty"`
}

// CreateEnvironment registers an environment, or — with Adopt set — records
// one that already exists without provisioning or deploying anything. When
// it is a runtime env with a pinned RuntimeVersion and the platform has a
// deploy executor configured, this also starts the server-side deploy (the
// response's Status moves registered -> provisioning -> running/failed; poll
// GetEnvironment).
func (c *PlatformClient) CreateEnvironment(ctx context.Context, params PlatformCreateEnvironmentParams) (PlatformEnvironment, error) {
	var environment PlatformEnvironment
	err := c.do(ctx, http.MethodPost, "/v1/environments", params, true, &environment)
	return environment, err
}

// PreviewCreateEnvironment resolves and returns the ordered plan the exact
// same params would run through CreateEnvironment, without creating
// anything — the register-preview entry point, distinct from Provision's own
// preview (/v1/provision), which cannot express a contextId, a
// runtimeVersion, or an adopt request. Always a successful preview
// (PlatformProvisionResult.QuotaOk names whether it can actually register)
// so a caller previewing a plan is never surprised by a refusal register
// itself would then hit differently.
func (c *PlatformClient) PreviewCreateEnvironment(ctx context.Context, params PlatformCreateEnvironmentParams) (PlatformProvisionResult, error) {
	body := struct {
		PlatformCreateEnvironmentParams
		Preview bool `json:"preview"`
	}{PlatformCreateEnvironmentParams: params, Preview: true}
	var result PlatformProvisionResult
	err := c.do(ctx, http.MethodPost, "/v1/environments", body, true, &result)
	return result, err
}

// PlatformDeployEnvironmentParams re-deploys at an explicit version; empty
// deploys the environment's own pinned RuntimeVersion.
type PlatformDeployEnvironmentParams struct {
	Version string `json:"version,omitempty"`
}

// DeployEnvironment starts a server-side deploy of an already-registered
// runtime environment. Errors ErrPlatformConflict when a deploy is already in
// flight, and ErrPlatformNotImplemented when the platform has no deploy
// executor configured.
func (c *PlatformClient) DeployEnvironment(ctx context.Context, environmentID string, params PlatformDeployEnvironmentParams) (PlatformEnvironment, error) {
	var environment PlatformEnvironment
	err := c.do(ctx, http.MethodPost, "/v1/environments/"+url.PathEscape(environmentID)+"/deploy", params, true, &environment)
	return environment, err
}

// StopEnvironment scales a runtime environment's Deployment to zero, the
// server-side equivalent of `erun stop`. Errors ErrPlatformNotImplemented
// when the platform has no deploy executor configured.
func (c *PlatformClient) StopEnvironment(ctx context.Context, environmentID string) (PlatformEnvironment, error) {
	var environment PlatformEnvironment
	err := c.do(ctx, http.MethodPost, "/v1/environments/"+url.PathEscape(environmentID)+"/stop", nil, true, &environment)
	return environment, err
}

// DeleteEnvironment starts tearing down a runtime environment's namespace and
// its row, the server-side equivalent of `erun delete`. The teardown itself
// runs asynchronously (#1140): this call returns as soon as the delete is
// claimed and the durable workflow behind it starts, not once the namespace
// is actually gone, since a stuck namespace finalizer can otherwise wedge for
// as long as Kubernetes is willing to sit in Terminating. The returned
// environment reflects the claim (Status "deleting"); poll GetEnvironment to
// watch it converge to gone (ErrPlatformNotFound) or "deletion-blocked"
// (DeleteError names why). Errors ErrPlatformConflict when a delete is
// already in progress, and ErrPlatformNotImplemented when the platform has no
// deploy executor configured.
func (c *PlatformClient) DeleteEnvironment(ctx context.Context, environmentID string) (PlatformEnvironment, error) {
	var environment PlatformEnvironment
	err := c.do(ctx, http.MethodDelete, "/v1/environments/"+url.PathEscape(environmentID), nil, true, &environment)
	return environment, err
}

// PlatformSetEnvironmentHostnameParams is PUT
// /v1/environments/{environment_id}/hostname's input: the IP
// the environment's own wildcard hostname should resolve to. A private or
// loopback address (e.g. 127.0.0.1, for a local cluster) is accepted on
// purpose.
type PlatformSetEnvironmentHostnameParams struct {
	TargetIP string `json:"targetIp"`
}

// PlatformEnvironmentHostname mirrors the hostname route's response: the
// wildcard hostname the write applied to, and the IP it now resolves to.
type PlatformEnvironmentHostname struct {
	Hostname string `json:"hostname"`
	TargetIP string `json:"targetIp,omitempty"`
}

// SetEnvironmentHostname points the caller's own environment's wildcard
// hostname at targetIP, performing the platform's own PowerDNS write on the
// caller's behalf. This is the write path a caller with no direct PowerDNS
// access to the platform cluster uses instead of `pdnsutil` (see
// erun-common's RunExposeService) — the same DNS record `erun expose`
// writes directly for a caller that does have that access (the hosted
// deploy Job). Errors ErrPlatformNotImplemented when the platform has no
// PowerDNS write path configured.
func (c *PlatformClient) SetEnvironmentHostname(ctx context.Context, environmentID string, params PlatformSetEnvironmentHostnameParams) (PlatformEnvironmentHostname, error) {
	var result PlatformEnvironmentHostname
	err := c.do(ctx, http.MethodPut, "/v1/environments/"+url.PathEscape(environmentID)+"/hostname", params, true, &result)
	return result, err
}

// DeleteEnvironmentHostname removes the caller's own environment's wildcard
// hostname record, symmetric with SetEnvironmentHostname — the platform-route
// counterpart to `erun unexpose`'s direct pdnsutil delete.
func (c *PlatformClient) DeleteEnvironmentHostname(ctx context.Context, environmentID string) error {
	return c.do(ctx, http.MethodDelete, "/v1/environments/"+url.PathEscape(environmentID)+"/hostname", nil, true, nil)
}

// ListContexts lists the caller's tenant's cloud contexts (managed clusters).
func (c *PlatformClient) ListContexts(ctx context.Context) ([]PlatformContext, error) {
	var contexts []PlatformContext
	err := c.do(ctx, http.MethodGet, "/v1/contexts", nil, true, &contexts)
	return contexts, err
}

// GetContext fetches one cloud context by id.
func (c *PlatformClient) GetContext(ctx context.Context, contextID string) (PlatformContext, error) {
	var cloudContext PlatformContext
	err := c.do(ctx, http.MethodGet, "/v1/contexts/"+url.PathEscape(contextID), nil, true, &cloudContext)
	return cloudContext, err
}

// PlatformCreateContextParams is the cluster-bootstrap registration input.
// Preview=true resolves and returns the plan without creating anything.
type PlatformCreateContextParams struct {
	Name               string `json:"name"`
	CloudProviderAlias string `json:"cloudProviderAlias"`
	Region             string `json:"region"`
	InstanceType       string `json:"instanceType,omitempty"`
	DiskType           string `json:"diskType,omitempty"`
	DiskSizeGB         int    `json:"diskSizeGb,omitempty"`
	Preview            bool   `json:"preview,omitempty"`
}

// PlatformCreateContextResult mirrors createContextResponse: Context is nil
// for a preview-only call.
type PlatformCreateContextResult struct {
	Context *PlatformContext `json:"context,omitempty"`
	Plan    []string         `json:"plan"`
}

// CreateContext registers a cloud context, or — with Preview set — only
// resolves and returns its bootstrap plan. When a provisioner is configured
// and this is a real (non-preview) create, the platform also starts the live
// cluster bootstrap (poll GetContext for its Status).
func (c *PlatformClient) CreateContext(ctx context.Context, params PlatformCreateContextParams) (PlatformCreateContextResult, error) {
	var result PlatformCreateContextResult
	err := c.do(ctx, http.MethodPost, "/v1/contexts", params, true, &result)
	return result, err
}

// PlatformProvisionParams is the combined "provision a hosted env" preview
// input. Exactly one of Context (bootstrap a new cluster) or
// KubernetesContext (reuse an existing one) should be set.
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

// PlatformProvisionResult is the preview result: always 200, since a preview
// surfaces a blocking quota decision in QuotaOk rather than failing the call.
type PlatformProvisionResult struct {
	Plan    []string `json:"plan"`
	QuotaOk bool     `json:"quotaOk"`
}

// Provision resolves and returns the full ordered plan for provisioning a
// hosted environment — tenant, quota, context bootstrap, namespace, register,
// deploy — without executing any of it or writing to the database.
func (c *PlatformClient) Provision(ctx context.Context, params PlatformProvisionParams) (PlatformProvisionResult, error) {
	var result PlatformProvisionResult
	err := c.do(ctx, http.MethodPost, "/v1/provision", params, true, &result)
	return result, err
}
