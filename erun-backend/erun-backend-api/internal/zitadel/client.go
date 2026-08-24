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
// needs to display and act on.
type User struct {
	ID        string `json:"id"`
	Username  string `json:"username"`
	State     string `json:"state"`
	Email     string `json:"email"`
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
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
}

func (r userSearchResult) toUser() User {
	u := User{ID: r.ID, Username: r.UserName, State: r.State}
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
type CreateHumanUserParams struct {
	Username  string
	Email     string
	FirstName string
	LastName  string
}

// CreateHumanUser creates a new human user via Zitadel's invite flow: no
// password is set here, so Zitadel emails the enrollee a verification/
// initialization link rather than this client (or its caller) ever handling
// a credential for someone else's account.
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
	body := map[string]any{
		"userName": username,
		"profile": map[string]any{
			"firstName": firstName,
			"lastName":  lastName,
		},
		"email": map[string]any{
			"email":           email,
			"isEmailVerified": false,
		},
	}
	var resp struct {
		UserID string `json:"userId"`
	}
	if err := c.call(ctx, http.MethodPost, "/management/v1/users/human", body, &resp); err != nil {
		return User{}, err
	}
	return User{
		ID:        resp.UserID,
		Username:  username,
		Email:     email,
		FirstName: firstName,
		LastName:  lastName,
		State:     "USER_STATE_INITIAL",
	}, nil
}

// DeactivateUser deactivates the IdP user, preventing their next sign-in.
func (c *Client) DeactivateUser(ctx context.Context, userID string) error {
	return c.call(ctx, http.MethodPost, fmt.Sprintf("/management/v1/users/%s/_deactivate", url.PathEscape(userID)), map[string]any{}, nil)
}

// ReactivateUser reverses DeactivateUser.
func (c *Client) ReactivateUser(ctx context.Context, userID string) error {
	return c.call(ctx, http.MethodPost, fmt.Sprintf("/management/v1/users/%s/_reactivate", url.PathEscape(userID)), map[string]any{}, nil)
}
