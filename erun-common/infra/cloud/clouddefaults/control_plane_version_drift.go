package clouddefaults

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
)

// defaultResolveHostAddrs ignores DryRun: drift resolution returns before probing under --dry-run.
func defaultResolveHostAddrs(_ core.Context, host string) ([]string, error) {
	return net.DefaultResolver.LookupHost(context.Background(), host)
}

// VersionSurfaceUnexpectedContentError means the surface answered /version.json with
// non-JSON (often an SPA fallback); callers must not treat it as unreachable.
type VersionSurfaceUnexpectedContentError struct {
	statusCode  int
	contentType string
}

func (e *VersionSurfaceUnexpectedContentError) Error() string {
	contentType := strings.TrimSpace(e.contentType)
	if contentType == "" {
		contentType = "(no content-type)"
	}
	return fmt.Sprintf("/version.json returned %d %s (expected application/json)", e.statusCode, contentType)
}

// defaultFetchVersionJSON reads a surface's GET /version.json, keeping transport
// failures distinct from unexpected content.
func defaultFetchVersionJSON(ctx core.Context, surfaceURL string) (string, error) {
	target := strings.TrimRight(strings.TrimSpace(surfaceURL), "/") + "/version.json"
	ctx.Trace("GET " + target)
	if ctx.DryRun {
		return "", nil
	}
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := (&http.Client{Timeout: erunHTTPTimeout}).Do(req)
	if err != nil {
		// Unwrapped: the caller names the surface.
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	var body struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(respBody, &body); err != nil {
		return "", &VersionSurfaceUnexpectedContentError{statusCode: resp.StatusCode, contentType: resp.Header.Get("Content-Type")}
	}
	return body.Version, nil
}
