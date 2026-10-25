package expose

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/config/projectroot"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/cloud/platformapi"
	"github.com/sophium/erun/erun-common/infra/mcpauth"
	"github.com/sophium/erun/erun-common/infra/platformclient"
)

const (
	// RouteProbeReachable: any status other than the mux's own bare 404.
	RouteProbeReachable = "REACHABLE"
	// RouteProbeMissing: the mux's literal "404 page not found".
	RouteProbeMissing = "MISSING"
	// RouteProbeError: a transport failure left reachability unknown.
	RouteProbeError = "ERROR"
)

func resolveRouteCheckRoutesDir(explicit string) (string, error) {
	if trimmed := strings.TrimSpace(explicit); trimmed != "" {
		return trimmed, nil
	}
	_, root, err := projectroot.FindProjectRoot()
	if err != nil {
		return "", fmt.Errorf("resolve routes directory: %w (pass --routes-dir explicitly outside a checkout of the erun repository)", err)
	}
	return filepath.Join(root, "erun-backend", "erun-backend-api", "internal", "routes"), nil
}

// RunRouteCheck reports which registered routes a deployed plane does not
// serve. Missing routes and an unreachable plane are findings, not errors.
func RunRouteCheck(ctx core.Context, store config.CloudReadStore, alias string, params model.RouteCheckParams, deps cloud.CloudDependencies) (model.RouteCheckResult, error) {
	routesDir, err := resolveRouteCheckRoutesDir(params.RoutesDir)
	if err != nil {
		return model.RouteCheckResult{}, err
	}
	ctx.Trace("route-check: reading route inventory from " + routesDir)
	inventory, err := mcpauth.DiscoverAPIRoutes(routesDir)
	if err != nil {
		return model.RouteCheckResult{}, err
	}
	ctx.Trace(fmt.Sprintf("route-check: %d route(s) registered", len(inventory)))

	client, provider, err := platformapi.NewPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return model.RouteCheckResult{}, err
	}

	platformapi.TracePlatformCall(ctx, provider, "GET", "/v1/whoami",
		"sanity probe: the plane must answer before any route in the inventory is probed")
	if ctx.DryRun {
		traceRouteCheckInventory(ctx, provider, inventory)
		return model.RouteCheckResult{APIURL: provider.ERun.APIURL}, nil
	}

	sane, reason := probeKnownGoodRoute(client)
	if !sane {
		ctx.Trace("route-check: sanity probe failed: " + reason)
		return model.RouteCheckResult{APIURL: provider.ERun.APIURL, PlaneReachable: false, UnreachableReason: reason}, nil
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

func traceRouteCheckInventory(ctx core.Context, provider model.CloudProviderConfig, inventory []mcpauth.APIRoute) {
	for _, route := range inventory {
		platformapi.TracePlatformCall(ctx, provider, "GET", substitutePathParameters(route.Path),
			fmt.Sprintf("probing registered route %s %s", route.Method, route.Path))
	}
}

func probeRouteInventory(ctx core.Context, provider model.CloudProviderConfig, client *platformclient.PlatformClient, inventory []mcpauth.APIRoute) model.RouteCheckResult {
	var result model.RouteCheckResult
	for _, route := range inventory {
		probePath := substitutePathParameters(route.Path)
		platformapi.TracePlatformCall(ctx, provider, "GET", probePath, fmt.Sprintf("probing registered route %s %s", route.Method, route.Path))
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

func probeOneRoute(client *platformclient.PlatformClient, route mcpauth.APIRoute, probePath string) model.RouteProbeResult {
	status, body, err := client.ProbeRoute(context.Background(), probePath)
	if err != nil {
		return model.RouteProbeResult{
			Method: route.Method, Path: route.Path, ProbePath: probePath,
			Status: RouteProbeError, Detail: err.Error(),
		}
	}
	if status == http.StatusNotFound && strings.TrimSpace(string(body)) == platformclient.MuxDefaultNotFoundBody {
		return model.RouteProbeResult{
			Method: route.Method, Path: route.Path, ProbePath: probePath,
			StatusCode: status, Status: RouteProbeMissing,
			Detail: "the plane's router has no route matching this path at all",
		}
	}
	return model.RouteProbeResult{
		Method: route.Method, Path: route.Path, ProbePath: probePath,
		StatusCode: status, Status: RouteProbeReachable,
	}
}

// substitutePathParameters fills each {param} with a placeholder: ServeMux
// matches a wildcard against any non-empty segment.
func substitutePathParameters(path string) string {
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		if strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") {
			segments[i] = "route-check-probe"
		}
	}
	return strings.Join(segments, "/")
}
