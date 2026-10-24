package integration

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/desktopsurface"
)

// httpMethodConstants maps the net/http method constant names used at every
// route's registration call site (register(http.MethodGet, ...)) to their
// literal method strings.
var httpMethodConstants = map[string]string{
	"MethodGet":     "GET",
	"MethodHead":    "HEAD",
	"MethodPost":    "POST",
	"MethodPut":     "PUT",
	"MethodPatch":   "PATCH",
	"MethodDelete":  "DELETE",
	"MethodConnect": "CONNECT",
	"MethodOptions": "OPTIONS",
	"MethodTrace":   "TRACE",
}

// httpMethodLiteral resolves an argument expression to its HTTP method
// string, whether written as an http.MethodX constant (every register(...)
// call site) or a plain string literal.
func httpMethodLiteral(expr ast.Expr) (string, bool) {
	switch e := expr.(type) {
	case *ast.SelectorExpr:
		ident, ok := e.X.(*ast.Ident)
		if !ok || ident.Name != "http" {
			return "", false
		}
		method, ok := httpMethodConstants[e.Sel.Name]
		return method, ok
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return "", false
		}
		v, err := strconv.Unquote(e.Value)
		if err != nil {
			return "", false
		}
		return v, true
	}
	return "", false
}

// apiRouteSite is one "METHOD /path" registration call site found by parsing
// erun-backend-api/internal/routes source.
type apiRouteSite struct {
	method string
	path   string
}

// apiRouteMapLiteral extracts the string keys of a package-level
// map[string]bool literal named mapName (e.g. InternalAPIRoutes,
// KnownUnsurfacedRoutes) from a parsed file, into dest.
func apiRouteMapLiteral(file *ast.File, mapName string, dest map[string]bool) {
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok || len(spec.Names) != 1 || spec.Names[0].Name != mapName || len(spec.Values) != 1 {
			return true
		}
		if composite, ok := spec.Values[0].(*ast.CompositeLit); ok {
			stringKeysOf(composite, dest)
		}
		return true
	})
}

// stringKeysOf adds every string-literal key of a map composite literal to dest.
func stringKeysOf(composite *ast.CompositeLit, dest map[string]bool) {
	for _, elt := range composite.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.BasicLit)
		if !ok || key.Kind != token.STRING {
			continue
		}
		if v, err := strconv.Unquote(key.Value); err == nil {
			dest[v] = true
		}
	}
}

// apiRouteWailsBindings hand-maps a "METHOD /path" API route to the exported
// erun-ui *App method that provides its real desktop entry point, for the
// routes where the operator surface exists but is reachable only through
// Wails, never through a literal path in TypeScript (see
// desktopsurface.Capability.WailsBinding). Each entry is verified on both
// ends before it is added here: the named erun-ui/*.go method calls the
// platformclient.PlatformClient method that issues this exact route (e.g.
// AddReviewer -> client.AddReviewer -> "POST /v1/reviews/{review_id}/reviewers"
// in erun-common/infra/platformclient/platform_client_reviews.go), and the same method name is
// called from erun-ui/frontend/src. A shared entry point that loads several
// routes at once (erun-ui/tenant_review_detail.go's LoadReviewDetail, called
// from erun-ui/frontend/src/app/api/reviewDetailApi.ts, fans out to
// ListReviewers/ListComments/ListBuilds) maps each of those routes to that
// same shared name.
var apiRouteWailsBindings = map[string]string{
	"GET /v1/reviews/{review_id}/reviewers":                      "LoadReviewDetail",
	"POST /v1/reviews/{review_id}/reviewers":                     "AddReviewer",
	"DELETE /v1/reviews/{review_id}/reviewers/{user_id}":         "RemoveReviewer",
	"GET /v1/reviews/{review_id}/comments":                       "LoadReviewDetail",
	"POST /v1/reviews/{review_id}/comments":                      "CreateReviewComment",
	"PATCH /v1/reviews/{review_id}/comments/{comment_id}/status": "ResolveReviewComment",
	"GET /v1/reviews/{review_id}/builds":                         "LoadReviewDetail",
	"PATCH /v1/reviews/{review_id}/status":                       "CloseReview",
	"POST /v1/reviews/merge-queue/advance":                       "AdvanceMergeQueue",
	"POST /v1/reviews/merge-queue/override-advance":              "OverrideAdvanceMergeQueue",
	"POST /v1/provision":                                         "PreviewPlatformProvision",
	"POST /v1/environments/{environment_id}/stop":                "StopPlatformEnvironment",
	// ExposeEnvironmentService -> expose.RunExposeService ->
	// resolveExposeDNSUpserter -> (only when an erun platform alias is
	// configured, never for the direct-pdnsutil path) client.
	// SetEnvironmentHostname -> "PUT /v1/environments/{environment_id}/hostname"
	// (erun-common/command/expose/expose_platform_dns.go); UnexposeEnvironment
	// is the same chain through RunUnexposeService/DeleteEnvironmentHostname
	// for the DELETE. Called from
	// erun-ui/frontend/src/components/app/ManageDialogPortsExposures.tsx via
	// app/manageExposureThunks.ts -- the Ports tab's existing expose/unexpose
	// controls, which need no new UI for this: the platform-route decision is
	// automatic on the shared erun-common path both transports already call.
	"PUT /v1/environments/{environment_id}/hostname":    "ExposeEnvironmentService",
	"DELETE /v1/environments/{environment_id}/hostname": "UnexposeEnvironment",
	// GetMyTenantInviteRequest -> client.MyInviteRequest -> "GET
	// /v1/invite-requests/mine" in
	// erun-common/infra/platformclient/platform_client_invites.go, called
	// from erun-ui/frontend/src/app/api/tenantInviteRequestApi.ts (which
	// tenantEnrollmentPoll.ts and the tenant dashboard's NotEnrolledState
	// both read through) -- the real desktop entry point this route's own
	// literal path never appears in TypeScript for.
	"GET /v1/invite-requests/mine": "GetMyTenantInviteRequest",
	// CheckHostedDefinitionDrift -> hostedDefinitionDriftFor ->
	// client.GetEnvironmentDefinition -> "GET
	// /v1/environments/{environment_id}/definition" in
	// erun-common/infra/platformclient/platform_client.go. Called from
	// erun-ui/frontend/src/app/hostedDefinitionDrift.ts, which the Manage
	// dialog's General tab reads through HostedDefinitionSection.tsx -- the
	// hosted-marker panel, whose "Check for updates" control is this route's
	// desktop entry point. The route's literal path never appears in
	// TypeScript for the same reason the other entries here do not: the
	// desktop reaches the platform through platformclient.PlatformClient.
	"GET /v1/environments/{environment_id}/definition": "CheckHostedDefinitionDrift",
	// UploadHostedDefinition -> uploadHostedDefinition ->
	// platform.PushEnvironmentDefinition -> client.PutEnvironmentDefinition
	// -> "PUT /v1/environments/{environment_id}/definition". Called from
	// erun-ui/frontend/src/app/hostedDefinitionDrift.ts, which
	// HostedDefinitionSection.tsx's "Upload to platform" control reads
	// through -- the recovery action for an environment whose settings moved
	// and whose automatic upload could not reach the platform. The same
	// transaction is driven without an operator click by the config watcher
	// (erun-ui/config_watcher.go -> reactToConfigWatchTargets ->
	// autoUploadHostedDefinition), which is why the desktop owns this route
	// at all rather than leaving it to the CLI.
	"PUT /v1/environments/{environment_id}/definition": "UploadHostedDefinition",
	// LoadTenantDashboard -> appendUnattachedTenantDashboardBuilds ->
	// client.ListAllBuilds -> "GET /v1/builds" (erun-ui/tenant_dashboard.go,
	// erun-common/infra/platformclient/platform_client_builds.go) -- merges an unattached build
	// into the same Builds tab the review-nested read above already
	// populates.
	"GET /v1/builds": "LoadTenantDashboard",
}

// apiRouteCapabilities parses every non-test .go file in
// erun-backend-api/internal/routes (not imported: it is a separate Go
// module, the same reason erun-ui's own .go files are parsed rather than
// imported below) and returns one Capability per registered HTTP route --
// every "register(http.MethodX, "path", ...)" call (the
// routes.ProtectedRouteRegistrar convention every route file uses) plus
// every route registered directly on the mux via
// "mux.HandleFunc("METHOD /path", ...)" for the handful of intentionally
// unauthenticated routes (see erun-backend-api/internal/routes/platform.go,
// invites.go). A route is AgentFacing when its "METHOD /path" key appears in
// routes.InternalAPIRoutes, and KnownGap when it appears in
// routes.KnownUnsurfacedRoutes instead -- both parsed the same way rather
// than imported.
func apiRouteCapabilities(t testing.TB, root string) []desktopsurface.Capability {
	t.Helper()
	dir := filepath.Join(root, "erun-backend", "erun-backend-api", "internal", "routes")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}

	fset := token.NewFileSet()
	internal := make(map[string]bool)
	knownGap := make(map[string]bool)
	var sites []apiRouteSite

	for _, entry := range entries {
		name := entry.Name()
		if !isNonTestGoFile(entry) {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}

		apiRouteMapLiteral(file, "InternalAPIRoutes", internal)
		apiRouteMapLiteral(file, "KnownUnsurfacedRoutes", knownGap)
		sites = append(sites, apiRouteSitesIn(file)...)
	}

	capabilities := make([]desktopsurface.Capability, 0, len(sites))
	for _, s := range sites {
		full := s.method + " " + s.path
		capabilities = append(capabilities, desktopsurface.Capability{
			Name:            full,
			Source:          "API route",
			Pattern:         desktopsurface.APIRoutePattern(s.path),
			AgentFacing:     internal[full],
			KnownGap:        knownGap[full],
			WailsBinding:    apiRouteWailsBindings[full],
			DeclarationHint: fmt.Sprintf("erun-backend-api/internal/routes/route_audit.go's InternalAPIRoutes map (add %q with a comment explaining why)", full),
			BaselineHint:    fmt.Sprintf("erun-backend-api/internal/routes/route_audit.go's KnownUnsurfacedRoutes map (remove %q)", full),
		})
	}
	sort.Slice(capabilities, func(i, j int) bool { return capabilities[i].Name < capabilities[j].Name })
	return capabilities
}

// isNonTestGoFile reports whether a directory entry is a Go source file that
// is not a test.
func isNonTestGoFile(entry os.DirEntry) bool {
	name := entry.Name()
	return !entry.IsDir() && strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")
}

// apiRouteSitesIn returns every route registration call site in file, in
// source order.
func apiRouteSitesIn(file *ast.File) []apiRouteSite {
	var sites []apiRouteSite
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		var site apiRouteSite
		switch fn := call.Fun.(type) {
		case *ast.Ident:
			site, ok = registerCallSite(fn, call)
		case *ast.SelectorExpr:
			site, ok = handleFuncCallSite(fn, call)
		}
		if ok {
			sites = append(sites, site)
		}
		return true
	})
	return sites
}

// registerCallSite reads a register(http.MethodX, "path", ...) call.
func registerCallSite(fn *ast.Ident, call *ast.CallExpr) (apiRouteSite, bool) {
	if fn.Name != "register" || len(call.Args) < 2 {
		return apiRouteSite{}, false
	}
	method, ok := httpMethodLiteral(call.Args[0])
	if !ok {
		return apiRouteSite{}, false
	}
	pathLit, ok := call.Args[1].(*ast.BasicLit)
	if !ok || pathLit.Kind != token.STRING {
		return apiRouteSite{}, false
	}
	p, err := strconv.Unquote(pathLit.Value)
	if err != nil {
		return apiRouteSite{}, false
	}
	return apiRouteSite{method: method, path: p}, true
}

// handleFuncCallSite reads a mux.HandleFunc("METHOD /path", ...) call.
func handleFuncCallSite(fn *ast.SelectorExpr, call *ast.CallExpr) (apiRouteSite, bool) {
	if fn.Sel.Name != "HandleFunc" || len(call.Args) < 1 {
		return apiRouteSite{}, false
	}
	lit, ok := call.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return apiRouteSite{}, false
	}
	raw, err := strconv.Unquote(lit.Value)
	if err != nil {
		return apiRouteSite{}, false
	}
	method, p, ok := strings.Cut(raw, " ")
	if !ok {
		return apiRouteSite{}, false
	}
	return apiRouteSite{method: method, path: p}, true
}
