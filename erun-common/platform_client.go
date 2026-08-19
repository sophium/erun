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
// returns a plain-text body (http.Error), never a JSON error envelope, so
// these carry the response body as their message rather than parsing one.
var (
	ErrPlatformUnauthorized   = errors.New("platform api: unauthorized")
	ErrPlatformForbidden      = errors.New("platform api: forbidden")
	ErrPlatformNotFound       = errors.New("platform api: not found")
	ErrPlatformConflict       = errors.New("platform api: conflict")
	ErrPlatformNotImplemented = errors.New("platform api: not implemented")
)

// PlatformClient talks to one erun-backend-api instance.
type PlatformClient struct {
	baseURL string
	mint    PlatformTokenMinter
	http    *http.Client
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
}

// PlatformWhoami mirrors GET /v1/whoami's response.
type PlatformWhoami struct {
	TenantID string   `json:"tenantId"`
	UserID   string   `json:"userId"`
	Username string   `json:"username,omitempty"`
	Roles    []string `json:"roles,omitempty"`
	Issuer   string   `json:"issuer"`
	Subject  string   `json:"subject"`
}

// PlatformTenant mirrors model.Tenant's JSON shape.
type PlatformTenant struct {
	TenantID  string    `json:"tenantId"`
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// PlatformUser mirrors model.User's JSON shape.
type PlatformUser struct {
	UserID    string    `json:"userId"`
	TenantID  string    `json:"tenantId"`
	Username  string    `json:"username"`
	Issuer    string    `json:"issuer,omitempty"`
	Subject   string    `json:"subject,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// PlatformEnvironment mirrors model.Environment's JSON shape.
type PlatformEnvironment struct {
	EnvironmentID     string    `json:"environmentId"`
	TenantID          string    `json:"tenantId"`
	Name              string    `json:"name"`
	Type              string    `json:"type"`
	KubernetesContext string    `json:"kubernetesContext,omitempty"`
	ContextID         string    `json:"contextId,omitempty"`
	RuntimeVersion    string    `json:"runtimeVersion,omitempty"`
	Status            string    `json:"status"`
	ProvisionError    string    `json:"provisionError,omitempty"`
	DeployedVersion   string    `json:"deployedVersion,omitempty"`
	CreatedAt         time.Time `json:"createdAt"`
	UpdatedAt         time.Time `json:"updatedAt"`
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

// PlatformCreateUserParams is the user-enrollment input. TenantID, when set,
// targets a tenant other than the caller's own and is honored only for an
// operations-scoped caller.
type PlatformCreateUserParams struct {
	Username string `json:"username"`
	Issuer   string `json:"issuer,omitempty"`
	Subject  string `json:"subject,omitempty"`
	TenantID string `json:"tenantId,omitempty"`
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
}

// CreateEnvironment registers an environment. When it is a runtime env with a
// pinned RuntimeVersion and the platform has a deploy executor configured,
// this also starts the server-side deploy (the response's Status moves
// registered -> provisioning -> running/failed; poll GetEnvironment).
func (c *PlatformClient) CreateEnvironment(ctx context.Context, params PlatformCreateEnvironmentParams) (PlatformEnvironment, error) {
	var environment PlatformEnvironment
	err := c.do(ctx, http.MethodPost, "/v1/environments", params, true, &environment)
	return environment, err
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
		return platformStatusError(method, path, resp.StatusCode, respBody)
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

// platformStatusError maps a non-2xx response to a sentinel a caller can
// distinguish with errors.Is, carrying the server's plain-text body (see
// writeError/http.Error in erun-backend-api) as detail.
func platformStatusError(method, path string, status int, body []byte) error {
	detail := strings.TrimSpace(string(body))
	base := fmt.Sprintf("platform api %s %s: http %d", method, path, status)
	if detail != "" {
		base += ": " + detail
	}
	switch status {
	case http.StatusUnauthorized:
		return fmt.Errorf("%s: %w", base, ErrPlatformUnauthorized)
	case http.StatusForbidden:
		return fmt.Errorf("%s: %w", base, ErrPlatformForbidden)
	case http.StatusNotFound:
		return fmt.Errorf("%s: %w", base, ErrPlatformNotFound)
	case http.StatusConflict:
		return fmt.Errorf("%s: %w", base, ErrPlatformConflict)
	case http.StatusNotImplemented:
		return fmt.Errorf("%s: %w", base, ErrPlatformNotImplemented)
	default:
		return errors.New(base)
	}
}
