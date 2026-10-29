package fingerprint

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The erun-blueprint-api templates ship some repository methods as TODO stubs
// for the scaffolding agent to implement, and the default wiring calls each
// one. A stub must fail rather than report success, or an unimplemented one
// goes unnoticed: an audit stub returning nil records and logs nothing. The
// docs must name every stub, or nothing tells the agent why a fresh scaffold
// refuses every token.
var blueprintAPIRepositoryStubs = map[string]struct {
	// denies is true where the stub fails closed by returning false;
	// otherwise it must return a non-nil error.
	denies bool
}{
	"IdentityRepository.Resolve":         {denies: false},
	"PermissionAuthorizer.IsAllowed":     {denies: true},
	"AuditEventRepository.WriteAPIEvent": {denies: false},
}

const blueprintAPIStubMarker = "TODO: implement"

var blueprintAPISkillDir = filepath.Join("erun-skills", "skills", "erun-blueprint-api")

// blueprintAPITemplateStubs returns, for every method in the templates whose
// body carries the stub marker, the statement its body ends with.
func blueprintAPITemplateStubs(t *testing.T) map[string]ast.Stmt {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(erunRepoRoot(t), blueprintAPISkillDir, "templates", "*.go.tmpl"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no Go templates under %s (err %v)", blueprintAPISkillDir, err)
	}
	stubs := map[string]ast.Stmt{}
	for _, path := range paths {
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && isStubMethod(file, fn) {
				stubs[receiverTypeName(fn)+"."+fn.Name.Name] = fn.Body.List[len(fn.Body.List)-1]
			}
		}
	}
	return stubs
}

func isStubMethod(file *ast.File, fn *ast.FuncDecl) bool {
	if fn.Recv == nil || fn.Body == nil || len(fn.Body.List) == 0 {
		return false
	}
	for _, group := range file.Comments {
		if group.Pos() > fn.Body.Lbrace && group.End() < fn.Body.Rbrace && strings.Contains(group.Text(), blueprintAPIStubMarker) {
			return true
		}
	}
	return false
}

func receiverTypeName(fn *ast.FuncDecl) string {
	recv := fn.Recv.List[0].Type
	if star, ok := recv.(*ast.StarExpr); ok {
		recv = star.X
	}
	if ident, ok := recv.(*ast.Ident); ok {
		return ident.Name
	}
	return ""
}

func isIdent(expr ast.Expr, name string) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == name
}

func sortedStubNames(stubs map[string]ast.Stmt) []string {
	names := make([]string, 0, len(stubs))
	for name := range stubs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func TestBlueprintAPIRepositoryStubsFailClosed(t *testing.T) {
	stubs := blueprintAPITemplateStubs(t)
	for name := range blueprintAPIRepositoryStubs {
		if _, ok := stubs[name]; !ok {
			t.Errorf("%s is no longer a %q stub in the erun-blueprint-api templates; drop it from blueprintAPIRepositoryStubs and from the docs that tell the agent to implement it", name, blueprintAPIStubMarker)
		}
	}
	for _, name := range sortedStubNames(stubs) {
		want, known := blueprintAPIRepositoryStubs[name]
		if !known {
			t.Errorf("%s is a %q stub that blueprintAPIRepositoryStubs does not list; add it, and name it where SKILL.md, the scaffold's AGENTS.md and skills-spec.md name the others", name, blueprintAPIStubMarker)
			continue
		}
		if failure := stubReportsSuccess(stubs[name], want.denies); failure != "" {
			t.Errorf("%s: %s", name, failure)
		}
	}
}

// stubReportsSuccess returns why a stub's final statement does not fail
// closed, or "" when it does.
func stubReportsSuccess(last ast.Stmt, denies bool) string {
	ret, ok := last.(*ast.ReturnStmt)
	switch {
	case !ok || len(ret.Results) == 0:
		return "the stub must end in a return that fails closed"
	case denies && !isIdent(ret.Results[0], "false"):
		return "the stub must deny (return false) until it is implemented"
	case !denies && isIdent(ret.Results[len(ret.Results)-1], "nil"):
		return "the stub returns a nil error, so the scaffold reports success for work it never did; return an error until it is implemented"
	}
	return ""
}

func TestBlueprintAPIDocsNameEveryRepositoryStub(t *testing.T) {
	root := erunRepoRoot(t)
	read := func(rel string) string {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		return string(data)
	}
	spec := read(filepath.Join("erun-docs", "docs", "agent-reference", "skills-spec.md"))
	start := strings.Index(spec, "### `erun-blueprint-api`")
	if start < 0 {
		t.Fatal("skills-spec.md has no erun-blueprint-api section")
	}
	section := spec[start:]
	if end := strings.Index(section[1:], "\n### "); end >= 0 {
		section = section[:end+1]
	}
	docs := map[string]string{
		filepath.Join(blueprintAPISkillDir, "SKILL.md"):                    read(filepath.Join(blueprintAPISkillDir, "SKILL.md")),
		filepath.Join(blueprintAPISkillDir, "templates", "AGENTS.md.tmpl"): read(filepath.Join(blueprintAPISkillDir, "templates", "AGENTS.md.tmpl")),
		"skills-spec.md's erun-blueprint-api section":                      section,
	}
	for _, name := range sortedStubNames(blueprintAPITemplateStubs(t)) {
		for doc, text := range docs {
			if !strings.Contains(text, "`"+name+"`") {
				t.Errorf("%s does not name the %s stub; the scaffold does not work until it is implemented, and the docs must say so", doc, name)
			}
		}
	}
}
