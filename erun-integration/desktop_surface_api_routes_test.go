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

// httpMethodConstants maps net/http method constant names to their method strings.
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

// httpMethodLiteral resolves an http.MethodX constant or string literal to its method.
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

// apiRouteSite is one "METHOD /path" registration call site.
type apiRouteSite struct {
	method string
	path   string
}

// apiRouteMapLiteral adds the string keys of the map[string]bool literal mapName to dest.
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

// apiRouteWailsBindings maps an API route to the erun-ui *App method that reaches
// it through Wails, for routes whose path never appears in TypeScript. Each entry
// must call the platformclient method issuing that route and be called from
// erun-ui/frontend/src.
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
	// Both hostname routes apply only when a platform alias is configured.
	"PUT /v1/environments/{environment_id}/hostname":    "ExposeEnvironmentService",
	"DELETE /v1/environments/{environment_id}/hostname": "UnexposeEnvironment",
	"GET /v1/invite-requests/mine":                      "GetMyTenantInviteRequest",
	"GET /v1/environments/{environment_id}/definition":  "CheckHostedDefinitionDrift",
	// Also driven by the config watcher's automatic upload.
	"PUT /v1/environments/{environment_id}/definition": "UploadHostedDefinition",
	"GET /v1/builds": "LoadTenantDashboard",
}

// apiRouteCapabilities returns one Capability per route registered in
// erun-backend-api/internal/routes, parsed rather than imported (separate module).
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

func isNonTestGoFile(entry os.DirEntry) bool {
	name := entry.Name()
	return !entry.IsDir() && strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")
}

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
