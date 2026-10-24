package expose

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/mcpauth"
	"github.com/sophium/erun/erun-common/infra/platformclient"
)

// RouteCheckParams is `erun exec route-check`'s input.
type RouteCheckParams struct {
	// RoutesDir is erun-backend-api/internal/routes. Empty resolves it from
	// the current checkout's project root.
	RoutesDir string
}

// Route probe outcomes.
const (
	// RouteProbeReachable means the plane answered as though the route is
	// registered: any status other than the mux's own bare 404.
	RouteProbeReachable = "REACHABLE"
	// RouteProbeMissing means the plane answered with its own literal
	// "404 page not found" -- no pattern in its router matches this path.
	RouteProbeMissing = "MISSING"
	// RouteProbeError means the probe itself could not complete (a
	// transport-level failure), so this route's reachability is unknown.
	RouteProbeError = "ERROR"
)

// RouteProbeResult is one route's probe outcome.
type RouteProbeResult struct {
	Method     string `json:"method"`
	Path       string `json:"path"`
	ProbePath  string `json:"probePath"`
	StatusCode int    `json:"statusCode,omitempty"`
	Status     string `json:"status"`
	Detail     string `json:"detail,omitempty"`
}

// RouteCheckResult is the full route-liveness report.
type RouteCheckResult struct {
	APIURL string `json:"apiUrl"`
	// PlaneReachable reports whether the known-good sanity probe (GET
	// /v1/whoami) answered 200 before any route in the inventory was
	// probed. When false, Routes is empty -- reporting every route as
	// MISSING against a plane that never answered would misreport a down
	// or misconfigured plane as an absent-route regression.
	PlaneReachable bool `json:"planeReachable"`
	// UnreachableReason explains a false PlaneReachable.
	UnreachableReason string             `json:"unreachableReason,omitempty"`
	RoutesChecked     int                `json:"routesChecked"`
	Routes            []RouteProbeResult `json:"routes,omitempty"`
	Missing           []RouteProbeResult `json:"missing,omitempty"`
	Errors            []RouteProbeResult `json:"errors,omitempty"`
}

func resolveRouteCheckRoutesDir(explicit string) (string, error) {
	if trimmed := strings.TrimSpace(explicit); trimmed != "" {
		return trimmed, nil
	}
	_, root, err := config.FindProjectRoot()
	if err != nil {
		return "", fmt.Errorf("resolve routes directory: %w (pass --routes-dir explicitly outside a checkout of the erun repository)", err)
	}
	return filepath.Join(root, "erun-backend", "erun-backend-api", "internal", "routes"), nil
}

// RunRouteCheck probes every route erun-backend-api's router registers
// against a deployed plane and reports which ones the plane does not
// actually serve. It returns a non-nil error only when the check itself
// could not run at all (no usable alias, the route inventory could not be
// read) -- a reachable plane that turns out to be missing routes, or an
// unreachable plane, are findings reported in the result, not errors, so a
// caller can always print the full report before deciding to fail loudly.
func RunRouteCheck(ctx core.Context, store config.CloudReadStore, alias string, params RouteCheckParams, deps cloud.CloudDependencies) (RouteCheckResult, error) {
	routesDir, err := resolveRouteCheckRoutesDir(params.RoutesDir)
	if err != nil {
		return RouteCheckResult{}, err
	}
	ctx.Trace("route-check: reading route inventory from " + routesDir)
	inventory, err := mcpauth.DiscoverAPIRoutes(routesDir)
	if err != nil {
		return RouteCheckResult{}, err
	}
	ctx.Trace(fmt.Sprintf("route-check: %d route(s) registered", len(inventory)))

	client, provider, err := cloud.NewPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return RouteCheckResult{}, err
	}

	cloud.TracePlatformCall(ctx, provider, "GET", "/v1/whoami",
		"sanity probe: the plane must answer before any route in the inventory is probed")
	if ctx.DryRun {
		traceRouteCheckInventory(ctx, provider, inventory)
		return RouteCheckResult{APIURL: provider.ERun.APIURL}, nil
	}

	sane, reason := probeKnownGoodRoute(client)
	if !sane {
		ctx.Trace("route-check: sanity probe failed: " + reason)
		return RouteCheckResult{APIURL: provider.ERun.APIURL, PlaneReachable: false, UnreachableReason: reason}, nil
	}

	sortRoutes(inventory)
	result := probeRouteInventory(ctx, provider, client, inventory)
	result.APIURL = provider.ERun.APIURL
	result.PlaneReachable = true
	return result, nil
}

func sortRoutes(routes []mcpauth.APIRoute) {
	sort.Slice(routes, func(i, j int) bool {
		if routes[i].Path != routes[j].Path {
			return routes[i].Path < routes[j].Path
		}
		return routes[i].Method < routes[j].Method
	})
}

// traceRouteCheckInventory traces the GET each registered route would
// receive, satisfying the --dry-run contract without sending any request.
func traceRouteCheckInventory(ctx core.Context, provider config.CloudProviderConfig, inventory []mcpauth.APIRoute) {
	for _, route := range inventory {
		cloud.TracePlatformCall(ctx, provider, "GET", substitutePathParameters(route.Path),
			fmt.Sprintf("probing registered route %s %s", route.Method, route.Path))
	}
}

// probeRouteInventory probes every route in inventory and buckets each
// outcome, leaving APIURL/PlaneReachable for the caller to set.
func probeRouteInventory(ctx core.Context, provider config.CloudProviderConfig, client *platformclient.PlatformClient, inventory []mcpauth.APIRoute) RouteCheckResult {
	var result RouteCheckResult
	for _, route := range inventory {
		probePath := substitutePathParameters(route.Path)
		cloud.TracePlatformCall(ctx, provider, "GET", probePath, fmt.Sprintf("probing registered route %s %s", route.Method, route.Path))
		probe := probeOneRoute(client, route, probePath)
		result.RoutesChecked++
		result.Routes = append(result.Routes, probe)
		switch probe.Status {
		case RouteProbeMissing:
			result.Missing = append(result.Missing, probe)
		case RouteProbeError:
			result.Errors = append(result.Errors, probe)
		}
	}
	return result
}

func probeKnownGoodRoute(client *platformclient.PlatformClient) (bool, string) {
	status, body, err := client.ProbeRoute(context.Background(), "/v1/whoami")
	if err != nil {
		return false, err.Error()
	}
	if status != http.StatusOK {
		return false, fmt.Sprintf("GET /v1/whoami returned http %d: %s", status, strings.TrimSpace(string(body)))
	}
	return true, ""
}

func probeOneRoute(client *platformclient.PlatformClient, route mcpauth.APIRoute, probePath string) RouteProbeResult {
	status, body, err := client.ProbeRoute(context.Background(), probePath)
	if err != nil {
		return RouteProbeResult{
			Method: route.Method, Path: route.Path, ProbePath: probePath,
			Status: RouteProbeError, Detail: err.Error(),
		}
	}
	if status == http.StatusNotFound && strings.TrimSpace(string(body)) == platformclient.MuxDefaultNotFoundBody {
		return RouteProbeResult{
			Method: route.Method, Path: route.Path, ProbePath: probePath,
			StatusCode: status, Status: RouteProbeMissing,
			Detail: "the plane's router has no route matching this path at all",
		}
	}
	return RouteProbeResult{
		Method: route.Method, Path: route.Path, ProbePath: probePath,
		StatusCode: status, Status: RouteProbeReachable,
	}
}

// substitutePathParameters replaces every {param} / {param...} wildcard
// segment in an API route's canonical path template with a literal
// placeholder segment. Go's net/http.ServeMux (1.22+) matches a wildcard
// against any non-empty literal segment, so the concrete value probed does
// not matter -- only that a value is present.
func substitutePathParameters(path string) string {
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		if strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") {
			segments[i] = "route-check-probe"
		}
	}
	return strings.Join(segments, "/")
}
