package platformclient

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
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

// APIHost is the platform base URL this client was built against — the address
// the hosted marker records, so a later call can tell "the same platform" from
// "a same-named environment somewhere else".
func (c *PlatformClient) APIHost() string { return c.baseURL }

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
	Issuer     string `json:"issuer"`
	APIURL     string `json:"apiUrl"`
	ConsoleURL string `json:"consoleUrl"`
	// DocsURL is the documentation site this plane's own front door links to,
	// and the address the docs-site version check resolves its target from --
	// the same discovery contract ConsoleURL carries for the console. Empty on
	// a plane that advertises none (an older platform, or one whose deploy
	// carried no platform config), which reads as no docs site rather than as
	// one that is up to date.
	DocsURL         string `json:"docsUrl"`
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
	TenantID string `json:"tenantId"`
	// TenantName is the tenant's name -- the same value `platform tenant
	// list` reports for TenantID, read by the server off the identical
	// tenants.name column, so the two can never disagree about what a
	// tenant is called.
	TenantName string   `json:"tenantName,omitempty"`
	UserID     string   `json:"userId"`
	Username   string   `json:"username,omitempty"`
	Roles      []string `json:"roles,omitempty"`
	// Capabilities is nil when the platform did not answer with one, and
	// non-nil but empty when the caller may do nothing. Do not conflate them —
	// see PlatformCapabilities.Known.
	Capabilities model.PlatformCapabilities `json:"capabilities"`
	Issuer       string                     `json:"issuer"`
	Subject      string                     `json:"subject"`
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

// PlatformEnvDefinitionRecord mirrors the environment_definitions row as the
// API returns it: the stored portable subset, the revision it carries, and who
// wrote it.
type PlatformEnvDefinitionRecord struct {
	EnvironmentID string `json:"environmentId"`
	TenantID      string `json:"tenantId"`
	// Revision increases by one on every upload. It is the number a local
	// marker stamps to record what it last synced from.
	Revision   int                             `json:"revision"`
	Definition configpkg.PlatformEnvDefinition `json:"definition"`
	// WrittenByUserID is the ERun user whose token performed the upload.
	WrittenByUserID string    `json:"writtenByUserId,omitempty"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

// PutEnvironmentDefinition stores definition as the environment's current one,
// advancing its revision by one — or writing revision 1 for an environment that
// has none yet. It is what an upload calls.
func (c *PlatformClient) PutEnvironmentDefinition(ctx context.Context, environmentID string, definition configpkg.PlatformEnvDefinition) (PlatformEnvDefinitionRecord, error) {
	body := struct {
		Definition configpkg.PlatformEnvDefinition `json:"definition"`
	}{Definition: definition}
	var record PlatformEnvDefinitionRecord
	path := "/v1/environments/" + url.PathEscape(environmentID) + "/definition"
	err := c.do(ctx, http.MethodPut, path, body, true, &record)
	return record, err
}

// GetEnvironmentDefinition reads the environment's stored definition and the
// revision it carries. A platform that holds none answers ErrPlatformNotFound,
// which a caller must render as "nothing has been uploaded" rather than as an
// empty definition: those are different states, and only one is resolved by
// uploading.
func (c *PlatformClient) GetEnvironmentDefinition(ctx context.Context, environmentID string) (PlatformEnvDefinitionRecord, error) {
	var record PlatformEnvDefinitionRecord
	path := "/v1/environments/" + url.PathEscape(environmentID) + "/definition"
	err := c.do(ctx, http.MethodGet, path, nil, true, &record)
	return record, err
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
