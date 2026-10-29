package integration

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/roleclassification"
)

// protectedRouteSites returns "METHOD /path" for every register(...) call in
// internal/routes; mux-registered unauthenticated routes never reach PermissionAuthorizer.
func protectedRouteSites(t testing.TB, root string) []string {
	t.Helper()
	dir := filepath.Join(root, "erun-backend", "erun-backend-api", "internal", "routes")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}

	fset := token.NewFileSet()
	var routes []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if route, ok := registerCallRoute(n); ok {
				routes = append(routes, route)
			}
			return true
		})
	}
	return routes
}

func registerCallRoute(n ast.Node) (string, bool) {
	call, ok := n.(*ast.CallExpr)
	if !ok {
		return "", false
	}
	ident, ok := call.Fun.(*ast.Ident)
	if !ok || ident.Name != "register" || len(call.Args) < 2 {
		return "", false
	}
	method, ok := httpMethodLiteral(call.Args[0])
	if !ok {
		return "", false
	}
	pathLit, ok := call.Args[1].(*ast.BasicLit)
	if !ok || pathLit.Kind != token.STRING {
		return "", false
	}
	p, err := strconv.Unquote(pathLit.Value)
	if err != nil {
		return "", false
	}
	return method + " " + p, true
}

// routeRoleClassifications reads only the keys of routeroles' Routes map.
func routeRoleClassifications(t testing.TB, root string) map[string]bool {
	t.Helper()
	path := filepath.Join(root, "erun-backend", "erun-backend-api", "internal", "routeroles", "route_roles.go")
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	classified := make(map[string]bool)
	apiRouteMapLiteral(file, "Routes", classified)
	return classified
}

// TestRouteRoleClassificationGate fails when an authenticated route is not in routeroles'
// Routes map; roles built from exact paths would otherwise silently miss new routes.
func TestRouteRoleClassificationGate(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	routes := protectedRouteSites(t, root)
	if len(routes) == 0 {
		t.Fatal("found zero protected routes to classify -- the enumeration is broken, not the classification")
	}

	classified := routeRoleClassifications(t, root)
	if len(classified) == 0 {
		t.Fatal("found zero classified routes -- the routeroles.Routes parse is broken, not the classification")
	}

	for _, route := range roleclassification.Unclassified(routes, classified) {
		t.Errorf("%s is not classified against the role model.\n"+
			"    Add it to erun-backend-api/internal/routeroles' Routes map as TenantUserClass, TenantAdminOnly, or OperationsOnly.", route)
	}
}
