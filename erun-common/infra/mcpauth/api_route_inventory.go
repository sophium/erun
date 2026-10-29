package mcpauth

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

// Routes are parsed from erun-backend-api's source rather than imported (it is a separate
// module), so the route check cannot drift from the router.

// APIRoute is one registered route, e.g. GET /v1/reviews/{review_id}.
type APIRoute struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

func (r APIRoute) String() string {
	return r.Method + " " + r.Path
}

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

// DiscoverAPIRoutes returns the sorted, de-duplicated routes registered in routesDir's non-test files,
// via register(http.MethodX, ...) or mux.HandleFunc("METHOD /path", ...).
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
