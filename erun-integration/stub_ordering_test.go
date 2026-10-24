package integration

// Structural gate: a stub handler must not read a value assigned after its server began
// serving. No gated venue runs -race for this module, so ordering is checked in source.
// Reads .go files directly, so it must run uncached (-count=1).
// Not covered: handlers reached indirectly, aliased bindings, and writes through fields or indexes.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// stubOrderingHit carries a scan-root-relative path so hits read the same in any checkout.
type stubOrderingHit struct {
	file    string // path relative to the scan root, forward-slash separated
	readAt  int    // line of the handler's read of name
	writeAt int    // line of the assignment with no happens-before edge to it
	serving int    // line where the stub began serving
	name    string
}

func (h stubOrderingHit) Message() string {
	return fmt.Sprintf(
		"%s:%d: this handler reads %q, assigned at line %d -- after the stub began serving at line %d, so the read has no happens-before edge to it. Assign every value a handler reads before the server starts: httptest.NewUnstartedServer(mux), assign, then Start() (erun-integration/AGENTS.md § \"Isolation and portability\")",
		h.file, h.readAt, h.name, h.writeAt, h.serving)
}

// handlerRegisteringCalls limits the scan to HTTP handlers, not ordinary closures.
var handlerRegisteringCalls = map[string]bool{
	"Handle":      true,
	"HandleFunc":  true,
	"HandlerFunc": true,
}

// handlerRegistration does not check the receiver type, so aliased imports still match.
func handlerRegistration(call *ast.CallExpr) []*ast.FuncLit {
	var name string
	switch fun := call.Fun.(type) {
	case *ast.SelectorExpr:
		name = fun.Sel.Name
	case *ast.Ident:
		name = fun.Name
	default:
		return nil
	}
	if !handlerRegisteringCalls[name] {
		return nil
	}
	var lits []*ast.FuncLit
	for _, arg := range call.Args {
		if lit, ok := arg.(*ast.FuncLit); ok {
			lits = append(lits, lit)
		}
	}
	return lits
}

// stubScope is one function body; nested literals are separate scopes.
type stubScope struct {
	servingPos  token.Pos // token.NoPos when this scope never begins serving
	servingLine int
	servings    []servingSpan
	handlers    []*ast.FuncLit
	writes      map[string][]scopeWrite
}

// scopeWrite.define marks a short declaration, which starts a new binding.
type scopeWrite struct {
	pos    token.Pos
	line   int
	define bool
}

// servingSpan lets a handler passed to the serving call itself count as already running.
type servingSpan struct {
	pos token.Pos // start of the serving expression, for write comparison
	end token.Pos
}

func (s *stubScope) markServing(pos, end token.Pos, line int) {
	if s.servingPos == token.NoPos || pos < s.servingPos {
		s.servingPos, s.servingLine = pos, line
	}
	s.servings = append(s.servings, servingSpan{pos: pos, end: end})
}

func (s *stubScope) insideServing(lit *ast.FuncLit) bool {
	for _, span := range s.servings {
		if lit.Pos() > span.pos && lit.End() < span.end {
			return true
		}
	}
	return false
}

func (s *stubScope) recordWrite(name string, pos token.Pos, line int, define bool) {
	if name == "_" {
		return
	}
	s.writes[name] = append(s.writes[name], scopeWrite{pos: pos, line: line, define: define})
}

// writesAtOrAfter returns the first write to name at or after pos that the read at
// readPos resolves to; the serving statement's own binding counts.
func (s *stubScope) writesAtOrAfter(name string, readPos, pos token.Pos) (int, bool) {
	for _, write := range s.writes[name] {
		if write.pos < pos {
			continue
		}
		if s.shadowedFromRead(name, readPos, write.pos) {
			continue
		}
		return write.line, true
	}
	return 0, false
}

// shadowedFromRead reports whether a short declaration between readPos and writePos
// shadows the read's binding. The serving declaration itself is exempt.
func (s *stubScope) shadowedFromRead(name string, readPos, writePos token.Pos) bool {
	for _, decl := range s.writes[name] {
		if !decl.define || decl.pos == s.servingPos {
			continue
		}
		if decl.pos > readPos && decl.pos <= writePos {
			return true
		}
	}
	return false
}

func collectScopeFacts(fset *token.FileSet, body *ast.BlockStmt) *stubScope {
	scope := &stubScope{writes: map[string][]scopeWrite{}}
	if body == nil {
		return scope
	}
	line := func(pos token.Pos) int { return fset.Position(pos).Line }
	unstarted := map[string]bool{}

	ast.Inspect(body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.CallExpr:
			scope.handlers = append(scope.handlers, handlerRegistration(node)...)
		case *ast.AssignStmt:
			scope.recordAssign(node, line(node.Pos()), unstarted)
		case *ast.ExprStmt:
			if call, ok := node.X.(*ast.CallExpr); ok && isStartCall(call, unstarted) {
				scope.markServing(node.Pos(), node.End(), line(node.Pos()))
			}
		case *ast.GoStmt:
			// `go server.Start()` begins serving too.
			if isStartCall(node.Call, unstarted) {
				scope.markServing(node.Pos(), node.End(), line(node.Pos()))
			}
		}
		return true
	})
	return scope
}

func (s *stubScope) recordAssign(node *ast.AssignStmt, line int, unstarted map[string]bool) {
	names := assignedNames(node.Lhs)
	for _, name := range names {
		s.recordWrite(name, node.Pos(), line, node.Tok == token.DEFINE)
	}
	if serving := newServerCall(node.Rhs); serving != nil {
		s.markServing(node.Pos(), serving.End(), line)
	}
	for i, name := range names {
		if i < len(node.Rhs) && isNewUnstartedServerCall(node.Rhs[i]) {
			unstarted[name] = true
		}
	}
}

func assignedNames(lhs []ast.Expr) []string {
	var names []string
	for _, expr := range lhs {
		if ident, ok := expr.(*ast.Ident); ok {
			names = append(names, ident.Name)
		}
	}
	return names
}

func isNewUnstartedServerCall(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "NewUnstartedServer"
}

// newServerCall keys on the selector name so aliased imports are still caught.
func newServerCall(rhs []ast.Expr) *ast.CallExpr {
	for _, expr := range rhs {
		if call, ok := expr.(*ast.CallExpr); ok && isNewServerCall(call) {
			return call
		}
	}
	return nil
}

// servingConstructors begin serving before they return.
var servingConstructors = map[string]bool{
	"NewServer":    true,
	"NewTLSServer": true,
}

func isNewServerCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	return ok && servingConstructors[sel.Sel.Name]
}

func isStartCall(call *ast.CallExpr, unstarted map[string]bool) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Start" {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	return ok && unstarted[ident.Name]
}

type handlerRead struct {
	pos  token.Pos
	line int
}

// handlerReads excludes selector fields and composite-literal keys.
func handlerReads(fset *token.FileSet, lit *ast.FuncLit) map[string]handlerRead {
	bound := map[string]bool{}
	notARead := map[token.Pos]bool{}
	ast.Inspect(lit, func(n ast.Node) bool {
		for _, name := range boundNames(n) {
			bound[name] = true
		}
		switch node := n.(type) {
		case *ast.SelectorExpr:
			notARead[node.Sel.Pos()] = true
		case *ast.KeyValueExpr:
			if ident, ok := node.Key.(*ast.Ident); ok {
				notARead[ident.Pos()] = true
			}
		}
		return true
	})

	reads := map[string]handlerRead{}
	ast.Inspect(lit, func(n ast.Node) bool {
		ident, ok := n.(*ast.Ident)
		if !ok || bound[ident.Name] || notARead[ident.Pos()] {
			return true
		}
		reads[ident.Name] = handlerRead{pos: ident.Pos(), line: fset.Position(ident.Pos()).Line}
		return true
	})
	return reads
}

func boundNames(n ast.Node) []string {
	switch node := n.(type) {
	case *ast.AssignStmt:
		if node.Tok == token.DEFINE {
			return assignedNames(node.Lhs)
		}
	case *ast.ValueSpec:
		return identNames(node.Names)
	case *ast.Field:
		return identNames(node.Names)
	case *ast.RangeStmt:
		return assignedNames([]ast.Expr{node.Key, node.Value})
	}
	return nil
}

func identNames(idents []*ast.Ident) []string {
	names := make([]string, 0, len(idents))
	for _, ident := range idents {
		names = append(names, ident.Name)
	}
	return names
}

func stubOrderingViolationsInFile(fset *token.FileSet, file *ast.File) []stubOrderingHit {
	var bodies []*ast.BlockStmt
	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.FuncDecl:
			bodies = append(bodies, node.Body)
		case *ast.FuncLit:
			bodies = append(bodies, node.Body)
		}
		return true
	})

	var hits []stubOrderingHit
	for _, body := range bodies {
		scope := collectScopeFacts(fset, body)
		if scope.servingPos == token.NoPos {
			continue
		}
		for _, lit := range scope.handlers {
			// A handler registered after serving began is ordered, unless it was passed to the
			// serving construct itself.
			if !scope.insideServing(lit) && lit.Pos() >= scope.servingPos {
				continue
			}
			for name, read := range handlerReads(fset, lit) {
				writeLine, ok := scope.writesAtOrAfter(name, read.pos, scope.servingPos)
				if !ok {
					continue
				}
				hits = append(hits, stubOrderingHit{
					readAt:  read.line,
					writeAt: writeLine,
					serving: scope.servingLine,
					name:    name,
				})
			}
		}
	}
	return hits
}

func findStubOrderingViolations(t testing.TB, root string) []stubOrderingHit {
	t.Helper()
	var hits []stubOrderingHit
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if path != root && skipDirForStubScan(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return fmt.Errorf("parse %s: %w", path, parseErr)
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return fmt.Errorf("relativize %s: %w", path, relErr)
		}
		rel = filepath.ToSlash(rel)
		for _, hit := range stubOrderingViolationsInFile(fset, file) {
			hit.file = rel
			hits = append(hits, hit)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan harness for stub-ordering violations: %v", err)
	}
	return hits
}

func skipDirForStubScan(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", "dist", "wailsjs", ".claude", ".claude-plugin", ".vscode", "testdata", "coverage":
		return true
	default:
		return false
	}
}

func TestFixtureStubsAssignBeforeServing(t *testing.T) {
	t.Parallel()
	for _, hit := range findStubOrderingViolations(t, harnessModuleRoot(t)) {
		t.Errorf("%s", hit.Message())
	}
}

// TestStubOrderingScannerFixtures pins both the shapes to catch and the correct ones to
// leave alone.
func TestStubOrderingScannerFixtures(t *testing.T) {
	t.Parallel()
	for name, fixture := range stubOrderingScannerFixtures {
		t.Run(name, func(t *testing.T) {
			assertStubOrderingFixture(t, fixture.src, fixture.names)
		})
	}
}

func assertStubOrderingFixture(t *testing.T, src string, want []string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fixture_test.go")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var got []string
	for _, hit := range findStubOrderingViolations(t, dir) {
		got = append(got, hit.name)
	}
	if len(got) != len(want) {
		t.Fatalf("scanner reported %v, want the names %v", got, want)
	}
	for _, name := range want {
		if !slices.Contains(got, name) {
			t.Fatalf("scanner reported %v, want %v", got, want)
		}
	}
}
