package cloud

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

// defaultResolveHostAddrs is the real DNS lookup behind CloudDependencies'
// ResolveHostAddrs. It ignores ctx.DryRun deliberately:
// ResolveControlPlaneVersionDrift returns before probing any plane under
// --dry-run, so there is no dry-run contract for it to honor here.
func defaultResolveHostAddrs(_ core.Context, host string) ([]string, error) {
	return net.DefaultResolver.LookupHost(context.Background(), host)
}

// VersionSurfaceUnexpectedContentError reports that a surface answered GET
// /version.json but did not serve the expected JSON document -- most
// commonly an SPA's index.html, served by a wildcard nginx fallback for a
// route the deployed config doesn't yet exact-match. It is deliberately a
// distinct type from a transport error: the surface did answer, so a
// caller must never treat this as unreachable.
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

// defaultFetchVersionJSON resolves a deployed surface's own build version via
// its unauthenticated GET /version.json -- the console's and the docs site's
// counterpart to defaultFetchPlatformInfo's GET /v1/platform. It makes the
// request directly rather than through fetchJSON so it can tell a transport
// failure (connection refused, DNS, TLS, timeout -- the request never got an
// answer) apart from a content failure (an answer arrived, but not the
// expected JSON document): the two point an operator at completely different
// first moves, and collapsing them into one "unreachable" verdict misdirects.
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
		// Returned unwrapped: the caller knows which surface it asked about
		// and is the one that says so, in the same words it traces.
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
