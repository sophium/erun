package eruncommon

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
)

// api_route_inventory.go reads the router's own registered routes directly
// out of erun-backend-api's source -- the same "read the source, don't
// import it" mechanism erun-integration's desktop-surface and
// role-classification gates already use for the same cross-module reason
// (erun-backend-api is a separate Go module, so importing it directly would
// be a new cross-module dependency rather than reuse of an existing one).
// RunRouteCheck (route_check.go) uses this as its route inventory instead of
// a hand-maintained list -- a hand-maintained list drifts, which is the
// same defect one level up: a route can merge, close its issue, and never
// actually be exercised against a deployed plane.

// APIRoute is one route erun-backend-api's router registers: a canonical
// method and path template, e.g. GET /v1/gate-runs/{gate_run_id}.
type APIRoute struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

func (r APIRoute) String() string {
	return r.Method + " " + r.Path
}

// httpMethodConstants maps the http.MethodX identifiers erun-backend-api's
// register(...) calls use to their string values, so a method argument
// written either way resolves the same.
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

// DiscoverAPIRoutes parses every non-test .go file directly under routesDir
// (erun-backend-api/internal/routes) and returns every route it registers,
// sorted and de-duplicated. It recognizes the two registration shapes that
// module's route files use: register(http.MethodX, "path", handler) (the
// ProtectedRouteRegistrar convention every authenticated route uses) and
// mux.HandleFunc("METHOD /path", handler) (the handful of intentionally
// unauthenticated routes registered directly on the mux).
func DiscoverAPIRoutes(routesDir string) ([]APIRoute, error) {
	entries, err := os.ReadDir(routesDir)
	if err != nil {
		return nil, fmt.Errorf("read routes directory %s: %w", routesDir, err)
	}
	seen := map[string]APIRoute{}
	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(routesDir, name), nil, 0)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", name, err)
		}
		for _, route := range routesFromFile(file) {
			seen[route.String()] = route
		}
	}
	routes := make([]APIRoute, 0, len(seen))
	for _, route := range seen {
		routes = append(routes, route)
	}
	sort.Slice(routes, func(i, j int) bool {
		if routes[i].Path != routes[j].Path {
			return routes[i].Path < routes[j].Path
		}
		return routes[i].Method < routes[j].Method
	})
	return routes, nil
}

func routesFromFile(file *ast.File) []APIRoute {
	var routes []APIRoute
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if route, ok := registerCallRoute(call); ok {
			routes = append(routes, route)
			return true
		}
		if route, ok := handleFuncCallRoute(call); ok {
			routes = append(routes, route)
		}
		return true
	})
	return routes
}

// registerCallRoute recognizes register(method, "path", handler) -- the
// routes.ProtectedRouteRegistrar convention every authenticated route in
// internal/routes uses.
func registerCallRoute(call *ast.CallExpr) (APIRoute, bool) {
	ident, ok := call.Fun.(*ast.Ident)
	if !ok || ident.Name != "register" || len(call.Args) < 2 {
		return APIRoute{}, false
	}
	method, ok := resolveHTTPMethodArg(call.Args[0])
	if !ok {
		return APIRoute{}, false
	}
	path, ok := stringLiteralValue(call.Args[1])
	if !ok {
		return APIRoute{}, false
	}
	return APIRoute{Method: method, Path: path}, true
}

// handleFuncCallRoute recognizes mux.HandleFunc("METHOD /path", handler) --
// the combined-pattern form the mux's own intentionally unauthenticated
// routes (platform discovery, invite accept/request) use.
func handleFuncCallRoute(call *ast.CallExpr) (APIRoute, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "HandleFunc" || len(call.Args) < 1 {
		return APIRoute{}, false
	}
	pattern, ok := stringLiteralValue(call.Args[0])
	if !ok {
		return APIRoute{}, false
	}
	method, path, ok := strings.Cut(pattern, " ")
	if !ok {
		return APIRoute{}, false
	}
	return APIRoute{Method: method, Path: path}, true
}

func resolveHTTPMethodArg(expr ast.Expr) (string, bool) {
	if lit, ok := stringLiteralValue(expr); ok {
		return lit, true
	}
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "http" {
		return "", false
	}
	method, ok := httpMethodConstants[sel.Sel.Name]
	return method, ok
}

func stringLiteralValue(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return value, true
}
