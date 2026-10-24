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

// PlatformClient is free of Cobra and the MCP SDK. Each request mints its bearer
// immediately before sending so a slow call cannot outlive its token.

// PlatformTokenMinter returns a fresh bearer token.
type PlatformTokenMinter func() (string, error)

// Sentinel errors carry the plain-text response body; structured refusals wrap
// them in typed errors such as PlatformMergeQueueBlockedError.
var (
	ErrPlatformUnauthorized   = errors.New("platform api: unauthorized")
	ErrPlatformForbidden      = errors.New("platform api: forbidden")
	ErrPlatformNotFound       = errors.New("platform api: not found")
	ErrPlatformConflict       = errors.New("platform api: conflict")
	ErrPlatformNotImplemented = errors.New("platform api: not implemented")
	ErrPlatformRateLimited    = errors.New("platform api: rate limited")
)

// MCPToolAuditHeader names the calling MCP tool so the backend audits the call as MCP.
// erun-backend-api reads this same constant.
const MCPToolAuditHeader = "X-Erun-Mcp-Tool"

// PlatformClient talks to one erun-backend-api instance.
type PlatformClient struct {
	baseURL      string
	mint         PlatformTokenMinter
	usernameHint string
	mcpTool      string
	http         *http.Client
}

// NewPlatformClient builds a client against baseURL. mint may be nil for PlatformInfo.
func NewPlatformClient(baseURL string, mint PlatformTokenMinter) *PlatformClient {
	return &PlatformClient{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		mint:    mint,
		// No timeout: the caller's context owns the deadline.
		http: &http.Client{},
	}
}

// APIHost is the base URL, recorded by the hosted marker to identify the platform.
func (c *PlatformClient) APIHost() string { return c.baseURL }

// WithUsernameHint returns a copy sending the X-ERun-Username hint, which names
// users whose token has no username claim (e.g. AWS STS).
func (c *PlatformClient) WithUsernameHint(username string) *PlatformClient {
	hinted := *c
	hinted.usernameHint = strings.TrimSpace(username)
	return &hinted
}

// WithMCPTool returns a copy that sends tool as the MCPToolAuditHeader.
func (c *PlatformClient) WithMCPTool(tool string) *PlatformClient {
	tagged := *c
	tagged.mcpTool = strings.TrimSpace(tool)
	return &tagged
}

// PlatformInfo mirrors GET /v1/platform; duplicated because the backend's type is internal.
type PlatformInfo struct {
	Issuer     string `json:"issuer"`
	APIURL     string `json:"apiUrl"`
	ConsoleURL string `json:"consoleUrl"`
	// DocsURL is empty when the plane advertises no docs site.
	DocsURL         string `json:"docsUrl"`
	ConsoleClientID string `json:"consoleClientId"`
	CLIClientID     string `json:"cliClientId"`
	Brand           string `json:"brand"`
	// Version is "dev" for an unstamped build.
	Version string `json:"version"`
}

// PlatformWhoami mirrors GET /v1/whoami. Clients gate on Capabilities; Roles is descriptive.
type PlatformWhoami struct {
	TenantID   string   `json:"tenantId"`
	TenantName string   `json:"tenantName,omitempty"`
	UserID     string   `json:"userId"`
	Username   string   `json:"username,omitempty"`
	Roles      []string `json:"roles,omitempty"`
	// Capabilities is nil when unknown and empty when the caller may do nothing.
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
	// Resolvable is set only by the operations-scoped listing; nil means not asked.
	Resolvable *bool `json:"resolvable,omitempty"`
}

// PlatformUser mirrors model.User's JSON shape.
type PlatformUser struct {
	UserID   string `json:"userId"`
	TenantID string `json:"tenantId"`
	Username string `json:"username"`
	Issuer   string `json:"issuer,omitempty"`
	Subject  string `json:"subject,omitempty"`
	// AlreadyEnrolled reports that POST /v1/users found the identity already enrolled.
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
	// DeleteError holds the namespace's conditions when Status is "deletion-blocked".
	DeleteError string    `json:"deleteError,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// PlatformEnvDefinitionRecord mirrors an environment_definitions row.
type PlatformEnvDefinitionRecord struct {
	EnvironmentID string `json:"environmentId"`
	TenantID      string `json:"tenantId"`
	// Revision increases by one per upload; local markers record it as last synced.
	Revision        int                             `json:"revision"`
	Definition      configpkg.PlatformEnvDefinition `json:"definition"`
	WrittenByUserID string                          `json:"writtenByUserId,omitempty"`
	CreatedAt       time.Time                       `json:"createdAt"`
	UpdatedAt       time.Time                       `json:"updatedAt"`
}

// PutEnvironmentDefinition stores definition and advances its revision.
func (c *PlatformClient) PutEnvironmentDefinition(ctx context.Context, environmentID string, definition configpkg.PlatformEnvDefinition) (PlatformEnvDefinitionRecord, error) {
	body := struct {
		Definition configpkg.PlatformEnvDefinition `json:"definition"`
	}{Definition: definition}
	var record PlatformEnvDefinitionRecord
	path := "/v1/environments/" + url.PathEscape(environmentID) + "/definition"
	err := c.do(ctx, http.MethodPut, path, body, true, &record)
	return record, err
}

// GetEnvironmentDefinition returns ErrPlatformNotFound when nothing was uploaded,
// which callers must not render as an empty definition.
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

// PlatformConfigResponse mirrors GET /v1/config, the console read model.
type PlatformConfigResponse struct {
	Tenant       PlatformTenant        `json:"tenant"`
	Environments []PlatformEnvironment `json:"environments"`
	Contexts     []PlatformContext     `json:"contexts"`
	// InviteRequestRateLimitWindowSeconds is the per-identity invite-request window.
	InviteRequestRateLimitWindowSeconds int `json:"inviteRequestRateLimitWindowSeconds"`
}

// Invite-request kinds and statuses mirror the backend's model.
const (
	PlatformInviteRequestKindJoinTenant   = "JOIN_TENANT"
	PlatformInviteRequestKindCreateTenant = "CREATE_TENANT"

	PlatformInviteRequestStatusPending  = "PENDING"
	PlatformInviteRequestStatusApproved = "APPROVED"
	PlatformInviteRequestStatusDeclined = "DECLINED"
)

// PlatformInviteRequest mirrors model.InviteRequest's JSON shape.
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
	// The Minted fields are set once Status is APPROVED.
	MintedInviteID        string     `json:"mintedInviteId,omitempty"`
	MintedInviteToken     string     `json:"mintedInviteToken,omitempty"`
	MintedInviteExpiresAt *time.Time `json:"mintedInviteExpiresAt,omitempty"`
	CreatedAt             time.Time  `json:"createdAt"`
	UpdatedAt             time.Time  `json:"updatedAt"`
}

// Platform fetches the instance's discovery config without a token.
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

// Config returns the console read model.
func (c *PlatformClient) Config(ctx context.Context) (PlatformConfigResponse, error) {
	var config PlatformConfigResponse
	err := c.do(ctx, http.MethodGet, "/v1/config", nil, true, &config)
	return config, err
}
