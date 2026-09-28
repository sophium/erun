package integration

// stub_ordering_test.go is a structural gate for the fixture stubs' own
// startup ordering, in the same shape as exec_bound_test.go: an AST walk that
// fails the build the next time the defect reappears, rather than another
// instruction to follow.
//
// The defect it prevents is a stub server that begins serving before the
// values its handlers read have been written. `httptest.NewServer` starts
// serving before it returns, so a value assigned from its result -- or any
// other value assigned after it -- has no happens-before edge to a handler
// goroutine that reads it. Only `-race` reports the resulting pair, and no
// gated venue runs `-race` for this module (erun-integration/AGENTS.md
// § "Isolation and portability"), so a behavioral probe cannot red in the gate
// on the pre-fix tree. This check can, because the ordering is a property of
// the source rather than of a schedule: the module's `-race` probes stay the
// evidence that the pair is real, and this is what keeps the shape from coming
// back where no gate would notice.
//
// What it covers: every handler literal passed to mux.Handle, mux.HandleFunc
// or http.HandlerFunc in a scope that begins serving, read against every name
// that scope assigns at or after the point serving begins. Serving begins at
// httptest.NewServer or httptest.NewTLSServer -- both accept connections before
// they return -- or at `<name>.Start()` on a server bound from
// NewUnstartedServer, whether the call is a statement of its own or the body of
// a `go` statement. That includes the literal handed straight to the
// constructor -- `NewServer(http.HandlerFunc(func(...){...}))` -- which is
// registered before the constructor returns and therefore reachable the moment
// it does; a literal in a later statement is not, and is left alone.
//
// A read is matched to the assignment it actually resolves to, not to any
// assignment of the same spelling. A short declaration after the read shadows
// an outer value of that name rather than writing the one the handler reads,
// so it is not a finding; only a declaration the handler's own read is in scope
// for counts.
//
// What it cannot cover, and does not claim to: a handler reached indirectly
// through a variable or a helper, a binding reached through an alias rather
// than the name bound at the server's own declaration, a captured value written
// from another function or goroutine, a variable used as a map-literal key, and
// a write through an index or a field selector rather than to the name itself.
// Those are false negatives, and the first three are why this is a floor rather
// than a proof; none of them is a shape this module has had.
//
// The scope is the module's own source, test and helper alike, and it reads
// .go files rather than only what is compiled into the test binary, so it must
// run uncached: scripts/integration-test.sh already runs this module with
// -count=1.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubOrderingHit is one handler literal that reads a value its own stub
// assigns only once the server is already serving. The location is carried as
// a path relative to the scan root rather than as token.Position, so the same
// hit reads identically from a local checkout and from the image test stage's
// own /src tree.
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

// handlerRegisteringCalls names the calls a func literal is passed to when it
// is meant to serve HTTP. Restricting the scan to these keeps the check from
// flagging an ordinary closure the enclosing function happens to call after
// the server is up: that call is ordered by the function's own body and is not
// what this gate is about.
var handlerRegisteringCalls = map[string]bool{
	"Handle":      true,
	"HandleFunc":  true,
	"HandlerFunc": true,
}

// handlerRegistration returns the func literals a call registers as handlers:
// mux.Handle/mux.HandleFunc on any receiver, or http.HandlerFunc. The receiver
// is not checked against a mux type -- an aliased import would defeat that --
// so a same-named method on some other value would be inspected as a handler,
// which in this module only ever means reviewing a false positive rather than
// missing one.
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

// stubScope is one function body's own contribution to the ordering question:
// where it begins serving, which names it assigns where, and which handler
// literals it registers. Nested function literals are scopes of their own, so
// the walk below does not cross into them.
type stubScope struct {
	servingPos  token.Pos // token.NoPos when this scope never begins serving
	servingLine int
	servings    []servingSpan
	handlers    []*ast.FuncLit
	writes      map[string][]scopeWrite
}

// scopeWrite is one assignment in a scope. define marks a short variable
// declaration, which -- unlike a plain assignment -- introduces a binding whose
// scope starts at that statement, so a read positioned before it resolves to a
// different variable of the same name.
type scopeWrite struct {
	pos    token.Pos
	line   int
	define bool
}

// servingSpan is one construct that begins serving, as an interval of source
// positions. The interval is what tells a handler literal that *is* the arg of
// the serving call -- `NewServer(http.HandlerFunc(func(...){...}))`, registered
// before the constructor returns and reachable the moment it does -- apart
// from a handler registered in a later statement, which cannot have been
// running yet.
type servingSpan struct {
	pos token.Pos // start of the serving expression, for write comparison
	end token.Pos
}

func (s *stubScope) markServing(pos, end token.Pos, line int) {
	// Keep the earliest serving site: it is the one bounding which handlers
	// can already be running.
	if s.servingPos == token.NoPos || pos < s.servingPos {
		s.servingPos, s.servingLine = pos, line
	}
	s.servings = append(s.servings, servingSpan{pos: pos, end: end})
}

// insideServing reports whether lit is contained in one of this scope's
// serving constructs. Such a literal is the handler the server starts serving
// with, so it is running-eligible as soon as the constructor returns -- the
// opposite of the "registered after serving began" shape a later statement
// has, and the reason it is not skipped by position alone.
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

// writesAtOrAfter returns the line of the first assignment to name at or after
// pos: the write with no happens-before edge to a handler reading it. A write
// recorded at the serving site's own position is the assignment that began
// serving -- `server := httptest.NewServer(mux)` binds server and starts
// serving in one statement -- so it counts.
//
// readPos is where the handler reads the name, and it distinguishes the write
// the handler's read actually resolves to from a same-named one it does not:
// matching on the name alone reds correct code the moment a short declaration
// shadows an outer value of that name after the server is already serving.
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

// shadowedFromRead reports whether a short variable declaration of name starts
// a new binding strictly between the handler's read and writePos. When one
// does, writePos assigns that newer binding while the read still resolves to
// whatever binding was in scope at its own position -- typically an outer one,
// as in a package-level value shadowed by a local declared after serving began.
//
// The declaration that began serving is exempt: NewServer assigns the variable
// it is bound to on its way out while the handler it was handed is already
// runnable, which is the shape this gate exists for.
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

// collectScopeFacts walks one function body without crossing into nested
// function literals.
func collectScopeFacts(fset *token.FileSet, body *ast.BlockStmt) *stubScope {
	scope := &stubScope{writes: map[string][]scopeWrite{}}
	if body == nil {
		return scope
	}
	line := func(pos token.Pos) int { return fset.Position(pos).Line }
	// Names bound from httptest.NewUnstartedServer, so that a later
	// `<name>.Start()` is recognised as the moment serving begins.
	unstarted := map[string]bool{}

	ast.Inspect(body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.FuncLit:
			// A nested literal is its own scope: its writes are its own, and
			// its handlers are registered when that literal is analysed.
			return false
		case *ast.CallExpr:
			// Visited before its own children, so a handler literal is seen
			// here and then skipped by the FuncLit case above.
			scope.handlers = append(scope.handlers, handlerRegistration(node)...)
		case *ast.AssignStmt:
			names := assignedNames(node.Lhs)
			for _, name := range names {
				scope.recordWrite(name, node.Pos(), line(node.Pos()), node.Tok == token.DEFINE)
			}
			if serving := newServerCall(node.Rhs); serving != nil {
				scope.markServing(node.Pos(), serving.End(), line(node.Pos()))
			}
			for i, name := range names {
				if i < len(node.Rhs) && isNewUnstartedServerCall(node.Rhs[i]) {
					unstarted[name] = true
				}
			}
		case *ast.ExprStmt:
			if call, ok := node.X.(*ast.CallExpr); ok && isStartCall(call, unstarted) {
				scope.markServing(node.Pos(), node.End(), line(node.Pos()))
			}
		case *ast.GoStmt:
			// `go server.Start()` begins serving from this point too: the same
			// call, deferred to a goroutine. A value assigned after it can be
			// read by a handler that is already running, so the statement
			// wrapper being different is not a reason to skip the scope.
			if isStartCall(node.Call, unstarted) {
				scope.markServing(node.Pos(), node.End(), line(node.Pos()))
			}
		}
		return true
	})
	return scope
}

// assignedNames returns the plain identifiers on an assignment's left side,
// skipping index expressions, selectors and blank identifiers.
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

// newServerCall returns the httptest.NewServer call in an assignment's right
// side, if any. Keying on the selector's own name rather than on an import
// alias means a file importing the package under another name is still caught;
// the cost is that an unrelated NewServer method on some other value would be
// too, and none exists in this module.
func newServerCall(rhs []ast.Expr) *ast.CallExpr {
	for _, expr := range rhs {
		if call, ok := expr.(*ast.CallExpr); ok && isNewServerCall(call) {
			return call
		}
	}
	return nil
}

// servingConstructors names the httptest constructors that begin serving before
// they return. NewTLSServer belongs beside NewServer rather than in a separate
// case: it is NewUnstartedServer followed by StartTLS, so the server it hands
// back is already accepting connections and the ordering hazard is identical.
var servingConstructors = map[string]bool{
	"NewServer":    true,
	"NewTLSServer": true,
}

func isNewServerCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	return ok && servingConstructors[sel.Sel.Name]
}

// isStartCall reports whether call is `<unstarted>.Start()`.
func isStartCall(call *ast.CallExpr, unstarted map[string]bool) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Start" {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	return ok && unstarted[ident.Name]
}

// handlerRead is one identifier a handler literal reads: its own position, so
// the read can be resolved against the declarations that do and do not bind it,
// and the line that gets reported when it cannot.
type handlerRead struct {
	pos  token.Pos
	line int
}

// handlerReads returns the names a handler literal reads, excluding the names
// it binds itself. A selector's field or method name is not a read, and
// neither is a composite literal's key: the false negative that leaves is a
// map literal keyed by a variable, which no stub in this module writes.
func handlerReads(fset *token.FileSet, lit *ast.FuncLit) map[string]handlerRead {
	bound := map[string]bool{}
	notARead := map[token.Pos]bool{}
	ast.Inspect(lit, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.AssignStmt:
			if node.Tok == token.DEFINE {
				for _, name := range assignedNames(node.Lhs) {
					bound[name] = true
				}
			}
		case *ast.ValueSpec:
			for _, ident := range node.Names {
				bound[ident.Name] = true
			}
		case *ast.Field:
			for _, ident := range node.Names {
				bound[ident.Name] = true
			}
		case *ast.RangeStmt:
			if ident, ok := node.Key.(*ast.Ident); ok {
				bound[ident.Name] = true
			}
			if ident, ok := node.Value.(*ast.Ident); ok {
				bound[ident.Name] = true
			}
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

// stubOrderingViolationsInFile analyses every function scope in one parsed
// file: each top-level function body and each function literal body is a scope
// with its own assignments and its own serving sites, so a stub helper is
// judged on its own body rather than against whatever encloses it.
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
			// A handler registered at or after the serving site cannot have
			// been running before it -- unless the literal *is* an argument of
			// the construct that began serving. That one is not registered
			// afterwards; it is what the server starts with, so it can run
			// from the moment the constructor returns and any value the
			// constructor assigns is unordered against it.
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

// findStubOrderingViolations parses every .go file under root -- production,
// test, and helper alike -- and reports each handler reading a value its own
// stub assigns only once serving has begun.
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

// skipDirForStubScan mirrors skipDirForExecScan's exclusions: no hand-written
// Go source lives in version control, installed JS dependencies, generated
// trees, or captured golden fixtures.
func skipDirForStubScan(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", "dist", "wailsjs", ".claude", ".claude-plugin", ".vscode", "testdata", "coverage":
		return true
	default:
		return false
	}
}

// TestFixtureStubsAssignBeforeServing fails when a fixture stub's handler
// reads a value the stub assigns only after it began serving, so the ordering
// cannot come back through a stub that no `-race` gate would notice.
func TestFixtureStubsAssignBeforeServing(t *testing.T) {
	t.Parallel()
	for _, hit := range findStubOrderingViolations(t, harnessModuleRoot(t)) {
		t.Errorf("%s", hit.Message())
	}
}

// TestStubOrderingScannerFixtures locks the scanner's scope: the shapes it
// must catch, and the legitimate ones it must leave alone. A gate that flagged
// the fixed form would be turned off rather than satisfied, so the negative
// cases matter as much as the positive ones. Every fixture is written into a
// temp directory rather than committed source.
func TestStubOrderingScannerFixtures(t *testing.T) {
	t.Parallel()
	fixtures := map[string]struct {
		src   string
		names []string // the read names the scanner must report, in any order
	}{
		"reads_the_server_assigned_by_newserver": {
			src: `package fixture

import (
	"net/http"
	"net/http/httptest"
)

func stub(t testing.TB) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/platform", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(server.URL))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}
`,
			names: []string{"server"},
		},
		"reads_a_value_assigned_after_newserver": {
			src: `package fixture

import (
	"net/http"
	"net/http/httptest"
)

func stub(t testing.TB) *httptest.Server {
	var issuer string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/platform", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(issuer))
	})
	server := httptest.NewServer(mux)
	issuer = server.URL
	t.Cleanup(server.Close)
	return server
}
`,
			names: []string{"issuer"},
		},
		"handler_is_the_constructor_argument_reading_the_constructor_binding": {
			// The literal is not registered after the constructor returned; it
			// is what the server starts with, so the binding NewServer performs
			// on its way out is unordered against the handler's read of it.
			src: `package fixture

import (
	"net/http"
	"net/http/httptest"
)

func stub(t testing.TB) *httptest.Server {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(server.URL))
	}))
	t.Cleanup(server.Close)
	return server
}
`,
			names: []string{"server"},
		},
		"handler_is_the_constructor_argument_reading_a_later_assignment": {
			src: `package fixture

import (
	"net/http"
	"net/http/httptest"
)

func stub(t testing.TB) *httptest.Server {
	var issuer string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(issuer))
	}))
	issuer = server.URL
	t.Cleanup(server.Close)
	return server
}
`,
			names: []string{"issuer"},
		},
		"handler_is_the_constructor_argument_reading_only_prior_values": {
			src: `package fixture

import (
	"net/http"
	"net/http/httptest"
)

func stub(t testing.TB) *httptest.Server {
	issuer := "http://example.invalid"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(issuer))
	}))
	t.Cleanup(server.Close)
	return server
}
`,
			names: nil,
		},
		"assigns_before_start": {
			src: `package fixture

import (
	"net/http"
	"net/http/httptest"
)

func stub(t testing.TB) *httptest.Server {
	var issuer string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/platform", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(issuer))
	})
	server := httptest.NewUnstartedServer(mux)
	issuer = "http://" + server.Listener.Addr().String()
	server.Start()
	t.Cleanup(server.Close)
	return server
}
`,
			names: nil,
		},
		"ordinary_closure_called_after_the_server_is_up": {
			src: `package fixture

import (
	"net/http"
	"net/http/httptest"
)

func stub(t testing.TB) *httptest.Server {
	report := func() string { return server.URL }
	server := httptest.NewServer(http.NewServeMux())
	t.Cleanup(server.Close)
	_ = report()
	return server
}
`,
			names: nil,
		},
		"handler_registered_after_serving_begins": {
			src: `package fixture

import (
	"net/http"
	"net/http/httptest"
)

func stub(t testing.TB) *httptest.Server {
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	mux.HandleFunc("GET /v1/platform", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(server.URL))
	})
	t.Cleanup(server.Close)
	return server
}
`,
			names: nil,
		},
		"reads_the_server_assigned_by_newtlsserver": {
			// NewTLSServer serves before it returns exactly as NewServer does:
			// it is NewUnstartedServer followed by StartTLS, so the returned
			// server is already accepting connections while the binding it
			// hands back -- and every value assigned after it -- has no
			// happens-before edge to a handler it was given.
			src: `package fixture

import (
	"net/http"
	"net/http/httptest"
)

func stub(t testing.TB) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/platform", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(server.URL))
	})
	server := httptest.NewTLSServer(mux)
	t.Cleanup(server.Close)
	return server
}
`,
			names: []string{"server"},
		},
		"tls_server_handler_reads_only_prior_values": {
			// Widening the serving constructors to NewTLSServer must not turn a
			// correctly ordered TLS stub into a finding.
			src: `package fixture

import (
	"net/http"
	"net/http/httptest"
)

func stub(t testing.TB) *httptest.Server {
	issuer := "http://example.invalid"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(issuer))
	}))
	t.Cleanup(server.Close)
	return server
}
`,
			names: nil,
		},
		"started_from_a_go_statement": {
			// `go server.Start()` begins serving too. The call is the same one
			// the ExprStmt case recognises; only the statement wrapper differs,
			// so a scope that starts its stub this way must not be skipped.
			src: `package fixture

import (
	"net/http"
	"net/http/httptest"
)

func stub(t testing.TB) *httptest.Server {
	var issuer string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/platform", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(issuer))
	})
	server := httptest.NewUnstartedServer(mux)
	go server.Start()
	issuer = server.URL
	t.Cleanup(server.Close)
	return server
}
`,
			names: []string{"issuer"},
		},
		"handler_reads_a_package_level_value_shadowed_later": {
			// The handler's `issuer` is the package-level one, which is never
			// written after serving begins. The local declared underneath is a
			// different binding that begins after both the handler's read and
			// the serving site, so matching the two by name alone would red
			// correct code.
			src: `package fixture

import (
	"net/http"
	"net/http/httptest"
)

var issuer = "http://example.invalid"

func stub(t testing.TB) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/platform", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(issuer))
	})
	server := httptest.NewServer(mux)
	issuer := server.URL
	_ = issuer
	t.Cleanup(server.Close)
	return server
}
`,
			names: nil,
		},
		"local_declared_before_the_handler_still_counts": {
			// The shadow exemption must not be a general amnesty for short
			// declarations: this one is in scope at the handler's read, so the
			// later assignment to it is a genuine finding.
			src: `package fixture

import (
	"net/http"
	"net/http/httptest"
)

func stub(t testing.TB) *httptest.Server {
	mux := http.NewServeMux()
	issuer := "http://example.invalid"
	server := httptest.NewUnstartedServer(mux)
	mux.HandleFunc("GET /v1/platform", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(issuer))
	})
	server.Start()
	issuer = server.URL
	t.Cleanup(server.Close)
	return server
}
`,
			names: []string{"issuer"},
		},
	}

	for name, fixture := range fixtures {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "fixture_test.go")
			if err := os.WriteFile(path, []byte(fixture.src), 0o644); err != nil {
				t.Fatalf("write fixture: %v", err)
			}
			var got []string
			for _, hit := range findStubOrderingViolations(t, dir) {
				got = append(got, hit.name)
			}
			if len(got) != len(fixture.names) {
				t.Fatalf("scanner reported %v, want the names %v", got, fixture.names)
			}
			for _, want := range fixture.names {
				found := false
				for _, name := range got {
					if name == want {
						found = true
					}
				}
				if !found {
					t.Fatalf("scanner reported %v, want %v", got, fixture.names)
				}
			}
		})
	}
}
