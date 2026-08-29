// Package zitadel is the first consumer of the org-owner PAT the erun-zitadel
// chart already provisions and durably persists (see the chart's
// oidc-bootstrap sidecar, which uses the same PAT for OIDC app bootstrap).
// This client drives the identity-administration slice of Zitadel's
// Management API: listing/enrolling/deactivating/reactivating IdP users and
// reading/updating org-level login and password policy.
//
// Org-owner is the highest-privilege credential the platform's IdP holds.
// Zitadel's built-in roles do offer a narrower ORG_USER_MANAGER role that
// would fit the user-CRUD half of this surface, but org policy management
// (login policy, password complexity) has no built-in role short of
// ORG_OWNER, and minting a second machine user for half the surface only
// shrinks the blast radius for user CRUD while adding a second
// bootstrap-managed credential. The compensating control instead is an
// enumerated, non-proxying endpoint surface (see internal/routes/identity.go)
// — every operation this client can perform is named here, not forwarded
// generically.
//
// The PAT is loaded once from a mounted file and held only in memory. It
// never appears in a struct field logged elsewhere, a command-line
// argument, or error text — API errors carry the response body, never the
// request headers.
package zitadel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Client drives Zitadel's Management API over HTTP.
type Client struct {
	baseURL        string
	externalDomain string
	pat            string
	httpClient     *http.Client
}

// Config configures a Client. The PAT itself is never a field here — only
// the path to read it from — so it cannot end up in a log line that prints a
// Config by mistake.
type Config struct {
	// BaseURL is the Zitadel core Management API base, typically the
	// cluster-internal Service address (e.g. http://<tenant>-zitadel:8080).
	BaseURL string
	// ExternalDomain is the externally reachable host Zitadel resolves the
	// instance from: every request carries it as the outgoing Host, because a
	// call with none 404s on every path (see the erun-zitadel chart header).
	ExternalDomain string
	// PATPath is the filesystem path to the mounted org-owner PAT.
	PATPath string
}

// NewClientFromFile loads the PAT from cfg.PATPath and returns a configured
// Client. It returns (nil, nil) when cfg is incomplete, matching this
// codebase's optional-dependency convention (server.go): a feature stays
// disabled rather than the process failing to start. A PATPath that is set
// but unreadable or empty is a hard misconfiguration and returns an error.
func NewClientFromFile(cfg Config) (*Client, error) {
	baseURL := strings.TrimSpace(cfg.BaseURL)
	externalDomain := strings.TrimSpace(cfg.ExternalDomain)
	patPath := strings.TrimSpace(cfg.PATPath)
	if baseURL == "" || externalDomain == "" || patPath == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(patPath)
	if err != nil {
		return nil, fmt.Errorf("read zitadel management pat: %w", err)
	}
	pat := strings.TrimSpace(string(raw))
	if pat == "" {
		return nil, fmt.Errorf("zitadel management pat file %s is empty", patPath)
	}
	return newClient(baseURL, externalDomain, pat), nil
}

func newClient(baseURL string, externalDomain string, pat string) *Client {
	return &Client{
		baseURL:        strings.TrimRight(baseURL, "/"),
		externalDomain: externalDomain,
		pat:            pat,
		httpClient:     &http.Client{Timeout: 15 * time.Second},
	}
}

// APIError is a non-2xx response from the Management API. Body is the
// response payload, truncated; the request's own Authorization header is
// never included.
type APIError struct {
	Method     string
	Path       string
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("zitadel management api %s %s: status %d: %s", e.Method, e.Path, e.StatusCode, e.Body)
}

// NotFound reports whether the error is Zitadel's 404 for the resource
// addressed by the request.
func (e *APIError) NotFound() bool {
	return e.StatusCode == http.StatusNotFound
}

const errorBodyTruncateLimit = 500

func (c *Client) call(ctx context.Context, method string, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode zitadel request %s %s: %w", method, path, err)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("build zitadel request %s %s: %w", method, path, err)
	}
	// Zitadel resolves which instance a Management API call targets from the
	// Host header, not from the address the call is actually addressed to
	// (see erun-zitadel chart header and oidc-bootstrap.sh); req.Host is what
	// net/http actually sends as the wire Host, a plain header set is ignored.
	req.Host = c.externalDomain
	req.Header.Set("Authorization", "Bearer "+c.pat)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("call zitadel management api %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read zitadel response %s %s: %w", method, path, err)
	}
	if resp.StatusCode >= 300 {
		return &APIError{Method: method, Path: path, StatusCode: resp.StatusCode, Body: truncate(respBody, errorBodyTruncateLimit)}
	}
	if out == nil || len(respBody) == 0 {
		return nil
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("decode zitadel response %s %s: %w", method, path, err)
	}
	return nil
}

func truncate(body []byte, limit int) string {
	if len(body) <= limit {
		return string(body)
	}
	return string(body[:limit]) + "..."
}

// User is a Zitadel IdP identity, as much of it as identity administration
// needs to display and act on. IsMachine distinguishes the org's own
// service identities (e.g. login-client, admin-sa) from human accounts —
// Zitadel's _search response carries a "machine" object in exactly the
// cases "human" is absent, so the two are read as a pair rather than
// inferring one from the other's absence.
type User struct {
	ID        string `json:"id"`
	Username  string `json:"username"`
	State     string `json:"state"`
	Email     string `json:"email"`
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
	IsMachine bool   `json:"isMachine"`
}

type usersSearchResponse struct {
	Result []userSearchResult `json:"result"`
}

type userSearchResult struct {
	ID       string `json:"id"`
	UserName string `json:"userName"`
	State    string `json:"state"`
	Human    *struct {
		Profile *struct {
			FirstName string `json:"firstName"`
			LastName  string `json:"lastName"`
		} `json:"profile"`
		Email *struct {
			Email string `json:"email"`
		} `json:"email"`
	} `json:"human"`
	Machine *struct {
		Name string `json:"name"`
	} `json:"machine"`
}

func (r userSearchResult) toUser() User {
	u := User{ID: r.ID, Username: r.UserName, State: r.State, IsMachine: r.Machine != nil}
	if r.Human == nil {
		return u
	}
	if r.Human.Profile != nil {
		u.FirstName = r.Human.Profile.FirstName
		u.LastName = r.Human.Profile.LastName
	}
	if r.Human.Email != nil {
		u.Email = r.Human.Email.Email
	}
	return u
}

// ListUsers lists every human and machine user of the org the client's PAT
// belongs to.
func (c *Client) ListUsers(ctx context.Context) ([]User, error) {
	var resp usersSearchResponse
	if err := c.call(ctx, http.MethodPost, "/management/v1/users/_search", map[string]any{}, &resp); err != nil {
		return nil, err
	}
	users := make([]User, 0, len(resp.Result))
	for _, result := range resp.Result {
		users = append(users, result.toUser())
	}
	return users, nil
}

// CreateHumanUserParams is the enrollment input for a new IdP identity.
// InitialPassword is empty for Zitadel's normal invite flow: no password is
// set, so Zitadel emails the enrollee a verification/initialization link
// rather than this client (or its caller) ever handling a credential for
// someone else's account. When the platform cannot send that email at all
// (issue #1168), the caller sets InitialPassword instead -- verified live
// against a real Zitadel v4.15.3 instance that AddHumanUser only sends the
// initialization email "if either the email address is not marked as
// verified or no password is set" (its own API doc comment), so a set
// password alone still leaves the account in USER_STATE_INITIAL waiting on
// a link that will never arrive; CreateHumanUser accordingly marks the email
// verified in that case too, and only in that case, which is what actually
// lands the account in USER_STATE_ACTIVE with the password usable
// immediately.
type CreateHumanUserParams struct {
	Username        string
	Email           string
	FirstName       string
	LastName        string
	InitialPassword string
}

// CreateHumanUser creates a new human user in Zitadel. See
// CreateHumanUserParams.InitialPassword for the two distinct outcomes this
// produces.
func (c *Client) CreateHumanUser(ctx context.Context, params CreateHumanUserParams) (User, error) {
	username := strings.TrimSpace(params.Username)
	email := strings.TrimSpace(params.Email)
	if username == "" || email == "" {
		return User{}, fmt.Errorf("username and email are required to create a zitadel user")
	}
	firstName := strings.TrimSpace(params.FirstName)
	if firstName == "" {
		firstName = username
	}
	lastName := strings.TrimSpace(params.LastName)
	if lastName == "" {
		lastName = username
	}
	hasInitialPassword := params.InitialPassword != ""
	body := map[string]any{
		"userName": username,
		"profile": map[string]any{
			"firstName": firstName,
			"lastName":  lastName,
		},
		"email": map[string]any{
			"email":           email,
			"isEmailVerified": hasInitialPassword,
		},
	}
	if hasInitialPassword {
		body["initialPassword"] = params.InitialPassword
	}
	var resp struct {
		UserID string `json:"userId"`
	}
	if err := c.call(ctx, http.MethodPost, "/management/v1/users/human", body, &resp); err != nil {
		return User{}, err
	}
	state := "USER_STATE_INITIAL"
	if hasInitialPassword {
		state = "USER_STATE_ACTIVE"
	}
	return User{
		ID:        resp.UserID,
		Username:  username,
		Email:     email,
		FirstName: firstName,
		LastName:  lastName,
		State:     state,
	}, nil
}

// Org is a Zitadel organization. Its ID is what an org-scoped issuer's
// org_field_value holds, and what the urn:zitadel:iam:user:resourceowner:id
// claim carries for a user who belongs to it.
type Org struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// CreateOrg creates a Zitadel organization, the per-tenant identity boundary
// an org-scoped issuer resolves tenants by. Without it, onboarding a second
// tenant onto the platform's own IdP needed a hand-made org in Zitadel's
// console: erun could register the tenant and the issuer mapping, but had no
// way to create the org the mapping points at (issue #1605).
//
// The org is created but deliberately not made the client's own: this
// credential stays scoped to the platform's org, and the new org's first
// erun caller becomes its admin through the per-tenant first-user bootstrap.
func (c *Client) CreateOrg(ctx context.Context, name string) (Org, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Org{}, fmt.Errorf("org name is required to create a zitadel org")
	}
	var resp struct {
		ID string `json:"id"`
	}
	if err := c.call(ctx, http.MethodPost, "/management/v1/orgs", map[string]any{"name": name}, &resp); err != nil {
		return Org{}, err
	}
	return Org{ID: resp.ID, Name: name}, nil
}

// DeactivateUser deactivates the IdP user, preventing their next sign-in.
func (c *Client) DeactivateUser(ctx context.Context, userID string) error {
	return c.call(ctx, http.MethodPost, fmt.Sprintf("/management/v1/users/%s/_deactivate", url.PathEscape(userID)), map[string]any{}, nil)
}

// ReactivateUser reverses DeactivateUser.
func (c *Client) ReactivateUser(ctx context.Context, userID string) error {
	return c.call(ctx, http.MethodPost, fmt.Sprintf("/management/v1/users/%s/_reactivate", url.PathEscape(userID)), map[string]any{}, nil)
}
