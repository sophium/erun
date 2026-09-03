package eruncommon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// PlatformClient is the transport-neutral client for a hosted erun platform's
// erun-backend-api: the contract erun-cli and erun-mcp both drive in stage B,
// kept free of Cobra and the MCP SDK so it stays usable as a plain library.
// Every method takes a bearer minted immediately before the request (see
// PlatformTokenMinter), mirroring the MCP client's minted-per-call token so a
// slow call cannot fail because the token aged out mid-flight.

// PlatformTokenMinter returns a fresh bearer token, invoked immediately before
// each request.
type PlatformTokenMinter func() (string, error)

// Sentinel errors a caller can distinguish with errors.Is. erun-backend-api
// usually returns a plain-text body (http.Error), so these carry the response
// body as their message rather than parsing one; an endpoint whose refusal
// carries structured detail (AdvanceMergeQueue's unresolved-thread block, for
// example) returns a JSON body instead, and decorates the same sentinel with
// a typed error a caller can pull out via errors.As — see
// PlatformMergeQueueBlockedError.
var (
	ErrPlatformUnauthorized   = errors.New("platform api: unauthorized")
	ErrPlatformForbidden      = errors.New("platform api: forbidden")
	ErrPlatformNotFound       = errors.New("platform api: not found")
	ErrPlatformConflict       = errors.New("platform api: conflict")
	ErrPlatformNotImplemented = errors.New("platform api: not implemented")
	ErrPlatformRateLimited    = errors.New("platform api: rate limited")
)

// MCPToolAuditHeader carries the calling MCP tool's name on a platform
// request, so erun-backend-api's audit middleware can record the call as an
// MCP-driven action (type MCP, mcp_tool set) instead of its default API
// classification. erun-backend-api reads the same constant so the header name
// cannot drift between the two sides.
const MCPToolAuditHeader = "X-Erun-Mcp-Tool"

// PlatformClient talks to one erun-backend-api instance.
type PlatformClient struct {
	baseURL      string
	mint         PlatformTokenMinter
	usernameHint string
	mcpTool      string
	http         *http.Client
}

// NewPlatformClient builds a client against baseURL (an erun-backend-api base,
// e.g. the apiUrl a GET /v1/platform discovery response reports). mint may be
// nil for the one endpoint that needs no token (PlatformInfo).
func NewPlatformClient(baseURL string, mint PlatformTokenMinter) *PlatformClient {
	return &PlatformClient{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		mint:    mint,
		// No timeout: the deadline belongs to the caller's context.Context, same
		// convention as the MCP client.
		http: &http.Client{},
	}
}

// WithUsernameHint returns a copy of c that sends username as the
// X-ERun-Username hint. erun-backend-api names a user it enrols (or renames)
// from that header, which is how a caller whose token carries no username
// claim — an AWS STS bearer, for one — still gets a readable identity.
func (c *PlatformClient) WithUsernameHint(username string) *PlatformClient {
	hinted := *c
	hinted.usernameHint = strings.TrimSpace(username)
	return &hinted
}

// WithMCPTool returns a copy of c that sends tool as the MCPToolAuditHeader,
// so erun-backend-api's audit trail attributes this call to the MCP tool
// invocation that made it rather than recording it as a plain API request.
func (c *PlatformClient) WithMCPTool(tool string) *PlatformClient {
	tagged := *c
	tagged.mcpTool = strings.TrimSpace(tool)
	return &tagged
}

// PlatformInfo mirrors GET /v1/platform's response. Duplicated here (rather
// than shared with erun-backend-api/internal/routes.PlatformInfo) because that
// package is internal and erun-backend-api depends on erun-common, not the
// reverse.
type PlatformInfo struct {
	Issuer          string `json:"issuer"`
	APIURL          string `json:"apiUrl"`
	ConsoleURL      string `json:"consoleUrl"`
	ConsoleClientID string `json:"consoleClientId"`
	CLIClientID     string `json:"cliClientId"`
	Brand           string `json:"brand"`
	// Version is the build actually serving this response -- see
	// routes.PlatformInfo.Version. "dev" when the serving binary was never
	// stamped with a real one.
	Version string `json:"version"`
}

// PlatformWhoami mirrors GET /v1/whoami's response. Capabilities is what a
// client gates its surfaces on; Roles is descriptive only.
type PlatformWhoami struct {
	TenantID string   `json:"tenantId"`
	UserID   string   `json:"userId"`
	Username string   `json:"username,omitempty"`
	Roles    []string `json:"roles,omitempty"`
	// Capabilities is nil when the platform did not answer with one, and
	// non-nil but empty when the caller may do nothing. Do not conflate them —
	// see PlatformCapabilities.Known.
	Capabilities PlatformCapabilities `json:"capabilities"`
	Issuer       string               `json:"issuer"`
	Subject      string               `json:"subject"`
}

// PlatformTenant mirrors model.Tenant's JSON shape.
type PlatformTenant struct {
	TenantID  string    `json:"tenantId"`
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	// Resolvable reports whether any of this tenant's registered issuer
	// mappings can resolve a token at all — false means no sign-in can ever
	// reach it, whatever account is used. Only the operations-scoped
	// GET /v1/tenants listing computes it; nil everywhere else means "this
	// read did not ask", never "it works".
	Resolvable *bool `json:"resolvable,omitempty"`
}

// PlatformUser mirrors model.User's JSON shape (plus AlreadyEnrolled, which
// only POST /v1/users' response sets).
type PlatformUser struct {
	UserID   string `json:"userId"`
	TenantID string `json:"tenantId"`
	Username string `json:"username"`
	Issuer   string `json:"issuer,omitempty"`
	Subject  string `json:"subject,omitempty"`
	// AlreadyEnrolled is true when POST /v1/users found the requested external
	// identity already enrolled in the target tenant and left it untouched
	// (a no-op) instead of creating a new user — the caller asked to enroll an
	// identity that was already usable, so this reports the existing user
	// rather than a false username-collision conflict.
	AlreadyEnrolled bool      `json:"alreadyEnrolled,omitempty"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

// PlatformEnvironment mirrors model.Environment's JSON shape.
type PlatformEnvironment struct {
	EnvironmentID     string `json:"environmentId"`
	TenantID          string `json:"tenantId"`
	Name              string `json:"name"`
	Type              string `json:"type"`
	KubernetesContext string `json:"kubernetesContext,omitempty"`
	ContextID         string `json:"contextId,omitempty"`
	RuntimeVersion    string `json:"runtimeVersion,omitempty"`
	Status            string `json:"status"`
	ProvisionError    string `json:"provisionError,omitempty"`
	DeployedVersion   string `json:"deployedVersion,omitempty"`
	// DeleteError names why a delete attempt did not tear the namespace down
	// (the namespace's own conditions, verbatim, when it is stuck on an
	// unsatisfiable finalizer) when Status is "deletion-blocked" (#1140).
	DeleteError string    `json:"deleteError,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// PlatformContext mirrors model.Context's JSON shape.
type PlatformContext struct {
	ContextID          string    `json:"contextId"`
	TenantID           string    `json:"tenantId"`
	Name               string    `json:"name"`
	Provider           string    `json:"provider"`
	CloudProviderAlias string    `json:"cloudProviderAlias,omitempty"`
	Region             string    `json:"region,omitempty"`
	InstanceID         string    `json:"instanceId,omitempty"`
	PublicIP           string    `json:"publicIp,omitempty"`
	InstanceType       string    `json:"instanceType,omitempty"`
	DiskType           string    `json:"diskType,omitempty"`
	DiskSizeGB         int       `json:"diskSizeGb,omitempty"`
	KubernetesContext  string    `json:"kubernetesContext,omitempty"`
	Status             string    `json:"status"`
	ProvisionError     string    `json:"provisionError,omitempty"`
	CreatedAt          time.Time `json:"createdAt"`
	UpdatedAt          time.Time `json:"updatedAt"`
}

// PlatformConfig mirrors GET /v1/config's response — the console read model.
type PlatformConfigResponse struct {
	Tenant       PlatformTenant        `json:"tenant"`
	Environments []PlatformEnvironment `json:"environments"`
	Contexts     []PlatformContext     `json:"contexts"`
	// InviteRequestRateLimitWindowSeconds is the current per-identity
	// invite-request submission window: the number of seconds a caller must
	// wait between two POST /v1/invite-requests calls for the same verified
	// (issuer, subject). Operator-editable only from erun-console (PATCH
	// /v1/config/invite-request-rate-limit, called directly over RTK Query) --
	// there is no Go transport for that write, so PlatformClient has no
	// corresponding method.
	InviteRequestRateLimitWindowSeconds int `json:"inviteRequestRateLimitWindowSeconds"`
}

// Invite-request kinds and statuses mirror
// erun-backend-api/internal/model.InviteRequestKind/InviteRequestStatus.
const (
	PlatformInviteRequestKindJoinTenant   = "JOIN_TENANT"
	PlatformInviteRequestKindCreateTenant = "CREATE_TENANT"

	PlatformInviteRequestStatusPending  = "PENDING"
	PlatformInviteRequestStatusApproved = "APPROVED"
	PlatformInviteRequestStatusDeclined = "DECLINED"
)

// PlatformInviteRequest mirrors model.InviteRequest's JSON shape: a
// verified-identity request to join or create a tenant, and the platform
// operator/admin's decision on it.
type PlatformInviteRequest struct {
	InviteRequestID string `json:"inviteRequestId"`
	Issuer          string `json:"issuer"`
	Subject         string `json:"subject"`
	Email           string `json:"email,omitempty"`
	DisplayName     string `json:"displayName,omitempty"`
	Kind            string `json:"kind"`
	TenantName      string `json:"tenantName"`
	EnvironmentName string `json:"environmentName,omitempty"`
	Note            string `json:"note,omitempty"`
	Status          string `json:"status"`
	DecidedByUserID string `json:"decidedByUserId,omitempty"`
	DeclineReason   string `json:"declineReason,omitempty"`
	// MintedInviteID/Token/ExpiresAt are populated once Status is APPROVED:
	// the underlying POST /v1/invites row minted for the requester, joined
	// live from the invites table.
	MintedInviteID        string     `json:"mintedInviteId,omitempty"`
	MintedInviteToken     string     `json:"mintedInviteToken,omitempty"`
	MintedInviteExpiresAt *time.Time `json:"mintedInviteExpiresAt,omitempty"`
	CreatedAt             time.Time  `json:"createdAt"`
	UpdatedAt             time.Time  `json:"updatedAt"`
}

// Platform fetches this instance's own self-describing config. Unauthenticated:
// it is how a caller discovers the issuer and client ids before it has a
// token, so it never mints one.
func (c *PlatformClient) Platform(ctx context.Context) (PlatformInfo, error) {
	var info PlatformInfo
	err := c.do(ctx, http.MethodGet, "/v1/platform", nil, false, &info)
	return info, err
}

// Whoami returns the caller's resolved identity.
func (c *PlatformClient) Whoami(ctx context.Context) (PlatformWhoami, error) {
	var whoami PlatformWhoami
	err := c.do(ctx, http.MethodGet, "/v1/whoami", nil, true, &whoami)
	return whoami, err
}

// Config returns the console read model: the caller's tenant, environments,
// and contexts.
func (c *PlatformClient) Config(ctx context.Context) (PlatformConfigResponse, error) {
	var config PlatformConfigResponse
	err := c.do(ctx, http.MethodGet, "/v1/config", nil, true, &config)
	return config, err
}

// PlatformCreateTenantParams is the operations-only tenant-registration input.
type PlatformCreateTenantParams struct {
	Name          string `json:"name"`
	Type          string `json:"type"`
	Issuer        string `json:"issuer"`
	OrgFieldKey   string `json:"orgFieldKey,omitempty"`
	OrgFieldValue string `json:"orgFieldValue,omitempty"`
	DisplayName   string `json:"displayName,omitempty"`
}

// CreateTenant registers a new tenant. Requires an operations-tenant caller.
func (c *PlatformClient) CreateTenant(ctx context.Context, params PlatformCreateTenantParams) (PlatformTenant, error) {
	var tenant PlatformTenant
	err := c.do(ctx, http.MethodPost, "/v1/tenants", params, true, &tenant)
	return tenant, err
}

// ListTenants lists tenants visible to the caller: every tenant for an
// operations-scoped caller, or a single-item list containing only the
// caller's own tenant otherwise.
func (c *PlatformClient) ListTenants(ctx context.Context) ([]PlatformTenant, error) {
	var tenants []PlatformTenant
	err := c.do(ctx, http.MethodGet, "/v1/tenants", nil, true, &tenants)
	return tenants, err
}

// PlatformOrg mirrors zitadel.Org's JSON shape: the platform's own IdP
// organization that an org-scoped tenant's mapping points at.
type PlatformOrg struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// PlatformCreateOrgParams is the org-creation input.
type PlatformCreateOrgParams struct {
	Name string `json:"name"`
}

// CreateOrg creates an organization on the platform's own IdP: the org an
// org-scoped tenant's mapping needs before it can resolve any token —
// POST /v1/tenants refuses an org-scoped mapping with no org value, and this
// is the reachable way to obtain one. Requires an operations-tenant caller.
// Pair the returned id with CreateTenant's OrgFieldValue.
func (c *PlatformClient) CreateOrg(ctx context.Context, params PlatformCreateOrgParams) (PlatformOrg, error) {
	var org PlatformOrg
	err := c.do(ctx, http.MethodPost, "/v1/identity/orgs", params, true, &org)
	return org, err
}

// PlatformTenantIssuer mirrors model.TenantIssuer's JSON shape.
type PlatformTenantIssuer struct {
	TenantID      string `json:"tenantId"`
	Issuer        string `json:"issuer"`
	Name          string `json:"name"`
	OrgFieldKey   string `json:"orgFieldKey,omitempty"`
	OrgFieldValue string `json:"orgFieldValue,omitempty"`
}

// PlatformRepairTenantIssuerOrgMappingParams is the repair-path input for a
// tenant already stuck with an unresolvable (issuer, org) mapping. TenantID,
// when set, targets a tenant other than the caller's own and is honored only
// for an operations-scoped caller.
type PlatformRepairTenantIssuerOrgMappingParams struct {
	TenantID      string `json:"tenantId,omitempty"`
	Issuer        string `json:"issuer"`
	OrgFieldKey   string `json:"orgFieldKey"`
	OrgFieldValue string `json:"orgFieldValue"`
}

// RepairTenantIssuerOrgMapping converts issuer to org-scoped (if it is not
// already) and sets the target tenant's own org value, so a tenant created
// with an empty --org-field-value before POST /v1/tenants started refusing
// that (or converted from a single-tenant issuer after a second tenant needed
// one) resolves again. Requires an operations-tenant caller.
func (c *PlatformClient) RepairTenantIssuerOrgMapping(ctx context.Context, params PlatformRepairTenantIssuerOrgMappingParams) (PlatformTenantIssuer, error) {
	var issuer PlatformTenantIssuer
	err := c.do(ctx, http.MethodPatch, "/v1/tenant-issuers", params, true, &issuer)
	return issuer, err
}

// PlatformCreateUserParams is the user-enrollment input. TenantID, when set,
// targets a tenant other than the caller's own and is honored only for an
// operations-scoped caller.
type PlatformCreateUserParams struct {
	Username string `json:"username"`
	Issuer   string `json:"issuer,omitempty"`
	Subject  string `json:"subject,omitempty"`
	TenantID string `json:"tenantId,omitempty"`
	// RoleIDs are the roles the enrollment grants. Empty leaves the platform's
	// own default (TenantUser, or TenantAdmin for a tenant's first user), so
	// naming roles here is what enrolls a tenant's administrator directly
	// instead of a member who then needs elevating from inside the tenant —
	// the elevation an unusable admin makes impossible.
	RoleIDs []string `json:"roleIds,omitempty"`
}

// CreateUser enrolls a user.
func (c *PlatformClient) CreateUser(ctx context.Context, params PlatformCreateUserParams) (PlatformUser, error) {
	var user PlatformUser
	err := c.do(ctx, http.MethodPost, "/v1/users", params, true, &user)
	return user, err
}

// PlatformListUsersParams optionally targets another tenant, honored only for
// an operations-scoped caller.
type PlatformListUsersParams struct {
	TenantID string
}

// ListUsers lists the target tenant's users.
func (c *PlatformClient) ListUsers(ctx context.Context, params PlatformListUsersParams) ([]PlatformUser, error) {
	path := "/v1/users"
	if strings.TrimSpace(params.TenantID) != "" {
		path += "?tenantId=" + url.QueryEscape(params.TenantID)
	}
	var users []PlatformUser
	err := c.do(ctx, http.MethodGet, path, nil, true, &users)
	return users, err
}

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

// PlatformSubmitInviteRequestParams is the unauthenticated-to-tenant
// invite-request submission input (POST /v1/invite-requests). The caller
// still authenticates with a bearer token verified against a trusted issuer
// — it just has no tenant membership yet, which is the whole point of the
// request. Issuer/Subject are never sent: the platform reads them from the
// verified token itself.
type PlatformSubmitInviteRequestParams struct {
	Email           string `json:"email,omitempty"`
	DisplayName     string `json:"displayName,omitempty"`
	Kind            string `json:"kind"`
	TenantName      string `json:"tenantName"`
	EnvironmentName string `json:"environmentName,omitempty"`
	Note            string `json:"note,omitempty"`
}

// SubmitInviteRequest submits (or, for a caller with an existing PENDING
// request, updates in place) a request to join or create a tenant. Requires
// only a verified bearer, never tenant resolution — errors
// ErrPlatformRateLimited (with Retry-After on the returned
// *PlatformStatusError) when the caller's identity is inside the
// platform-configured submission window.
func (c *PlatformClient) SubmitInviteRequest(ctx context.Context, params PlatformSubmitInviteRequestParams) (PlatformInviteRequest, error) {
	var request PlatformInviteRequest
	err := c.do(ctx, http.MethodPost, "/v1/invite-requests", params, true, &request)
	return request, err
}

// MyInviteRequest returns the caller's own most recent invite request (any
// status), resolved from the verified bearer's (issuer, subject) — the one
// thing a requester with no tenant membership can check while waiting.
// Errors ErrPlatformNotFound when the caller has never submitted one.
func (c *PlatformClient) MyInviteRequest(ctx context.Context) (PlatformInviteRequest, error) {
	var request PlatformInviteRequest
	err := c.do(ctx, http.MethodGet, "/v1/invite-requests/mine", nil, true, &request)
	return request, err
}

// PlatformListInviteRequestsParams filters the operator/admin queue. Kind is
// honored only for an operations-scoped caller; a tenant-scoped caller
// always sees only JOIN_TENANT requests naming their own tenant.
type PlatformListInviteRequestsParams struct {
	Status string
	Kind   string
}

// ListInviteRequests lists invite requests visible to the caller, oldest
// first. Requires TenantUserClass.
func (c *PlatformClient) ListInviteRequests(ctx context.Context, params PlatformListInviteRequestsParams) ([]PlatformInviteRequest, error) {
	query := url.Values{}
	if strings.TrimSpace(params.Status) != "" {
		query.Set("status", params.Status)
	}
	if strings.TrimSpace(params.Kind) != "" {
		query.Set("kind", params.Kind)
	}
	path := "/v1/invite-requests"
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var requests []PlatformInviteRequest
	err := c.do(ctx, http.MethodGet, path, nil, true, &requests)
	return requests, err
}

// ApproveInviteRequest approves a pending invite request: for a JOIN_TENANT
// request, enrolls the requester into the caller's own tenant; for a
// CREATE_TENANT request, registers the tenant (requires an operations-scoped
// caller) and enrolls the requester as its first user. Either way, mints an
// invite through the same path POST /v1/invites uses. Requires
// TenantAdminOnly. Errors ErrPlatformConflict when the request was already
// decided, ErrPlatformForbidden when the caller lacks authority over it.
func (c *PlatformClient) ApproveInviteRequest(ctx context.Context, inviteRequestID string) (PlatformInviteRequest, error) {
	var request PlatformInviteRequest
	err := c.do(ctx, http.MethodPost, "/v1/invite-requests/"+url.PathEscape(inviteRequestID)+"/approve", nil, true, &request)
	return request, err
}

// PlatformDeclineInviteRequestParams carries the required decline reason: a
// decline with no reason reaches nobody, so the API refuses an empty one.
type PlatformDeclineInviteRequestParams struct {
	Reason string `json:"reason"`
}

// DeclineInviteRequest declines a pending invite request with a reason the
// requester will see. Requires TenantAdminOnly. Errors ErrPlatformConflict
// when the request was already decided, ErrPlatformForbidden when the caller
// lacks authority over it.
func (c *PlatformClient) DeclineInviteRequest(ctx context.Context, inviteRequestID string, params PlatformDeclineInviteRequestParams) (PlatformInviteRequest, error) {
	var request PlatformInviteRequest
	err := c.do(ctx, http.MethodPost, "/v1/invite-requests/"+url.PathEscape(inviteRequestID)+"/decline", params, true, &request)
	return request, err
}

// do is the single request path every method above funnels through: build,
// authenticate (unless authenticate is false), send, and decode — or map the
// status to a typed error.
func (c *PlatformClient) do(ctx context.Context, method string, path string, body any, authenticate bool, out any) error {
	req, err := c.buildRequest(ctx, method, path, body, authenticate)
	if err != nil {
		return err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("call platform api %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return platformStatusError(method, path, resp.StatusCode, respBody, resp.Header)
	}
	return decodePlatformResponse(method, path, respBody, out)
}

// buildRequest encodes body (if any), attaches the standard headers, and
// mints + attaches a bearer token when authenticate is set.
func (c *PlatformClient) buildRequest(ctx context.Context, method string, path string, body any, authenticate bool) (*http.Request, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode platform api request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return nil, fmt.Errorf("build platform api request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if c.usernameHint != "" {
		req.Header.Set("X-ERun-Username", c.usernameHint)
	}
	if c.mcpTool != "" {
		req.Header.Set(MCPToolAuditHeader, c.mcpTool)
	}
	if !authenticate {
		return req, nil
	}
	if c.mint == nil {
		return nil, fmt.Errorf("platform api: no token minter is configured for an authenticated call")
	}
	token, err := c.mint()
	if err != nil {
		return nil, fmt.Errorf("mint platform api bearer token: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return req, nil
}

func decodePlatformResponse(method string, path string, respBody []byte, out any) error {
	if out == nil || len(strings.TrimSpace(string(respBody))) == 0 {
		return nil
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("decode platform api %s %s response: %w", method, path, err)
	}
	return nil
}

// PlatformStatusError is the structured form behind every non-2xx platform
// api response. errors.Is still matches the mapped sentinel (ErrPlatformConflict
// and friends) via Unwrap; a caller whose endpoint can return a structured
// JSON body on top of the sentinel — AdvanceMergeQueue's unresolved-thread
// block, for example — reaches it with errors.As against this type and
// decodes Body itself, rather than re-parsing the formatted Error() string.
type PlatformStatusError struct {
	Method string
	Path   string
	Status int
	Body   []byte
	// Header carries the raw response headers — in particular Retry-After,
	// RateLimit-Limit, RateLimit-Remaining, and RateLimit-Reset on a 429 from
	// the invite-request submission limiter.
	Header   http.Header
	sentinel error
}

// RetryAfter parses the response's Retry-After header (seconds, the only
// form erun-backend-api's limiter sends) as a duration. ok is false when the
// header is absent or unparseable.
func (e *PlatformStatusError) RetryAfter() (delay time.Duration, ok bool) {
	raw := strings.TrimSpace(e.Header.Get("Retry-After"))
	if raw == "" {
		return 0, false
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds < 0 {
		return 0, false
	}
	return time.Duration(seconds) * time.Second, true
}

func (e *PlatformStatusError) Error() string {
	detail := strings.TrimSpace(string(e.Body))
	base := fmt.Sprintf("platform api %s %s: http %d", e.Method, e.Path, e.Status)
	if detail != "" {
		base += ": " + detail
	}
	if e.sentinel != nil {
		base += ": " + e.sentinel.Error()
	}
	return base
}

func (e *PlatformStatusError) Unwrap() error {
	return e.sentinel
}

// platformAuthErrorEnvelope mirrors the {code, message} JSON envelope
// erun-backend-api's pre-route auth layer sends on every 401
// (auth.go's authErrorEnvelope). TENANT_UNRESOLVED, NOT_ENROLLED, and
// RESOLUTION_FAILED are three different situations behind the same HTTP
// status; a caller checking only ErrPlatformUnauthorized cannot tell them
// apart.
type platformAuthErrorEnvelope struct {
	Code string `json:"code"`
}

// PlatformAuthErrorCode reports the machine-readable code a 401 response
// carried (e.g. "TENANT_UNRESOLVED", "NOT_ENROLLED", "RESOLUTION_FAILED"), or
// "" when err is not a 401 PlatformStatusError, or its body carried no
// recognizable code (an older platform, or a body that failed to parse) --
// callers must treat "" the same as an unclassified 401, never as one of the
// named codes.
func PlatformAuthErrorCode(err error) string {
	var statusErr *PlatformStatusError
	if !errors.As(err, &statusErr) || statusErr.Status != http.StatusUnauthorized {
		return ""
	}
	var envelope platformAuthErrorEnvelope
	if jsonErr := json.Unmarshal(statusErr.Body, &envelope); jsonErr != nil {
		return ""
	}
	return envelope.Code
}

// platformStatusError maps a non-2xx response to a sentinel a caller can
// distinguish with errors.Is, carrying the response body (see
// PlatformStatusError) so a caller whose endpoint returns structured detail
// can decode it.
func platformStatusError(method, path string, status int, body []byte, header http.Header) error {
	return &PlatformStatusError{Method: method, Path: path, Status: status, Body: body, Header: header, sentinel: platformStatusSentinel(status)}
}

func platformStatusSentinel(status int) error {
	switch status {
	case http.StatusUnauthorized:
		return ErrPlatformUnauthorized
	case http.StatusForbidden:
		return ErrPlatformForbidden
	case http.StatusNotFound:
		return ErrPlatformNotFound
	case http.StatusConflict:
		return ErrPlatformConflict
	case http.StatusNotImplemented:
		return ErrPlatformNotImplemented
	case http.StatusTooManyRequests:
		return ErrPlatformRateLimited
	default:
		return nil
	}
}
