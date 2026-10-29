// Package zitadel drives the identity-administration slice of Zitadel's
// Management API with the org-owner PAT, held only in memory and never logged.
// Every operation is named here, never proxied generically: that enumerated
// surface is the compensating control for the PAT's org-owner breadth.
package zitadel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	baseURL        string
	externalDomain string
	pat            string
	httpClient     *http.Client
}

// Config holds only the PAT's path, so printing a Config cannot leak it.
type Config struct {
	BaseURL string
	// ExternalDomain is sent as the Host header; Zitadel 404s every call without it.
	ExternalDomain string
	PATPath        string
}

// NewClientFromFile returns (nil, nil) when cfg is incomplete, leaving the feature
// disabled; a set but unreadable or empty PATPath is an error.
func NewClientFromFile(cfg Config) (*Client, error) {
	baseURL := strings.TrimSpace(cfg.BaseURL)
	externalDomain := strings.TrimSpace(cfg.ExternalDomain)
	patPath := strings.TrimSpace(cfg.PATPath)
	if baseURL == "" || externalDomain == "" || patPath == "" {
		return nil, nil //nolint:nilnil // an incomplete config leaves the feature disabled
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

// APIError is a non-2xx response; Body never includes request headers.
type APIError struct {
	Method     string
	Path       string
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("zitadel management api %s %s: status %d: %s", e.Method, e.Path, e.StatusCode, e.Body)
}

func (e *APIError) NotFound() bool {
	return e.StatusCode == http.StatusNotFound
}

// ErrUsernameTaken is the sentinel behind *UsernameTakenError.
var ErrUsernameTaken = errors.New("username is already taken")

// UsernameTakenError reports a login name the instance already holds.
type UsernameTakenError struct {
	// Username is as the caller supplied it; under a domain-qualifying Domain Policy
	// the instance holds it suffixed with the org's domain.
	Username string
}

func (e *UsernameTakenError) Error() string {
	return fmt.Sprintf("username %q is already taken; choose a different login name", e.Username)
}

func (e *UsernameTakenError) Unwrap() error { return ErrUsernameTaken }

const zitadelUserAlreadyExistsKey = "Errors.User.AlreadyExists"

// usernameConflict keys on the message key, not the status: a conflict can also
// be a colliding email, which is not a username problem.
func usernameConflict(username string, err error) error {
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusConflict {
		return err
	}
	if !strings.Contains(apiErr.Body, zitadelUserAlreadyExistsKey) {
		return err
	}
	return &UsernameTakenError{Username: username}
}

const errorBodyTruncateLimit = 500

// callInOrg addresses orgID via the x-zitadel-orgid header instead of the
// credential's own org; an empty orgID is exactly call.
func (c *Client) callInOrg(ctx context.Context, orgID string, method string, path string, body any, out any) error {
	return c.callWithHeaders(ctx, method, path, body, out, orgHeaders(orgID))
}

func orgHeaders(orgID string) map[string]string {
	orgID = strings.TrimSpace(orgID)
	if orgID == "" {
		return nil
	}
	return map[string]string{"x-zitadel-orgid": orgID}
}

func (c *Client) call(ctx context.Context, method string, path string, body any, out any) error {
	return c.callWithHeaders(ctx, method, path, body, out, nil)
}

func (c *Client) callWithHeaders(ctx context.Context, method string, path string, body any, out any, headers map[string]string) error {
	req, err := c.newRequest(ctx, method, path, body, headers)
	if err != nil {
		return err
	}
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

func (c *Client) newRequest(ctx context.Context, method string, path string, body any, headers map[string]string) (*http.Request, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode zitadel request %s %s: %w", method, path, err)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return nil, fmt.Errorf("build zitadel request %s %s: %w", method, path, err)
	}
	// Zitadel resolves the instance from the Host header; net/http sends req.Host,
	// not a Host set in the header map.
	req.Host = c.externalDomain
	req.Header.Set("Authorization", "Bearer "+c.pat)
	req.Header.Set("Content-Type", "application/json")
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	return req, nil
}

func truncate(body []byte, limit int) string {
	if len(body) <= limit {
		return string(body)
	}
	return string(body[:limit]) + "..."
}

// User is a Zitadel IdP identity. IsMachine is read from the "machine" object
// rather than inferred from "human" being absent.
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
	Result  []userSearchResult `json:"result"`
	Details listDetails        `json:"details"`
}

// listDetails carries a v1 search's totalResult, which the gateway renders as a
// JSON string; absent means the server did not report one.
type listDetails struct {
	TotalResult *listCount `json:"totalResult"`
}

type listCount uint64

func (c *listCount) UnmarshalJSON(data []byte) error {
	text := strings.Trim(string(data), `"`)
	if text == "" || text == "null" {
		*c = 0
		return nil
	}
	value, err := strconv.ParseUint(text, 10, 64)
	if err != nil {
		return fmt.Errorf("zitadel list totalResult %s: %w", data, err)
	}
	*c = listCount(value)
	return nil
}

// nameEqualsQuery narrows a v1 project or app search to one exact name, so the
// match is found however many siblings the server's row cap would hide.
func nameEqualsQuery(name string) map[string]any {
	return map[string]any{"queries": []map[string]any{{
		"nameQuery": map[string]any{"name": name, "method": "TEXT_QUERY_METHOD_EQUALS"},
	}}}
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

// ListUsers lists orgID's users, newest first, or the PAT's own org's when orgID
// is empty. A call without the org header silently sees only its own org.
// Zitadel caps an unpaged v1 search at its MaxQueryLimit (1000 by default) from
// v4.19.5, so it pages by offset alone, each page whatever size that server
// allows. Pages run oldest first so a user created mid-walk lands past the
// offsets already read, and the result is reversed into the unpaged order.
func (c *Client) ListUsers(ctx context.Context, orgID string) ([]User, error) {
	users := []User{}
	for {
		var resp usersSearchResponse
		body := map[string]any{"query": map[string]any{"offset": strconv.Itoa(len(users)), "asc": true}}
		if err := c.callInOrg(ctx, orgID, http.MethodPost, "/management/v1/users/_search", body, &resp); err != nil {
			return nil, err
		}
		for _, result := range resp.Result {
			users = append(users, result.toUser())
		}
		total := resp.Details.TotalResult
		if len(resp.Result) == 0 || total == nil || uint64(len(users)) >= uint64(*total) {
			slices.Reverse(users)
			return users, nil
		}
	}
}

// CreateHumanUserParams is the enrollment input. An empty InitialPassword uses
// Zitadel's invite email; when set, the email is also marked verified, since
// Zitadel otherwise leaves the user in USER_STATE_INITIAL awaiting a link.
type CreateHumanUserParams struct {
	Username        string
	Email           string
	FirstName       string
	LastName        string
	InitialPassword string
	OrgID           string
}

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
	if err := c.callInOrg(ctx, params.OrgID, http.MethodPost, "/management/v1/users/human", body, &resp); err != nil {
		return User{}, usernameConflict(username, err)
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

// Org is a Zitadel organization; its ID is what an org-scoped issuer's
// org_field_value holds.
type Org struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// CreateOrg creates a Zitadel organization. The credential stays scoped to the
// platform's org; the new org's first user becomes its admin via bootstrap.
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

// DeactivateUser deactivates the user in orgID (empty means the client's own).
// User ids are org-scoped, so a wrong org 404s.
func (c *Client) DeactivateUser(ctx context.Context, orgID string, userID string) error {
	return c.callInOrg(ctx, orgID, http.MethodPost, fmt.Sprintf("/management/v1/users/%s/_deactivate", url.PathEscape(userID)), map[string]any{}, nil)
}

// ReactivateUser reverses DeactivateUser.
func (c *Client) ReactivateUser(ctx context.Context, orgID string, userID string) error {
	return c.callInOrg(ctx, orgID, http.MethodPost, fmt.Sprintf("/management/v1/users/%s/_reactivate", url.PathEscape(userID)), map[string]any{}, nil)
}
