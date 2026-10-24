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

// ProbeRoute issues a plain, authenticated GET against path and reports the
// raw HTTP status and body, treating no status as an error -- unlike every
// typed method above, which funnels through do and maps a non-2xx to a
// PlatformStatusError. A route-liveness check needs to tell "the plane
// answered, whatever it said" apart from "the request never reached the
// plane at all", and it needs that for arbitrary, often-parameterized
// paths with no typed response shape -- so this bypasses status-to-error
// mapping and response decoding entirely. It never sends a body and always
// uses GET regardless of what method the probed route is actually
// registered under, which is what keeps a liveness probe read-only.
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
		return NewPlatformStatusError(method, path, resp.StatusCode, respBody, resp.Header)
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
	if e.routeNotRegistered() {
		base += "; the deployed plane's router has no route matching this path at all (not an application-level " +
			"not-found) -- it likely predates this route; run `erun exec route-check` to confirm, or " +
			"`erun list --control-planes` to compare its deployed version against what's published"
	}
	return base
}

// routeNotRegistered reports whether this 404's body is exactly the plane's
// own unmodified "no route matched" body -- the same discriminator
// route_check.go uses to tell "this route was never registered on the
// deployed plane" apart from an application-level 404 (a well-formed request
// for an id that doesn't exist, which always carries erun-backend-api's own
// JSON error shape instead). A route can merge, get unit-tested, and close
// its issue while the deployed plane still predates it; every
// ordinary typed call funnels through do()/platformStatusError, so without
// this the operator sees only an opaque "http 404: 404 page not found" with
// nothing distinguishing a genuine not-found from a plane running old code.
func (e *PlatformStatusError) routeNotRegistered() bool {
	return e.Status == http.StatusNotFound && strings.TrimSpace(string(e.Body)) == MuxDefaultNotFoundBody
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

// NewPlatformStatusError maps a non-2xx response to a sentinel a caller can
// distinguish with errors.Is, carrying the response body (see
// PlatformStatusError) so a caller whose endpoint returns structured detail
// can decode it.
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
