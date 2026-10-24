package integration

// Cmd.Wait waits for the stdout/stderr drain, which a grandchild holding the pipe
// keeps open after the child exits; WaitDelay bounds that. internal/harnessexec is
// the only constructor that arms it, and this gate refuses any other, with no baseline.
// It reads source files, so it must run uncached (-count=1).

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// blessedExecPackage is the one directory allowed to construct an *exec.Cmd.
var blessedExecPackage = filepath.Join("internal", "harnessexec")

// bareExecConstructors are refused; a context alone does not arm WaitDelay.
var bareExecConstructors = map[string]bool{
	"Command":        true,
	"CommandContext": true,
}

func skipDirForExecScan(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", "dist", "wailsjs", ".claude", ".claude-plugin", ".vscode", "testdata", "coverage":
		return true
	default:
		return false
	}
}

// bareExecHit is one direct *exec.Cmd construction, located relative to the scan
// root so hits read the same locally and in the image's /src tree.
type bareExecHit struct {
	file string // path relative to the scan root, forward-slash separated
	line int
	col  int
	call string // e.g. "exec.Command", "osexec.CommandContext", "Command"
}

func (h bareExecHit) position() string {
	return fmt.Sprintf("%s:%d:%d", h.file, h.line, h.col)
}

func (h bareExecHit) Message() string {
	return fmt.Sprintf("%s: %s builds a child process directly -- build every harness child with harnessexec.Command or harnessexec.CommandContext instead, the one constructor that arms the bound keeping a descendant's held pipe from blocking Wait forever (erun-integration/AGENTS.md § \"Subprocess bounds\")", h.position(), h.call)
}

// execImportNames returns the names a file binds "os/exec" to, keyed on the import
// path so aliases count and unrelated exec packages do not.
func execImportNames(file *ast.File) (names map[string]bool, dotImported bool) {
	names = map[string]bool{}
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil || path != "os/exec" {
			continue
		}
		switch {
		case imp.Name == nil:
			names["exec"] = true
		case imp.Name.Name == "_":
		case imp.Name.Name == ".":
			dotImported = true
		default:
			names[imp.Name.Name] = true
		}
	}
	return names, dotImported
}

// findBareExecConstructors returns one hit per os/exec constructor call under root
// outside blessedExecPackage, stub binaries included.
func findBareExecConstructors(t testing.TB, root string) []bareExecHit {
	t.Helper()
	var hits []bareExecHit
	fset := token.NewFileSet()
	blessed := filepath.Join(root, blessedExecPackage)
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return execScanDirVerdict(root, blessed, path, d)
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

		hits = append(hits, bareExecHitsInFile(fset, file, rel)...)
		return nil
	})
	if err != nil {
		t.Fatalf("scan harness for bare exec constructors: %v", err)
	}
	return hits
}

func execScanDirVerdict(root, blessed, path string, d os.DirEntry) error {
	if path != root && (skipDirForExecScan(d.Name()) || path == blessed) {
		return filepath.SkipDir
	}
	return nil
}

func bareExecHitsInFile(fset *token.FileSet, file *ast.File, rel string) []bareExecHit {
	var hits []bareExecHit
	names, dotImported := execImportNames(file)
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		position := fset.Position(call.Pos())
		switch fun := call.Fun.(type) {
		case *ast.SelectorExpr:
			ident, ok := fun.X.(*ast.Ident)
			if !ok || !names[ident.Name] || !bareExecConstructors[fun.Sel.Name] {
				return true
			}
			hits = append(hits, bareExecHit{
				file: rel, line: position.Line, col: position.Column,
				call: ident.Name + "." + fun.Sel.Name,
			})
		case *ast.Ident:
			if dotImported && bareExecConstructors[fun.Name] {
				hits = append(hits, bareExecHit{
					file: rel, line: position.Line, col: position.Column,
					call: fun.Name,
				})
			}
		}
		return true
	})
	return hits
}

// harnessModuleRoot resolves the module directory from this file's location.
func harnessModuleRoot(t testing.TB) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller could not resolve this test file's path")
	}
	return filepath.Dir(file)
}

func TestNoBareExecConstructorInHarness(t *testing.T) {
	t.Parallel()
	for _, hit := range findBareExecConstructors(t, harnessModuleRoot(t)) {
		t.Errorf("%s", hit.Message())
	}
}

// TestFindBareExecConstructorsExclusions: os/exec is used throughout for LookPath,
// ErrWaitDelay and ExitError, which must not be flagged.
func TestFindBareExecConstructorsExclusions(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	files := map[string]string{
		"default_name.go": `package fixture

import "os/exec"

func a() { _ = exec.Command("git", "status") }
`,
		"aliased.go": `package fixture

import osexec "os/exec"

func b() { _ = osexec.CommandContext(nil, "git", "status") }
`,
		"dot_import.go": `package fixture

import . "os/exec"

func c() { _ = Command("git", "status") }
`,
		"lookpath_only.go": `package fixture

import "os/exec"

func d() (string, error) { return exec.LookPath("atlas") }

var _ = exec.ErrWaitDelay
var _ = exec.ExitError{}
`,
		"other_package.go": `package fixture

import "example.com/exec"

func e() { _ = exec.Command("git", "status") }
`,
		"wired.go": `package fixture

import "github.com/sophium/erun/erun-integration/internal/harnessexec"

func f() { _ = harnessexec.Command("git", "status") }
`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	byFile := map[string]bareExecHit{}
	for _, hit := range findBareExecConstructors(t, dir) {
		byFile[hit.file] = hit
	}
	if len(byFile) != 3 {
		t.Fatalf("expected exactly three hits (default_name.go, aliased.go, dot_import.go), got %+v", byFile)
	}
	for file, wantCall := range map[string]string{
		"default_name.go": "exec.Command",
		"aliased.go":      "osexec.CommandContext",
		"dot_import.go":   "Command",
	} {
		hit, ok := byFile[file]
		if !ok {
			t.Errorf("expected %s to be caught, got no hit", file)
			continue
		}
		if hit.call != wantCall {
			t.Errorf("%s: reported call %q, want %q", file, hit.call, wantCall)
		}
	}
	for _, file := range []string{"lookpath_only.go", "other_package.go", "wired.go"} {
		if hit, ok := byFile[file]; ok {
			t.Errorf("expected %s to be allowed, got %s", file, hit.Message())
		}
	}
}

func TestBareExecConstructorMessageNamesTheReplacement(t *testing.T) {
	t.Parallel()
	msg := bareExecHit{file: "some/file.go", line: 12, col: 3, call: "exec.Command"}.Message()
	for _, want := range []string{"some/file.go:12:3", "exec.Command", "harnessexec.Command"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q does not name %q", msg, want)
		}
	}
}
