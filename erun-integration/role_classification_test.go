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

// protectedRouteSites parses every non-test .go file in
// erun-backend-api/internal/routes and returns one "METHOD /path" key per
// register(http.MethodX, "path", ...) call site -- the
// routes.ProtectedRouteRegistrar convention every route file uses. Unlike
// apiRouteCapabilities above (which also walks the couple of routes
// registered directly on the mux for platform.go/invites.go's intentionally
// unauthenticated endpoints), this deliberately excludes them: an
// unauthenticated route never reaches PermissionAuthorizer, so classifying
// it against TenantUser/TenantAdmin would assert something the route can
// never actually be gated on.
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
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			ident, ok := call.Fun.(*ast.Ident)
			if !ok || ident.Name != "register" || len(call.Args) < 2 {
				return true
			}
			method, ok := httpMethodLiteral(call.Args[0])
			if !ok {
				return true
			}
			pathLit, ok := call.Args[1].(*ast.BasicLit)
			if !ok || pathLit.Kind != token.STRING {
				return true
			}
			p, err := strconv.Unquote(pathLit.Value)
			if err != nil {
				return true
			}
			routes = append(routes, method+" "+p)
			return true
		})
	}
	return routes
}

// routeRoleClassifications parses erun-backend-api/internal/routeroles'
// Routes map literal the same way apiRouteMapLiteral above reads
// InternalAPIRoutes/KnownUnsurfacedRoutes: it only needs each key's
// presence, not which routeroles.Class the value names, so the same
// generic bool-map reader works unchanged here too.
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

// TestRouteRoleClassificationGate fails when a registered, authenticated API
// route is not classified against the predefined role model in
// erun-backend-api/internal/routeroles' Routes map -- the same "classify
// every route or fail" treatment as TestDesktopSurfaceGate above, applied to
// who may call a route instead of whether an operator can reach it. Without
// this, a role defined by enumerated exact paths would silently fail to
// grant a route nobody remembered to add, which is a narrower role's own
// hazard: a wildcard role (ReadAll/WriteAll) can never miss a new route this
// way, since it grants everything by pattern.
func TestRouteRoleClassificationGate(t *testing.T) {
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
