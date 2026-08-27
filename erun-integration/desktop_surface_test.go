package integration

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	cmd "github.com/sophium/erun/cmd"
	eruncommon "github.com/sophium/erun/erun-common"
	"github.com/sophium/erun/erun-integration/internal/desktopsurface"
	"github.com/spf13/cobra"
)

// repoRoot resolves the checkout root from this file's own location rather
// than the working directory `go test` happens to run from, so the gate finds
// erun-ui/frontend regardless of how it is invoked.
func repoRoot(t testing.TB) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller could not resolve this test file's path")
	}
	// This file lives at <repoRoot>/erun-integration/desktop_surface_test.go.
	return filepath.Dir(filepath.Dir(file))
}

// readOperatorSurfaceSource concatenates every .ts/.tsx file under each given
// src root -- the committed, human-facing subtrees that together make up
// "the operator's surface": the desktop app (erun-ui/frontend/src) and the
// hosted web console (erun-console/src). Their sibling wailsjs/dist/node_modules
// directories are generated and gitignored, so a capability's binding
// showing up there is not evidence a human can reach it -- see
// erun-ui/.gitignore and erun-console/.gitignore.
func readOperatorSurfaceSource(t testing.TB, roots ...string) desktopsurface.FrontendSource {
	t.Helper()
	var sb strings.Builder
	for _, srcDir := range roots {
		err := filepath.WalkDir(srcDir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if !strings.HasSuffix(path, ".ts") && !strings.HasSuffix(path, ".tsx") {
				return nil
			}
			content, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			sb.Write(content)
			sb.WriteByte('\n')
			return nil
		})
		if err != nil {
			t.Fatalf("read %s: %v", srcDir, err)
		}
	}
	return desktopsurface.FrontendSource(sb.String())
}

// capabilityToken picks the literal word most likely to appear in whatever
// frontend code calls a capability: the last CLI path segment when one
// exists, falling further back to family, then the raw name, for a wire-only
// tool. A hyphenated leaf (review_queue_override-advance's "override-advance")
// is trimmed to its final word ("advance"): the compound name's qualifier
// rarely appears verbatim in frontend prose or identifiers, but its core verb
// does, since that is also the plain command's own leaf name.
func capabilityToken(name, family string, cliPath []string) string {
	token := name
	if len(cliPath) > 0 {
		token = cliPath[len(cliPath)-1]
	} else if family != "" {
		token = family
	}
	if idx := strings.LastIndex(token, "-"); idx >= 0 && idx+1 < len(token) {
		token = token[idx+1:]
	}
	return token
}

// mcpCapabilities returns one Capability per registered MCP tool.
func mcpCapabilities() []desktopsurface.Capability {
	names := eruncommon.MCPToolNames()
	capabilities := make([]desktopsurface.Capability, 0, len(names))
	for _, name := range names {
		descriptor, ok := eruncommon.MCPToolDescriptorFor(name)
		if !ok {
			continue
		}
		capabilities = append(capabilities, desktopsurface.Capability{
			Name:            name,
			Source:          "MCP tool",
			Token:           capabilityToken(name, descriptor.Family, descriptor.CLIPath),
			AgentFacing:     descriptor.AgentFacing,
			DeclarationHint: fmt.Sprintf("erun-common/mcp_tools.go's AgentFacing field on %q's MCPToolDescriptor entry", name),
		})
	}
	return capabilities
}

// mcpCoveredCLIPaths returns every CLI path (space-joined, below "erun") that
// an MCP tool already represents, so the CLI tree walk below does not
// double-count the same capability under its CLI identity too.
func mcpCoveredCLIPaths() map[string]bool {
	covered := make(map[string]bool)
	for _, name := range eruncommon.MCPToolNames() {
		descriptor, ok := eruncommon.MCPToolDescriptorFor(name)
		if !ok || len(descriptor.CLIPath) == 0 {
			continue
		}
		covered[strings.Join(descriptor.CLIPath, " ")] = true
	}
	return covered
}

// cliOnlyCapabilities walks the real, fully-wired erun command tree and
// returns one Capability per invocable command with no matching MCP tool.
// Hidden and Deprecated commands are excluded outright: both are themselves
// explicit, reviewable markers at the command's own definition site (a
// contributor sees them in the diff that adds the command), so they need no
// second declaration here -- see erun-cli/cmd/command_tree.go.
func cliOnlyCapabilities(t testing.TB) []desktopsurface.Capability {
	t.Helper()
	covered := mcpCoveredCLIPaths()
	var capabilities []desktopsurface.Capability
	// hidden propagates down the tree: Cobra's own Hidden field only removes a
	// command from its *parent's* help listing, but a command nested under a
	// Hidden parent (erun-cli/cmd/activity.go's whole "activity" group) is just
	// as undiscoverable to a human as the parent itself, so a leaf must not
	// need its own redundant Hidden to inherit that.
	var walk func(c *cobra.Command, path []string, hidden bool)
	walk = func(c *cobra.Command, path []string, hidden bool) {
		for _, child := range c.Commands() {
			childPath := append(append([]string{}, path...), child.Name())
			childHidden := hidden || child.Hidden
			if child.Runnable() && !childHidden && child.Deprecated == "" {
				full := strings.Join(childPath, " ")
				if !covered[full] {
					capabilities = append(capabilities, desktopsurface.Capability{
						Name:            full,
						Source:          "CLI command",
						Token:           capabilityToken("", "", childPath),
						AgentFacing:     cmd.IsAgentFacingCLIOnlyCommand(full),
						DeclarationHint: fmt.Sprintf("erun-cli/cmd/command_tree.go's cliOnlyAgentFacingCommands map (add %q)", full),
					})
				}
			}
			walk(child, childPath, childHidden)
		}
	}
	walk(cmd.CommandTreeForAudit(), nil, false)
	sort.Slice(capabilities, func(i, j int) bool { return capabilities[i].Name < capabilities[j].Name })
	return capabilities
}

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
// routes.InternalAPIRoutes, parsed the same way rather than imported.
func apiRouteCapabilities(t testing.TB, root string) []desktopsurface.Capability {
	t.Helper()
	dir := filepath.Join(root, "erun-backend", "erun-backend-api", "internal", "routes")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}

	fset := token.NewFileSet()
	internal := make(map[string]bool)
	var sites []apiRouteSite

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
			spec, ok := n.(*ast.ValueSpec)
			if !ok || len(spec.Names) != 1 || spec.Names[0].Name != "InternalAPIRoutes" || len(spec.Values) != 1 {
				return true
			}
			composite, ok := spec.Values[0].(*ast.CompositeLit)
			if !ok {
				return true
			}
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
					internal[v] = true
				}
			}
			return true
		})

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch fn := call.Fun.(type) {
			case *ast.Ident:
				if fn.Name != "register" || len(call.Args) < 2 {
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
				sites = append(sites, apiRouteSite{method: method, path: p})
			case *ast.SelectorExpr:
				if fn.Sel.Name != "HandleFunc" || len(call.Args) < 1 {
					return true
				}
				lit, ok := call.Args[0].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				raw, err := strconv.Unquote(lit.Value)
				if err != nil {
					return true
				}
				method, p, ok := strings.Cut(raw, " ")
				if !ok {
					return true
				}
				sites = append(sites, apiRouteSite{method: method, path: p})
			}
			return true
		})
	}

	capabilities := make([]desktopsurface.Capability, 0, len(sites))
	for _, s := range sites {
		full := s.method + " " + s.path
		capabilities = append(capabilities, desktopsurface.Capability{
			Name:            full,
			Source:          "API route",
			Pattern:         desktopsurface.APIRoutePattern(s.path),
			AgentFacing:     internal[full],
			DeclarationHint: fmt.Sprintf("erun-backend-api/internal/routes/route_audit.go's InternalAPIRoutes map (add %q with a comment explaining why)", full),
		})
	}
	sort.Slice(capabilities, func(i, j int) bool { return capabilities[i].Name < capabilities[j].Name })
	return capabilities
}

// TestDesktopSurfaceGate fails when a user-facing CLI command, MCP tool, or
// API route has no way in from an operator surface -- erun-ui/frontend or
// erun-console (erun AGENTS.md § "Smooth, Seamless, No Dead Ends", failure
// mode 3). See erun-integration/AGENTS.md, erun-common/mcp_tools.go's
// AgentFacing field, erun-cli/cmd/command_tree.go's
// cliOnlyAgentFacingCommands, and
// erun-backend-api/internal/routes/route_audit.go's InternalAPIRoutes for
// the three declaration mechanisms.
func TestDesktopSurfaceGate(t *testing.T) {
	root := repoRoot(t)
	operatorSurface := readOperatorSurfaceSource(t,
		filepath.Join(root, "erun-ui", "frontend", "src"),
		filepath.Join(root, "erun-console", "src"),
	)

	capabilities := append(mcpCapabilities(), cliOnlyCapabilities(t)...)
	capabilities = append(capabilities, apiRouteCapabilities(t, root)...)
	if len(capabilities) == 0 {
		t.Fatal("found zero capabilities to audit -- the enumeration is broken, not the desktop surface")
	}

	missing := desktopsurface.FindMissingDesktopSurface(capabilities, operatorSurface)
	for _, m := range missing {
		t.Errorf("%s", m.Message())
	}
}

// appMethodDecls parses every non-test .go file directly under erun-ui (not
// its subpackages: *App lives at the module root) and returns one
// AppMethodDecl per method on *App, with IdentUses counting how many other
// places in the same file set mention the bare method name -- a real caller,
// or nothing.
func appMethodDecls(t testing.TB, root string) []desktopsurface.AppMethodDecl {
	t.Helper()
	dir := filepath.Join(root, "erun-ui")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read erun-ui: %v", err)
	}

	fset := token.NewFileSet()
	files := make(map[string]*ast.File)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		files[path] = file
	}

	// identCounts[name] counts every bare-identifier occurrence of name across
	// the whole file set, including its own declaration; FindUnboundAppMethods
	// only needs "any other use", so the declaration's own contribution
	// (exactly 1 per method, from the FuncDecl.Name identifier) is subtracted.
	identCounts := make(map[string]int)
	for _, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			if ident, ok := n.(*ast.Ident); ok {
				identCounts[ident.Name]++
			}
			return true
		})
	}

	var decls []desktopsurface.AppMethodDecl
	for path, file := range files {
		for _, d := range file.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || !desktopsurface.IsAppPointerReceiverMethod(fn) {
				continue
			}
			name := fn.Name.Name
			decls = append(decls, desktopsurface.AppMethodDecl{
				Name:      name,
				Exported:  desktopsurface.IsExportedName(name),
				File:      filepath.Base(path),
				Line:      fset.Position(fn.Pos()).Line,
				IdentUses: identCounts[name] - 1,
			})
		}
	}
	return decls
}

// TestNoUnboundAppMethods fails when a method on *App is unexported and
// called from nowhere else in erun-ui: Wails binds only exported methods, so
// that combination is a Wails binding location with no binding -- the
// specific shape whipOrchestratorNow shipped in.
func TestNoUnboundAppMethods(t *testing.T) {
	root := repoRoot(t)
	decls := appMethodDecls(t, root)
	if len(decls) == 0 {
		t.Fatal("found zero *App methods -- the parse is broken, not erun-ui")
	}
	for _, u := range desktopsurface.FindUnboundAppMethods(decls) {
		t.Errorf("%s", u.Message())
	}
}
