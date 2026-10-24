package platformclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ProbeRoute always GETs (staying read-only) and returns the raw status unmapped,
// so a liveness check can tell "answered" from "unreachable".
func (c *PlatformClient) ProbeRoute(ctx context.Context, path string) (status int, body []byte, err error) {
	req, err := c.buildRequest(ctx, http.MethodGet, path, nil, true)
	if err != nil {
		return 0, nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("call platform api GET %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, respBody, nil
}

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
		return NewPlatformStatusError(method, path, resp.StatusCode, respBody, resp.Header)
	}
	return decodePlatformResponse(method, path, respBody, out)
}

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

// PlatformStatusError is every non-2xx response; it unwraps to the mapped
// sentinel and keeps Body for endpoints with structured refusals.
type PlatformStatusError struct {
	Method   string
	Path     string
	Status   int
	Body     []byte
	Header   http.Header
	sentinel error
}

// RetryAfter parses the Retry-After header in seconds, the only form the limiter sends.
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
	if e.routeNotRegistered() {
		base += "; the deployed plane's router has no route matching this path at all (not an application-level " +
			"not-found) -- it likely predates this route; run `erun exec route-check` to confirm, or " +
			"`erun list --control-planes` to compare its deployed version against what's published"
	}
	return base
}

// routeNotRegistered reports a 404 carrying the router's own default body, meaning
// the deployed plane predates the route rather than an id not being found.
func (e *PlatformStatusError) routeNotRegistered() bool {
	return e.Status == http.StatusNotFound && strings.TrimSpace(string(e.Body)) == MuxDefaultNotFoundBody
}

func (e *PlatformStatusError) Unwrap() error {
	return e.sentinel
}

// platformAuthErrorEnvelope mirrors the backend's 401 {code, message} envelope.
type platformAuthErrorEnvelope struct {
	Code string `json:"code"`
}

// PlatformAuthErrorCode returns a 401's code, or "" when there is none; callers
// must treat "" as an unclassified 401.
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

// NewPlatformStatusError maps a non-2xx response to a PlatformStatusError.
func NewPlatformStatusError(method, path string, status int, body []byte, header http.Header) error {
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
