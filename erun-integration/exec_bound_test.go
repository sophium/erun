package integration

// exec_bound_test.go is a structural gate for the harness's own subprocess
// handling, in the same shape as issue_reference_test.go and
// bare_required_input_test.go: an AST walk that fails the build the next time
// the defect reappears, rather than another instruction to follow.
//
// The defect it prevents is a moved hang, not a missing feature. os/exec
// drains a child's stdout and stderr from goroutines of its own and Cmd.Wait
// waits for those goroutines rather than for the child, so any process the
// child started still holds the write end after the child is gone: the drain
// never reaches EOF and Wait blocks on a child that has already exited.
// Cmd.WaitDelay is the exact bound for that, but it has to be armed on the Cmd
// that is actually waited on. A single patched call site left every other one
// bare, so the hang simply reappeared on the next test the gate ran -- twice,
// on a different pair of tests each time.
//
// internal/harnessexec is now the only constructor, and this gate is what
// keeps it that way: a bare exec.Command or exec.CommandContext anywhere else
// in the module fails, naming the call to use instead. It is deliberately a
// zero-tolerance check with no baseline, in the shape of the role-
// classification gate: the module was fully converted in the change that added
// this, so there is no pre-existing debt to carry, and a baseline over a
// two-word call form would only invite entries to be added to it.
//
// This test reads .go files under the module rather than only what is
// compiled into it, so it must run uncached: scripts/integration-test.sh
// already runs this module with -count=1. The whole module is COPYed into the
// image test stage, so the scanned input is present wherever the gate runs.

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
// It is a directory rather than a file list so the package can grow helpers
// around its constructor without this gate having to be taught each one; the
// guarantee that matters is that the constructor is reachable in exactly one
// place, and exec-bound_test.go's own unit tests pin what it arms.
var blessedExecPackage = filepath.Join("internal", "harnessexec")

// bareExecConstructor is the call this gate refuses. CommandContext is listed
// beside Command because it is the shape a call site reaches for once it
// believes it has bounded something: it arms a context, but a context alone
// does not arm WaitDelay, so the Cmd it builds still waits on a descendant's
// held pipe for as long as that descendant lives.
var bareExecConstructors = map[string]bool{
	"Command":        true,
	"CommandContext": true,
}

// skipDirForExecScan mirrors skipDirForIssueReferenceScan's exclusions: no
// hand-written Go source lives in version control, installed JS dependencies,
// generated trees, or captured golden fixtures.
func skipDirForExecScan(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", "dist", "wailsjs", ".claude", ".claude-plugin", ".vscode", "testdata", "coverage":
		return true
	default:
		return false
	}
}

// bareExecHit is one direct construction of an *exec.Cmd found in harness Go
// source. The location is carried as a path relative to the scan root rather
// than as token.Position, so the same hit reads identically from a local
// checkout and from the image test stage's own /src tree.
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

// execImportNames returns the local names a file binds "os/exec" to, and
// whether it dot-imports the package. Keying on the import path rather than
// the identifier means an unrelated package that happens to be named exec is
// not a hit, and an alias the file chose for itself still is.
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
			// A blank import cannot name a constructor.
		case imp.Name.Name == ".":
			dotImported = true
		default:
			names[imp.Name.Name] = true
		}
	}
	return names, dotImported
}

// findBareExecConstructors parses every .go file under root -- production,
// test, and the stub binaries alike, since all of them run inside the same
// test binary or are spawned by it -- and returns one hit per direct call to
// an os/exec constructor outside blessedExecPackage.
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
			if path != root && (skipDirForExecScan(d.Name()) || path == blessed) {
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
		return nil
	})
	if err != nil {
		t.Fatalf("scan harness for bare exec constructors: %v", err)
	}
	return hits
}

// harnessModuleRoot resolves this module's directory from this file's own
// location rather than the working directory `go test` happens to run from,
// so the gate scans the same tree however it was invoked.
func harnessModuleRoot(t testing.TB) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller could not resolve this test file's path")
	}
	// This file lives at <moduleRoot>/exec_bound_test.go.
	return filepath.Dir(file)
}

// TestNoBareExecConstructorInHarness fails when any file in the module builds
// an *exec.Cmd itself instead of going through internal/harnessexec, so the
// unbounded wait cannot come back through a call site that was never
// converted -- the shape that made this defect recur twice.
func TestNoBareExecConstructorInHarness(t *testing.T) {
	t.Parallel()
	for _, hit := range findBareExecConstructors(t, harnessModuleRoot(t)) {
		t.Errorf("%s", hit.Message())
	}
}

// TestFindBareExecConstructorsExclusions locks the scope this scanner must
// respect. The legitimate cases matter as much as the caught ones: os/exec is
// imported throughout the harness for LookPath, ErrWaitDelay and ExitError,
// and a gate that flagged those would be turned off rather than satisfied.
// Every fixture below is written into a temp directory rather than committed
// source.
func TestFindBareExecConstructorsExclusions(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	files := map[string]string{
		// Caught: the default import name, the alias a file may choose, and a
		// dot import, for both constructors.
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
		// Not caught: os/exec used for anything other than a constructor.
		"lookpath_only.go": `package fixture

import "os/exec"

func d() (string, error) { return exec.LookPath("atlas") }

var _ = exec.ErrWaitDelay
var _ = exec.ExitError{}
`,
		// Not caught: a different package that happens to be named exec. Keying
		// on the import path rather than the identifier is what separates it
		// from the aliased.go hit above.
		"other_package.go": `package fixture

import "example.com/exec"

func e() { _ = exec.Command("git", "status") }
`,
		// Not caught: calling through the blessed constructor, whichever
		// package the caller imported it from.
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

// TestBareExecConstructorMessageNamesTheReplacement keeps the failure
// actionable rather than merely prohibitive: a gate that says only "do not
// call exec.Command" leaves the next author to find the constructor, which is
// how a one-off exception gets argued for instead of a conversion.
func TestBareExecConstructorMessageNamesTheReplacement(t *testing.T) {
	t.Parallel()
	msg := bareExecHit{file: "some/file.go", line: 12, col: 3, call: "exec.Command"}.Message()
	for _, want := range []string{"some/file.go:12:3", "exec.Command", "harnessexec.Command"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q does not name %q", msg, want)
		}
	}
}
