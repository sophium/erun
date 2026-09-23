package eruncommon

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// This file owns the Go-test half of the erun-devops COPY contract enforced by
// dockerfile_copy_contract_test.go's
// TestCheckGateGoTestsReadOnlyRepoRootPathsTheDevopsImageProvides. The sibling
// half of that guard reads shell scripts and can only see what a script
// resolves through ${script_dir}/../..; a Go test reading a repo-root file goes
// through a runtime.Caller-derived helper instead, and is invisible to any
// shell-script scan. This file parses those tests to find the reads.
//
// The parse is deliberately conservative in one direction only: a read it can
// resolve must be provided by the image, and a read it cannot resolve is
// reported rather than skipped, so a site this scan cannot see is never quietly
// counted as satisfied.
//
// What it sees, so a reader can tell what it does not:
//
//   - a filepath.Join rooted at a runtime.Caller-derived helper, called
//     directly or through one assigned local variable;
//   - a filepath.Join whose first argument is a literal that climbs out of the
//     file's own package directory (filepath.Join("..", "Formula", "erun.rb")),
//     which is what `go test`'s working directory makes a repository-root read;
//   - either shape inside a function body or in a package-level declaration.
//
// What it does not see is enumerated in the limits recorded on
// TestCheckGateGoTestsReadOnlyRepoRootPathsTheDevopsImageProvides, and the shape
// it most commonly misses -- a path that arrives as a function parameter -- is
// pinned by a test so the boundary is checked rather than asserted.

// gateGoTestModuleDirs lists the modules whose `go test` runs inside the
// erun-devops image test stage -- the Go half of what `make check-gate`
// covers. Every entry must also be named by a check-gate prerequisite recipe,
// which gateGoTestModuleDirsInMakefile checks; the one asymmetry is
// erun-integration, whose `go test ./...` lives inside
// erun-integration/scripts/integration-test.sh rather than in the Makefile's
// own recipe (integration-test-gate invokes that script).
var gateGoTestModuleDirs = []string{
	"erun-common",
	"erun-mcp",
	"erun-ui",
	"erun-backend/erun-backend-api",
	"erun-devops/dns01-webhook",
	"erun-integration",
}

// gateGoTestRepoRootHelperNames is the canary for this file's structural
// helper detection, not a coverage list: helpers are found by shape (a
// function that derives a path from runtime.Caller and walks up as many
// directories as separate its own file from the repository root), so a new one
// under a new name is picked up without being registered here. Naming the
// known ones means a rename or a changed return shape reds this check instead
// of leaving it to scan a tree it can no longer understand and report success
// for having found nothing.
var gateGoTestRepoRootHelperNames = []string{
	"repoRoot",
	"repoRootForDockerignoreTest",
	"repoRootForDockerPlatformsProjectConfigTest",
	"repoRootForOverviewDocTest",
}

// gateGoTestReadExemptionKey identifies one read site that needs a recorded
// reason. The expression text rather than a line number is the key on purpose:
// an edit above the site must not break the registration, while a genuinely
// different read in the same file still arrives as an unregistered site.
type gateGoTestReadExemptionKey struct {
	file string
	expr string
}

// goTestRepoRootReadExemptions records every repo-root read in the gate's Go
// tests that this check cannot settle on its own, with the reason. There are
// two kinds, and they are different claims:
//
//   - the path does not resolve statically (a loop variable, a struct field, a
//     conversion), so no COPY can be matched against it here;
//   - the path resolves, but the image does not provide it and absence is
//     tolerated by that read -- recorded because a read that silently narrows
//     to nothing inside the image is still worth a reviewer's attention, even
//     where it does not fail the gate.
//
// An entry that matches no site fails the check, so a fix that makes one of
// these reads literal (or a deletion of the read) cannot leave a stale
// assertion behind.
var goTestRepoRootReadExemptions = map[gateGoTestReadExemptionKey]string{
	// script is a token checkGateShellScripts already resolved against the
	// checkout, so the paths are the Makefile's own, and the sibling half of
	// this guard checks both things a script needs from the image: that the
	// stage COPYs the script file the recipe invokes, and that it COPYs the
	// directory the script resolves its repository root through.
	{"erun-common/dockerfile_copy_contract_test.go", "script"}: "script ranges over the shell scripts the Makefile's check-gate targets name, " +
		"each already confirmed to exist in the checkout by checkGateShellScripts; the shell-script half of this guard " +
		"reads each one and checks the erun-devops test stage provides both the script file itself and every directory " +
		"it resolves its repository root through",

	// mirror.path is the path column of a two-row table in the same file
	// (chartServicePath, devopsDockerfile); both rows are COPYd by the test
	// stage, but a field read off a range variable is not a path this scan can
	// resolve without evaluating the table.
	{"erun-common/runtime_dind_default_mirrors_test.go", "mirror.path"}: "mirror.path reads the path column of a two-row table over " +
		"erun-devops/k8s/erun-devops/templates/service.yaml and erun-devops/docker/erun-devops/Dockerfile, both provided by this " +
		"stage's COPYs; the field is a range variable rather than a literal, so the scan cannot resolve it",

	// erun-devops/terraform-erun is genuinely absent from the image. The read
	// is not fatal there: discoverModuleReferencedImageNames returns no names
	// for a missing tree, and anonymousPullabilityBaseline is empty, so the
	// test narrows to nothing inside the image instead of failing. Recorded
	// rather than fixed because copying the whole Terraform tree into a Go
	// test stage buys nothing for a check that is vacuous without a populated
	// baseline either way.
	{"erun-common/release_anonymous_pullability_test.go", `"erun-devops", "terraform-erun"`}: "the image does not provide erun-devops/terraform-erun, and this " +
		"read tolerates that: discoverModuleReferencedImageNames returns no names for a missing tree and anonymousPullabilityBaseline is empty, " +
		"so the test is vacuous rather than red inside the image",

	// Both of these walk a baseline map's keys, which are repo-relative Go
	// source paths rather than a literal list. Their absence handling is
	// deliberate and documented at each site: a narrowed build context
	// legitimately omits files a full checkout has, and neither read
	// distinguishes the image's absence from a deleted file.
	{"erun-integration/bare_required_input_test.go", "filepath.FromSlash(...)"}: "the path is a key of bareRequiredInputBaseline, not a literal; the site " +
		"stats it and skips os.IsNotExist explicitly, because a narrowed build context can legitimately omit a baselined file",
	{"erun-integration/issue_reference_test.go", "filepath.FromSlash(...)"}: "the path is a key of issueReferenceBaseline, not a literal; the site " +
		"handles os.IsNotExist explicitly for the same reason as bare_required_input_test.go's",
}

// gateGoTestRead is one repo-root-rooted path read found in a gate Go test.
type gateGoTestRead struct {
	file string // repo-relative, slash-separated
	line int
	expr string // the joined arguments as written; the exemption key
	path string // resolved repo-relative path; only meaningful when literal
	// literal reports whether every joined argument was a string literal (or a
	// constant string), which is what makes path the read's real path.
	literal bool
}

func (r gateGoTestRead) exemptionKey() gateGoTestReadExemptionKey {
	return gateGoTestReadExemptionKey{file: r.file, expr: r.expr}
}

// gateGoTestReadScan is what one pass over the gate's Go tests found: the reads
// themselves, and the repo-root helpers those reads were resolved through --
// the second so the caller can tell "no test reads anything" apart from "the
// helper detection stopped working".
type gateGoTestReadScan struct {
	reads           []gateGoTestRead
	repoRootHelpers map[string]bool
}

// scanGateGoTestRepoRootReads returns every repo-root-rooted path read in the
// Go tests of the given modules, sorted by file and line.
func scanGateGoTestRepoRootReads(t *testing.T, root string, moduleDirs []string) gateGoTestReadScan {
	t.Helper()
	scan := gateGoTestReadScan{repoRootHelpers: map[string]bool{}}
	for _, module := range moduleDirs {
		scan.reads = append(scan.reads, scanGateGoTestModule(t, root, module, scan.repoRootHelpers)...)
	}
	sort.Slice(scan.reads, func(i, j int) bool {
		if scan.reads[i].file != scan.reads[j].file {
			return scan.reads[i].file < scan.reads[j].file
		}
		return scan.reads[i].line < scan.reads[j].line
	})
	return scan
}

// scanGateGoTestModule returns every repo-root read in one module's test files,
// recording the repo-root helpers it resolved them through.
func scanGateGoTestModule(t *testing.T, root, module string, repoRootHelpers map[string]bool) []gateGoTestRead {
	t.Helper()
	moduleDir := filepath.Join(root, module)
	if info, err := os.Stat(moduleDir); err != nil || !info.IsDir() {
		t.Errorf("%s is registered as a module whose Go tests run inside the erun-devops image, but it is not a "+
			"directory in this checkout -- fix the registration rather than dropping it, because a module this "+
			"scan cannot read is a module whose repo-root reads this guard cannot check", module)
		return nil
	}
	byDir, err := gateGoTestFilesByPackage(moduleDir)
	if err != nil {
		t.Fatalf("walk %s: %v", module, err)
	}
	var reads []gateGoTestRead
	for _, dir := range gateGoTestSortedKeys(byDir) {
		packageReads, helpers := collectGateGoTestReadsInPackage(t, root, dir, byDir[dir])
		reads = append(reads, packageReads...)
		for name, depth := range helpers {
			if depth == 0 {
				repoRootHelpers[name] = true
			}
		}
	}
	return reads
}

// gateGoTestFilesByPackage groups a module's *_test.go files by the package
// directory they belong to, since a helper declared in one file is called from
// its package's others.
func gateGoTestFilesByPackage(moduleDir string) (map[string][]string, error) {
	byDir := map[string][]string{}
	err := filepath.WalkDir(moduleDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if gateGoTestSkippedDirs[entry.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(entry.Name(), "_test.go") {
			byDir[filepath.Dir(path)] = append(byDir[filepath.Dir(path)], path)
		}
		return nil
	})
	return byDir, err
}

// gateGoTestSkippedDirs are directories no Go test file of a gate module lives
// in. node_modules is the one worth pruning on cost alone -- it is large,
// generated, and reachable under erun-ui's and erun-console's frontend trees --
// and the rest are pruned for the same reason as any other tool that reads Go
// sources: nothing the go tool would compile lives there.
var gateGoTestSkippedDirs = map[string]bool{
	"node_modules": true,
	"vendor":       true,
	"testdata":     true,
	".git":         true,
}

func gateGoTestSortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// sortedGateExemptionKeys returns the exemption keys in a stable order, so the
// report for stale entries does not depend on map iteration.
func sortedGateExemptionKeys(m map[gateGoTestReadExemptionKey]string) []gateGoTestReadExemptionKey {
	keys := make([]gateGoTestReadExemptionKey, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].file != keys[j].file {
			return keys[i].file < keys[j].file
		}
		return keys[i].expr < keys[j].expr
	})
	return keys
}

// collectGateGoTestReadsInPackage parses one Go package's test files and
// returns the repo-root reads in them. Helpers are collected across the whole
// package first: repoRoot is declared in one file and called from a dozen
// others, so a per-file scan would resolve almost nothing.
func collectGateGoTestReadsInPackage(t *testing.T, root, dir string, paths []string) ([]gateGoTestRead, map[string]int) {
	t.Helper()
	type parsedFile struct {
		path   string
		fset   *token.FileSet
		file   *ast.File
		depth  int
		consts map[string]string
	}
	sort.Strings(paths)
	var parsed []parsedFile
	helpers := map[string]int{}
	for _, path := range paths {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			// An unparsed file is a file this scan did not read, which is the
			// one outcome it must never treat as a pass.
			t.Errorf("parse %s: %v -- fix the parse rather than excluding the file, because a gate test this scan "+
				"cannot read is a repo-root read it cannot check", gateRepoRelativePath(root, path), err)
			continue
		}
		consts := map[string]string{}
		gateGoTestTopLevelStringConsts(file, consts)
		depth := gateGoTestDirDepth(root, path)
		parsed = append(parsed, parsedFile{path: path, fset: fset, file: file, depth: depth, consts: consts})
		collectGateGoTestPathHelpers(file, depth, helpers)
	}
	var reads []gateGoTestRead
	for _, file := range parsed {
		reads = append(reads, collectGateGoTestReadsInFile(root, file.path, file.fset, file.file, file.consts, helpers)...)
	}
	return reads, helpers
}

// gateGoTestDirDepth is how many directory components separate a file's own
// directory from the repository root.
func gateGoTestDirDepth(root, path string) int {
	rel := gateRepoRelativePath(root, path)
	dir := filepath.Dir(rel)
	if dir == "." {
		return 0
	}
	return len(strings.Split(filepath.ToSlash(dir), "/"))
}

func gateRepoRelativePath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

// collectGateGoTestPathHelpers records every function in the package that
// derives a path from its own source location, with the directory depth (from
// the repository root) that path resolves to. Depth 0 is the repository root;
// a helper one level up from a file directly under a module resolves to that
// module and is deliberately not treated as a repo root -- tracing it would
// read a temp directory or a module directory as if it were the checkout.
//
// The returned expression may reach the caller's file variable through an
// assigned local (`dir := filepath.Dir(file)`, then `return dir`), which
// gateGoTestLocalAssignments follows. Only a returned *path* is resolved: a
// helper that returns the file's contents or a concatenation is not a path
// resolver under either shape.
func collectGateGoTestPathHelpers(file *ast.File, fileDepth int, helpers map[string]int) {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil || fn.Recv != nil {
			continue
		}
		callerVar := gateGoTestCallerFileVar(fn.Body)
		if callerVar == "" {
			continue
		}
		dirs, found := gateGoTestReturnedDirDepth(fn.Body, callerVar)
		if !found {
			continue
		}
		helpers[fn.Name.Name] = fileDepth - (dirs - 1)
	}
}

// gateGoTestCallerFileVar returns the variable a runtime.Caller result is
// assigned into -- the file name is the second of its targets, as in
// `_, file, _, ok := runtime.Caller(0)`.
func gateGoTestCallerFileVar(body *ast.BlockStmt) string {
	name := ""
	ast.Inspect(body, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for _, rhs := range assign.Rhs {
			call, ok := rhs.(*ast.CallExpr)
			if !ok {
				continue
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Caller" || len(assign.Lhs) < 2 {
				continue
			}
			if id, ok := assign.Lhs[1].(*ast.Ident); ok {
				name = id.Name
			}
		}
		return true
	})
	return name
}

// gateGoTestReturnedDirDepth returns how many filepath.Dir calls wrap the
// runtime.Caller file variable in the function's return expressions. A helper
// that returns something else built from that variable (a file's contents, a
// concatenation) is not a path resolver and reports found=false.
func gateGoTestReturnedDirDepth(body *ast.BlockStmt, callerVar string) (int, bool) {
	locals := gateGoTestLocalAssignments(body)
	depth, found := 0, false
	ast.Inspect(body, func(n ast.Node) bool {
		ret, ok := n.(*ast.ReturnStmt)
		if !ok {
			return true
		}
		for _, result := range ret.Results {
			if ok, d := gateGoTestDirNesting(result, callerVar, locals, map[string]bool{}); ok {
				found = true
				if d > depth {
					depth = d
				}
			}
		}
		return true
	})
	return depth, found
}

// gateGoTestLocalAssignments records a function's single-name local
// assignments, so a helper that computes its directory into a variable and
// returns that variable is resolved like the inline form. Returning a named
// intermediate is ordinary Go, and a helper this scan failed to recognize is
// every read through it going unchecked — which is the whole failure the
// canary in gateGoTestRepoRootHelperNames exists to catch, one shape narrower.
func gateGoTestLocalAssignments(body *ast.BlockStmt) map[string]ast.Expr {
	locals := map[string]ast.Expr{}
	ast.Inspect(body, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok || len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
			return true
		}
		if id, ok := assign.Lhs[0].(*ast.Ident); ok {
			if _, seen := locals[id.Name]; !seen {
				locals[id.Name] = assign.Rhs[0]
			}
		}
		return true
	})
	return locals
}

// gateGoTestDirNesting reports how many filepath.Dir calls separate an
// expression from the runtime.Caller file variable, following local variables
// to the expression they were assigned. seen bounds that following: a
// self-referential or cyclic pair of assignments resolves to not-a-resolver
// rather than recursing forever.
func gateGoTestDirNesting(expr ast.Expr, callerVar string, locals map[string]ast.Expr, seen map[string]bool) (bool, int) {
	switch v := expr.(type) {
	case *ast.Ident:
		if v.Name == callerVar {
			return true, 0
		}
		inner, ok := locals[v.Name]
		if !ok || seen[v.Name] {
			return false, 0
		}
		seen[v.Name] = true
		return gateGoTestDirNesting(inner, callerVar, locals, seen)
	case *ast.CallExpr:
		sel, ok := v.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Dir" || len(v.Args) != 1 {
			return false, 0
		}
		inner, depth := gateGoTestDirNesting(v.Args[0], callerVar, locals, seen)
		if !inner {
			return false, 0
		}
		return true, depth + 1
	}
	return false, 0
}

// collectGateGoTestReadsInFile finds the filepath.Join calls rooted at the
// repository root, and the ones written relative to the file's own package
// directory -- `filepath.Join("..", "Formula", "erun.rb")` -- which reaches
// repository-root state without a helper to say so. Root variables are scoped to
// the function that binds them: `root` is a name tests use for temp directories
// too, and treating every one of those as the repository root would report temp
// paths as repo-root reads.
//
// Package-level declarations are walked as well as function bodies. A table of
// paths is as often a package var as a function local -- the Formula and bucket
// reads this file's CWD-relative half exists for are both in one -- and a read
// there is the same read. A package var has no local scope, so it is resolved
// against package constants alone.
func collectGateGoTestReadsInFile(
	root, path string,
	fset *token.FileSet,
	file *ast.File,
	pkgConsts map[string]string,
	helpers map[string]int,
) []gateGoTestRead {
	rel := gateRepoRelativePath(root, path)
	pkgDir := filepath.ToSlash(filepath.Dir(rel))
	var reads []gateGoTestRead
	record := func(n ast.Node, consts map[string]string, rootVars map[string]bool) {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return
		}
		if read, ok := gateGoTestJoinRead(call, fset, rel, pkgDir, consts, rootVars, helpers); ok {
			reads = append(reads, read)
		}
	}
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Body == nil {
				continue
			}
			consts, rootVars := gateGoTestConsts(pkgConsts, d), gateGoTestRootVars(d, helpers)
			ast.Inspect(d.Body, func(n ast.Node) bool {
				record(n, consts, rootVars)
				return true
			})
		case *ast.GenDecl:
			ast.Inspect(d, func(n ast.Node) bool {
				record(n, pkgConsts, nil)
				return true
			})
		}
	}
	return reads
}

// gateGoTestConsts is the constants a path in this function may be written as:
// the package's own, with the function's local ones overriding them.
func gateGoTestConsts(pkgConsts map[string]string, fn *ast.FuncDecl) map[string]string {
	consts := map[string]string{}
	for name, value := range pkgConsts {
		consts[name] = value
	}
	gateGoTestLocalStringConsts(fn, consts)
	return consts
}

// gateGoTestRootVars returns the variables this function binds to a repo-root
// helper. The binding is the whole reason a bare `root` can be trusted here:
// the same name holds temp directories elsewhere in the same file.
func gateGoTestRootVars(fn *ast.FuncDecl, helpers map[string]int) map[string]bool {
	rootVars := map[string]bool{}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, rhs := range assign.Rhs {
			if i >= len(assign.Lhs) {
				continue
			}
			call, ok := rhs.(*ast.CallExpr)
			if !ok {
				continue
			}
			id, ok := call.Fun.(*ast.Ident)
			if !ok {
				continue
			}
			if depth, ok := helpers[id.Name]; ok && depth == 0 {
				if lhs, ok := assign.Lhs[i].(*ast.Ident); ok {
					rootVars[lhs.Name] = true
				}
			}
		}
		return true
	})
	return rootVars
}

// gateGoTestJoinRead resolves one filepath.Join call to a repo-root read, when
// it is one: either rooted at the repository root, or climbing to it from the
// file's own package directory.
func gateGoTestJoinRead(
	call *ast.CallExpr,
	fset *token.FileSet,
	file, pkgDir string,
	consts map[string]string,
	rootVars map[string]bool,
	helpers map[string]int,
) (gateGoTestRead, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Join" || len(call.Args) == 0 {
		return gateGoTestRead{}, false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "filepath" {
		return gateGoTestRead{}, false
	}
	line := fset.Position(call.Pos()).Line
	if gateGoTestIsRepoRoot(call.Args[0], rootVars, helpers) {
		path, expr, literal := gateGoTestJoinArgs(call.Args[1:], consts)
		return gateGoTestRead{file: file, line: line, expr: expr, path: path, literal: literal}, true
	}
	return gateGoTestCwdRelativeRead(call, file, pkgDir, line, consts)
}

// gateGoTestCwdRelativeRead resolves a filepath.Join whose first argument is a
// literal that climbs out of the package directory, as in
// filepath.Join("..", "Formula", "erun.rb") from erun-ui. `go test` runs a
// package's test binary with its working directory set to that package's own
// source directory, so the path is relative to the file's directory and reaches
// repository-root state — a repo-root read wearing no repoRoot helper, which the
// helper-rooted half above cannot see however it is shaped.
//
// Only a ".."-leading first argument counts. One that does not climb
// (filepath.Join("relative", "repo")) names a path inside the package's own
// directory, and an absolute one (filepath.Join("/tmp", tenant)) names a
// location outside the checkout; neither can read state this stage would have to
// COPY, and reporting them would turn test table values into findings about
// paths nothing opens.
func gateGoTestCwdRelativeRead(call *ast.CallExpr, file, pkgDir string, line int, consts map[string]string) (gateGoTestRead, bool) {
	head, ok := gateGoTestStringValue(call.Args[0], consts)
	if !ok || gateGoTestFirstPathComponent(head) != ".." {
		return gateGoTestRead{}, false
	}
	rel, expr, literal := gateGoTestJoinArgs(call.Args, consts)
	if !literal {
		return gateGoTestRead{file: file, line: line, expr: expr, literal: false}, true
	}
	joined := filepath.ToSlash(path.Join(pkgDir, rel))
	if joined == ".." || strings.HasPrefix(joined, "../") {
		// Climbs past the checkout, so it names nothing this stage could COPY and
		// nothing the repository root holds.
		return gateGoTestRead{}, false
	}
	return gateGoTestRead{file: file, line: line, expr: expr, path: joined, literal: true}, true
}

func gateGoTestFirstPathComponent(value string) string {
	if i := strings.IndexByte(value, '/'); i >= 0 {
		return value[:i]
	}
	return value
}

// gateGoTestJoinArgs resolves a filepath.Join's remaining arguments to one
// repo-relative path, reporting literal=false when any of them is not a string
// this scan can settle -- in which case the path means nothing and is left
// empty rather than reported as a partial one.
func gateGoTestJoinArgs(args []ast.Expr, consts map[string]string) (path, expr string, literal bool) {
	literal, expr = true, ""
	var parts []string
	for _, arg := range args {
		expr = gateGoTestJoinExpr(expr, arg)
		value, ok := gateGoTestStringValue(arg, consts)
		if !ok {
			literal = false
			continue
		}
		parts = append(parts, value)
	}
	if !literal {
		return "", expr, false
	}
	return filepath.ToSlash(filepath.Join(parts...)), expr, true
}

func gateGoTestJoinExpr(existing string, arg ast.Expr) string {
	piece := gateGoTestExprString(arg)
	if existing == "" {
		return piece
	}
	return existing + ", " + piece
}

// gateGoTestIsRepoRoot reports whether an expression is the repository root.
// A bare identifier only counts when something in this function bound it to a
// repo-root helper: package-level names are free to collide, and
// `repoRoot := t.TempDir()` is a real declaration in this repository.
func gateGoTestIsRepoRoot(expr ast.Expr, rootVars map[string]bool, helpers map[string]int) bool {
	switch v := expr.(type) {
	case *ast.Ident:
		return rootVars[v.Name]
	case *ast.CallExpr:
		id, ok := v.Fun.(*ast.Ident)
		if !ok {
			return false
		}
		depth, ok := helpers[id.Name]
		return ok && depth == 0
	}
	return false
}

// gateGoTestStringValue resolves an expression to a string when it is a
// literal or a constant the enclosing file declares; anything else is a path
// this scan cannot settle.
func gateGoTestStringValue(expr ast.Expr, consts map[string]string) (string, bool) {
	switch v := expr.(type) {
	case *ast.BasicLit:
		if v.Kind != token.STRING {
			return "", false
		}
		value, err := strconv.Unquote(v.Value)
		if err != nil {
			return "", false
		}
		return value, true
	case *ast.Ident:
		value, ok := consts[v.Name]
		return value, ok
	}
	return "", false
}

// gateGoTestTopLevelStringConsts records the package-level string constants a
// file declares, and gateGoTestLocalStringConsts the ones a function declares,
// so a path written as `const scriptPath = "..."` resolves to the same read the
// literal would. The two are kept apart on purpose: a local constant belongs to
// its own function, and letting it resolve an identifier in a sibling function
// would report a path there that the source does not name.
func gateGoTestTopLevelStringConsts(file *ast.File, consts map[string]string) {
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		gateGoTestRecordConstSpecs(gen.Specs, consts)
	}
}

func gateGoTestLocalStringConsts(fn *ast.FuncDecl, consts map[string]string) {
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		stmt, ok := n.(*ast.DeclStmt)
		if !ok {
			return true
		}
		gen, ok := stmt.Decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			return true
		}
		gateGoTestRecordConstSpecs(gen.Specs, consts)
		return true
	})
}

func gateGoTestRecordConstSpecs(specs []ast.Spec, consts map[string]string) {
	for _, spec := range specs {
		value, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		for i, name := range value.Names {
			if i >= len(value.Values) {
				continue
			}
			literal, ok := value.Values[i].(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				continue
			}
			unquoted, err := strconv.Unquote(literal.Value)
			if err != nil {
				continue
			}
			consts[name.Name] = unquoted
		}
	}
}

func gateGoTestExprString(expr ast.Expr) string {
	switch v := expr.(type) {
	case *ast.BasicLit:
		return v.Value
	case *ast.Ident:
		return v.Name
	case *ast.CallExpr:
		return gateGoTestExprString(v.Fun) + "(...)"
	case *ast.SelectorExpr:
		return gateGoTestExprString(v.X) + "." + v.Sel.Name
	}
	return "?"
}

// gateGoTestCopyContractFindings returns every way the erun-devops image fails
// the gate's Go-test COPY contract for one repository root, and the scan they
// were derived from. It is separated from the test that reports them so the
// failure the contract exists to catch -- a gate test reading a repo-root path
// the test stage does not COPY -- is reproducible from a fixture tree, which is
// the one case that must never regress to silence.
func gateGoTestCopyContractFindings(
	t *testing.T,
	root, dockerfile string,
	moduleDirs []string,
	exemptions map[gateGoTestReadExemptionKey]string,
) ([]string, gateGoTestReadScan) {
	t.Helper()
	provided := erunDevopsProvidedSrcPaths(t, root, dockerfile)
	findings := gateGoTestModuleFindings(t, root, moduleDirs)

	scan := scanGateGoTestRepoRootReads(t, root, moduleDirs)
	if len(scan.reads) == 0 {
		findings = append(findings, "found no repo-root reads in the gate's Go tests -- the scan is broken, not the "+
			"tree, and a guard that looked at nothing must not report the contract as satisfied")
		return findings, scan
	}
	return append(findings, gateGoTestReadFindings(provided, scan, exemptions)...), scan
}

// gateGoTestModuleFindings reports the modules check-gate runs `go test` in
// that this scan cannot read itself: one it cannot name from the recipe, and
// one it is not registered for.
func gateGoTestModuleFindings(t *testing.T, root string, moduleDirs []string) []string {
	t.Helper()
	var findings []string
	gateModules, unreadable := gateGoTestModuleDirsInMakefile(t, root)
	for _, line := range unreadable {
		findings = append(findings, "check-gate runs `go test` at "+line+", without a `cd <module>` this scan can "+
			"read: that module's repo-root reads would be invisible to this guard, so name the module in the recipe "+
			"or register it here")
	}
	for _, module := range gateModules {
		if !slices.Contains(moduleDirs, module) {
			findings = append(findings, "check-gate runs `go test` in "+module+", which gateGoTestModuleDirs does not "+
				"register -- a module missing from that list is a module whose repo-root reads this guard never reads, "+
				"so add it there rather than relying on the gate to catch what it cannot see")
		}
	}
	return findings
}

// gateGoTestReadFindings reports every read the image does not demonstrably
// provide, and every exemption that no longer matches a read.
func gateGoTestReadFindings(provided []string, scan gateGoTestReadScan, exemptions map[gateGoTestReadExemptionKey]string) []string {
	var findings []string
	used := map[gateGoTestReadExemptionKey]bool{}
	for _, read := range scan.reads {
		if read.literal && providedSrcPathCoversRead(provided, "/src/"+read.path) {
			continue
		}
		key := read.exemptionKey()
		if _, ok := exemptions[key]; ok {
			used[key] = true
			continue
		}
		findings = append(findings, gateGoTestUncoveredReadFinding(read))
	}
	for _, key := range sortedGateExemptionKeys(exemptions) {
		if !used[key] {
			findings = append(findings, "goTestRepoRootReadExemptions still records "+key.file+"'s "+key.expr+" read ("+
				exemptions[key]+"), but no site there resolves to it any more -- the read was made literal or removed, "+
				"so drop the stale entry rather than leaving an exemption standing for a read nothing makes")
		}
	}
	return findings
}

// gateGoTestUncoveredReadFinding says which read is unaccounted for, and which
// of the two things is wrong with it.
func gateGoTestUncoveredReadFinding(read gateGoTestRead) string {
	if !read.literal {
		return gateGoTestReadSite(read) + " reads a repo-root path through filepath.Join(" + read.expr + "), which " +
			"this check cannot resolve statically, so it cannot tell whether the erun-devops image provides it -- " +
			"make the path literal, or register it in goTestRepoRootReadExemptions with the reason no COPY is needed"
	}
	return gateGoTestReadSite(read) + " reads " + read.path + " from the repository root, but the erun-devops image " +
		"test stage COPYs nothing that provides /src/" + read.path + " -- `make check` passes in a full checkout and " +
		"fails inside every image build, so no release can be produced"
}

func gateGoTestReadSite(read gateGoTestRead) string {
	return read.file + ":" + strconv.Itoa(read.line)
}

// makefileRecipeCDPattern matches the `cd <module> &&` a recipe uses to run a
// module's tests from the repository root. A $(VAR) or a shell expansion is
// deliberately not matched: this scan cannot resolve it, and the caller
// reports an unresolved `go test` rather than guessing which module it names.
var makefileRecipeCDPattern = regexp.MustCompile(`\(?\bcd\s+([A-Za-z0-9_][A-Za-z0-9_./-]*)\s*&&`)

// gateGoTestModuleDirsInMakefile returns the module directories check-gate's
// own prerequisite recipes run `go test` in, plus the recipe lines that run
// `go test` without a `cd` into a directory this scan can name. The second list
// is a return value rather than a report so its handling is testable: a module
// this scan cannot name is a module it would otherwise never read.
func gateGoTestModuleDirsInMakefile(t *testing.T, root string) (modules, unreadable []string) {
	t.Helper()
	rules := makefileRules(t, root)
	gate, ok := rules["check-gate"]
	if !ok {
		t.Fatal("Makefile declares no check-gate target")
	}
	seen := map[string]bool{}
	for _, prereq := range gate.prereqs {
		for _, line := range rules[prereq].recipe {
			trimmed := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "@"))
			if strings.HasPrefix(trimmed, "echo ") || !strings.Contains(trimmed, "go test ") {
				continue
			}
			match := makefileRecipeCDPattern.FindStringSubmatch(trimmed)
			if match == nil {
				unreadable = append(unreadable, prereq+": "+trimmed)
				continue
			}
			module := filepath.ToSlash(match[1])
			if seen[module] {
				continue
			}
			seen[module] = true
			modules = append(modules, module)
		}
	}
	sort.Strings(modules)
	sort.Strings(unreadable)
	return modules, unreadable
}

// gateGoTestReadFixture is one package's worth of the shapes the scan has to
// tell apart. It is parsed, never compiled, so its imports are declarative
// only.
const gateGoTestReadFixture = `package fixture

// repoRoot resolves the checkout root from this file's own location. This file
// sits directly under the module directory, so the grandparent of its own file
// is the repository root.
func repoRoot(t testing.TB) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller could not resolve this test file's path")
	}
	return filepath.Dir(filepath.Dir(file))
}

// harnessModuleRoot resolves this module's directory, one level below the
// repository root, and must not be read as a repository root.
func harnessModuleRoot(t testing.TB) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller could not resolve this test file's path")
	}
	return filepath.Dir(file)
}

func TestModuleScopedHelperIsNotARoot(t *testing.T) {
	_, _ = os.ReadFile(filepath.Join(harnessModuleRoot(t), "go.work"))
}

func TestLiteralAndConstantReads(t *testing.T) {
	root := repoRoot(t)
	const scriptPath = "erun-integration/scripts/integration-test.sh"
	_, _ = os.ReadFile(filepath.Join(root, "erun-devops", "docker", "erun-devops", "Dockerfile"))
	_, _ = os.ReadFile(filepath.Join(repoRoot(t), "erun-cli", "run.sh"))
	_, _ = os.ReadFile(filepath.Join(root, scriptPath))
	_, _ = filepath.Glob(filepath.Join(root, "erun-devops", "docker", "*", "Dockerfile"))
	_, _ = filepath.Rel(root, filepath.Join(root, filepath.FromSlash(entry)))
}

func TestTempDirRootedReadsAreNotRepoRootReads(t *testing.T) {
	root := t.TempDir()
	_, _ = os.ReadFile(filepath.Join(root, "config.yaml"))
}

// helperTakingRoot reads under a root its caller supplies, which this scan does
// not trace back to that caller. This shape is deliberately uncovered, and
// TestScanLeavesParameterRootedAndNonJoinReadsUnseen pins it as such.
func helperTakingRoot(t testing.TB, root, workspace string) {
	_, _ = os.ReadFile(filepath.Join(root, workspace, "package.json"))
}

// repoRootViaLocal resolves the same root through an assigned local. Returning a
// named intermediate is ordinary Go, and a helper the scan does not recognize is
// every read through it going unchecked.
func repoRootViaLocal(t testing.TB) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller could not resolve this test file's path")
	}
	dir := filepath.Dir(filepath.Dir(file))
	return dir
}

// buildStampSources is a package-level table, which is where the CWD-relative
// read this scan was extended for actually lives: paths to files the build
// scripts are checked against are almost always a var rather than a local.
var buildStampSources = []string{
	filepath.Join("..", "Formula", "erun.rb"),
}

func TestCwdRelativeReads(t *testing.T) {
	_, _ = os.ReadFile(filepath.Join("..", "bucket", "erun.json"))
	_, _ = os.ReadFile(filepath.Join(repoRootViaLocal(t), "go.work"))

	// Neither of these reaches repository-root state and neither is a read this
	// scan reports: one names a path inside the package's own directory, the
	// other a location outside the checkout entirely. Reporting them is how a
	// test table value becomes a finding about a path nothing opens.
	_, _ = os.ReadFile(filepath.Join("relative", "repo"))
	_, _ = os.ReadFile(filepath.Join("/tmp", "config.yaml"))
}
`

// writeGateGoTestFixture parses the fixture above as one module's only test
// file and returns a scan of it.
func writeGateGoTestFixture(t *testing.T) gateGoTestReadScan {
	t.Helper()
	root := t.TempDir()
	const module = "erun-integration"
	dir := filepath.Join(root, module)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create fixture module: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fixture_test.go"), []byte(gateGoTestReadFixture), 0o644); err != nil {
		t.Fatalf("write fixture test file: %v", err)
	}
	return scanGateGoTestRepoRootReads(t, root, []string{module})
}

func TestScanGateGoTestRepoRootReadsResolvesTheShapesItClaims(t *testing.T) {
	scan := writeGateGoTestFixture(t)

	type want struct {
		path    string
		literal bool
		expr    string
	}
	wants := []want{
		{path: "erun-devops/docker/erun-devops/Dockerfile", literal: true, expr: `"erun-devops", "docker", "erun-devops", "Dockerfile"`},
		{path: "erun-cli/run.sh", literal: true, expr: `"erun-cli", "run.sh"`},
		{path: "erun-integration/scripts/integration-test.sh", literal: true, expr: "scriptPath"},
		{path: "erun-devops/docker/*/Dockerfile", literal: true, expr: `"erun-devops", "docker", "*", "Dockerfile"`},
		{path: "", literal: false, expr: "filepath.FromSlash(...)"},
		// The package-level table, resolved against the file's own directory:
		// this is the shape erun-ui/buildstamp_test.go's Formula and bucket reads
		// have, and both halves of it — the var rather than a local, and the
		// climbing literal rather than a repoRoot helper — used to make the read
		// invisible.
		{path: "Formula/erun.rb", literal: true, expr: `"..", "Formula", "erun.rb"`},
		{path: "bucket/erun.json", literal: true, expr: `"..", "bucket", "erun.json"`},
		// Through the helper that assigns before it returns.
		{path: "go.work", literal: true, expr: `"go.work"`},
	}
	if len(scan.reads) != len(wants) {
		for _, read := range scan.reads {
			t.Logf("read: %s:%d literal=%v path=%q expr=%s", read.file, read.line, read.literal, read.path, read.expr)
		}
		t.Fatalf("found %d reads, want %d -- a read the scan misses is a read the guard cannot check, and a read "+
			"it invents (a temp directory, a module-scoped helper, a helper's root parameter, a path inside the "+
			"package or outside the checkout) is a demand no COPY can satisfy", len(scan.reads), len(wants))
	}
	for i, got := range scan.reads {
		if got.path != wants[i].path || got.literal != wants[i].literal || got.expr != wants[i].expr {
			t.Errorf("read %d = {path: %q, literal: %v, expr: %q}, want {path: %q, literal: %v, expr: %q}",
				i, got.path, got.literal, got.expr, wants[i].path, wants[i].literal, wants[i].expr)
		}
	}
	if !scan.repoRootHelpers["repoRoot"] {
		t.Errorf("repoRoot was not recognized as a repository-root helper, so no read through it resolved")
	}
	if !scan.repoRootHelpers["repoRootViaLocal"] {
		t.Errorf("repoRootViaLocal returns its directory through an assigned local and was not recognized as a " +
			"repository-root helper, so every read through it went unchecked")
	}
	if scan.repoRootHelpers["harnessModuleRoot"] {
		t.Errorf("harnessModuleRoot resolves this module's directory, one level below the repository root, and was " +
			"wrongly classified as a repository-root helper: reads through it would be checked against the wrong path")
	}
}

// TestScanLeavesParameterRootedAndNonJoinReadsUnseen pins the limits this scan
// states in its documentation, so the boundary is checked rather than asserted.
// These shapes are *not* reported, deliberately: resolving them needs the
// intra-procedural analysis neither half of this guard does, and a change that
// closes one of them should fail here and reconcile the prose rather than
// quietly widen what the guard claims.
func TestScanLeavesParameterRootedAndNonJoinReadsUnseen(t *testing.T) {
	scan := writeGateGoTestFixture(t)
	resolved := map[string]bool{}
	for _, read := range scan.reads {
		resolved[read.expr] = true
	}
	// helperTakingRoot joins under a parameter its caller supplies; the scan does
	// not bind arguments into helpers, so nothing inside it is a candidate.
	for expr := range resolved {
		if strings.Contains(expr, "workspace") || strings.Contains(expr, "package.json") {
			t.Errorf("a read under helperTakingRoot's parameter was reported as %q: either the scan grew "+
				"parameter-bound resolution — in which case the limits recorded on "+
				"TestCheckGateGoTestsReadOnlyRepoRootPathsTheDevopsImageProvides must say so — or it is reporting a "+
				"path it never resolved", expr)
		}
	}
	// A literal first argument that does not climb names a path inside the
	// package's own directory, and an absolute one names a location outside the
	// checkout. Neither reaches repository-root state, and reporting them is how
	// a test table value becomes a finding about a path nothing opens.
	for _, path := range []string{"erun-integration/relative/repo", "tmp/config.yaml"} {
		if _, err := os.Stat(filepath.Join(t.TempDir(), path)); err == nil {
			t.Fatalf("fixture path %q unexpectedly exists", path)
		}
		for _, read := range scan.reads {
			if read.path == path {
				t.Errorf("%q was resolved as a repository-root read, but it names nothing this stage could COPY", path)
			}
		}
	}
}

func TestProvidedSrcPathCoversReadHandlesGlobs(t *testing.T) {
	provided := []string{
		"/src/erun-devops/docker",
		"/src/erun-ui",
		"/src/.gitignore",
		"/src/erun-docs/docs/mcp/overview.md",
	}
	for _, tc := range []struct {
		path string
		want bool
	}{
		{"/src/erun-devops/docker/erun-devops/Dockerfile", true},
		{"/src/erun-devops/docker/*/Dockerfile", true},
		{"/src/erun-ui/frontend/src", true},
		{"/src/.gitignore", true},
		{"/src/erun-docs/docs/mcp/overview.md", true},
		{"/src/Makefile", false},
		{"/src/erun-docs/docs/reference/env-vars.md", false},
		{"*", false},
	} {
		if got := providedSrcPathCoversRead(provided, tc.path); got != tc.want {
			t.Errorf("providedSrcPathCoversRead(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

// TestErunDevopsProvidedSrcPathsTreatsDirectorySourcesAsDockerDoes pins the
// model against what a real docker build does with each COPY shape, because the
// two directions of getting it wrong are both defects: too little and the guard
// passes a read the release venue fails, too much and it reds a change that is
// correct.
//
// The shapes here were each verified against a real build. The one that matters
// is the directory source with a slash-terminated destination: `COPY some-dir
// /src/some-dir/` lands the *contents* of some-dir at /src/some-dir, not at
// /src/some-dir/some-dir, because the basename level is a file source's rule.
// Reading it as a file is what reported a genuinely provided path as absent.
func TestErunDevopsProvidedSrcPathsTreatsDirectorySourcesAsDockerDoes(t *testing.T) {
	context := t.TempDir()
	for _, dir := range []string{"erun-cli", "some-dir", "dir-two"} {
		if err := os.MkdirAll(filepath.Join(context, dir), 0o755); err != nil {
			t.Fatalf("create context dir %s: %v", dir, err)
		}
	}
	dockerfile := filepath.Join(context, "Dockerfile")
	body := strings.Join([]string{
		"FROM alpine:3.20 AS test",
		"WORKDIR /src",
		"COPY .dockerignore /src/.dockerignore",
		"COPY package.json yarn.lock /src/",
		"COPY erun-cli /src/erun-cli",
		"COPY some-dir /src/some-dir/",
		"COPY a.txt dir-two /src/three/",
		"COPY --from=node /usr/local/bin/node /usr/local/bin/node",
		"COPY --chmod=0755 erun-devops/docker/erun-devops/entrypoint.sh /usr/local/bin/erun-devops-entrypoint",
		`# COPY /src/mentioned-in-a-comment`,
		"RUN make check && touch /test-ok",
		"",
		"FROM alpine:3.20 AS builder",
		"COPY erun-devops/VERSION /src/erun-devops/VERSION",
	}, "\n")
	if err := os.WriteFile(dockerfile, []byte(body), 0o644); err != nil {
		t.Fatalf("write Dockerfile fixture: %v", err)
	}
	provided := erunDevopsProvidedSrcPaths(t, context, dockerfile)
	want := []string{
		"/src/.dockerignore",
		"/src/package.json",
		"/src/yarn.lock",
		"/src/erun-cli",
		"/src/some-dir",
		"/src/three/a.txt",
		"/src/three",
	}
	if !slices.Equal(provided, want) {
		t.Errorf("provided = %v, want %v — a directory destination lands each file source under it by name and each "+
			"directory source's contents at it; a COPY outside the gate stage places nothing the gate can read, and "+
			"/src itself must never be reported, because it would satisfy every path this guard asks about",
			provided, want)
	}
}

// TestGateStageCopyModelIgnoresCopiesOutsideTheGateStage is the regression for
// the stage scoping this model needs. It builds the exact disagreement the real
// Dockerfile has: the builder stage COPYs a path the gate stage does not, so a
// guard that unions every stage's COPYs reports that path as provided while
// `make check` inside the image cannot read it.
func TestGateStageCopyModelIgnoresCopiesOutsideTheGateStage(t *testing.T) {
	context := t.TempDir()
	dockerfile := filepath.Join(context, "Dockerfile")
	body := strings.Join([]string{
		"FROM alpine:3.20 AS test",
		"WORKDIR /src",
		"COPY Makefile /src/Makefile",
		"RUN make check && touch /test-ok",
		"",
		"FROM alpine:3.20 AS builder",
		"COPY erun-devops/VERSION /src/erun-devops/VERSION",
	}, "\n")
	if err := os.WriteFile(dockerfile, []byte(body), 0o644); err != nil {
		t.Fatalf("write Dockerfile fixture: %v", err)
	}
	provided := erunDevopsProvidedSrcPaths(t, context, dockerfile)
	if !slices.Equal(provided, []string{"/src/Makefile"}) {
		t.Errorf("provided = %v, want only the gate stage's own COPY — a path another stage COPYs is not one the gate "+
			"can read, so modelling it as provided passes a gate test that dies in the image", provided)
	}
	if providedSrcPathCovers(provided, "/src/erun-devops/VERSION") {
		t.Error("the builder stage's COPY was modelled as provided to the gate stage, which is the union-of-all-stages " +
			"defect this scoping exists to prevent")
	}
}

// TestGateStageCopyModelRequiresExactlyOneGateStage pins the fail-closed
// direction of that scoping. A Dockerfile with no stage naming the gate command
// has no stage to model, and one with two is ambiguous; both must be a reported
// error rather than a fallback to the whole file, which is the union defect
// wearing a fixture that lacks FROM lines.
func TestGateStageCopyModelRequiresExactlyOneGateStage(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{
			name: "no FROM lines at all",
			body: "COPY Makefile /src/Makefile\nCOPY erun-devops/VERSION /src/erun-devops/VERSION\n",
		},
		{
			name: "stages but none running the gate",
			body: "FROM alpine:3.20 AS test\nCOPY Makefile /src/Makefile\n\nFROM alpine:3.20 AS builder\nCOPY x /src/x\n",
		},
		{
			name: "two stages running the gate",
			body: "FROM alpine:3.20 AS one\nRUN make check\n\nFROM alpine:3.20 AS two\nRUN make check\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := dockerfileGateStage(tc.body); err == nil {
				t.Fatal("dockerfileGateStage accepted a Dockerfile with no single gate stage — a model that cannot " +
					"name the stage the gate runs in cannot say which COPYs the gate sees")
			} else if !strings.Contains(err.Error(), devopsGateStageCommand) {
				t.Errorf("error %q does not name %q, so a reader cannot tell what the model failed to find", err, devopsGateStageCommand)
			}
		})
	}

	// The canary the two halves of this guard both lean on: the real Dockerfile
	// must still resolve to exactly one stage. If this stops being true the
	// scans above are modelling some other stage's COPYs.
	root := repoRootForDockerignoreTest(t)
	real := filepath.Join(root, "erun-devops", "docker", "erun-devops", "Dockerfile")
	data, err := os.ReadFile(real)
	if err != nil {
		t.Fatalf("read %s: %v", real, err)
	}
	stage, err := dockerfileGateStage(string(data))
	if err != nil {
		t.Fatalf("the erun-devops Dockerfile no longer names exactly one gate stage: %v", err)
	}
	if !strings.Contains(stage.base, "golang:") {
		t.Errorf("the resolved gate stage is based on %q, which is not the golang test stage this guard models", stage.base)
	}
}

func TestGateGoTestModuleDirsInMakefile(t *testing.T) {
	root := t.TempDir()
	makefile := strings.Join([]string{
		"check-gate: test-mod-a test-mod-b test-mod-c",
		"",
		"test-mod-a:",
		"\t@(cd erun-common && go test -count=1 ./...)",
		"",
		"test-mod-b:",
		"\t@echo \">> go test mod-b\"",
		"\t@go test ./...",
		"",
		"test-mod-c:",
		"\t@(cd $(MODULE) && go test ./...)",
	}, "\n")
	if err := os.WriteFile(filepath.Join(root, "Makefile"), []byte(makefile), 0o644); err != nil {
		t.Fatalf("write Makefile fixture: %v", err)
	}
	modules, unreadable := gateGoTestModuleDirsInMakefile(t, root)
	if !slices.Equal(modules, []string{"erun-common"}) {
		t.Errorf("modules = %v, want [erun-common]", modules)
	}
	// The `@echo` line names `go test` as a label rather than running it, and
	// must not be reported; the other two run it in a module this scan cannot
	// name, and must be.
	if len(unreadable) != 2 {
		t.Fatalf("unreadable = %v, want the two recipes that run `go test` in a module this scan cannot name", unreadable)
	}
	for _, line := range unreadable {
		if strings.Contains(line, "mod-b") && strings.Contains(line, "echo") {
			t.Errorf("the `@echo` label was reported as an unreadable `go test` recipe: %s", line)
		}
	}
}

// gateGoTestFixtureRepoRootHelper is the runtime.Caller-derived repoRoot every
// fixture test file declares, in the shape this scan recognizes: it sits
// directly under the module directory, so the grandparent of its own file is the
// repository root.
const gateGoTestFixtureRepoRootHelper = `package fixture

func repoRoot(t testing.TB) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Dir(filepath.Dir(file))
}
`

// gateGoTestCopyContractFixtureTree writes a miniature repository that has
// everything the contract reads -- a Makefile naming a Go test module, that
// module's test file, and a Dockerfile whose gate stage COPYs exactly the files
// named here -- and returns its root, which is also the Dockerfile's build
// context, and the Dockerfile path.
//
// The Dockerfile carries two stages on purpose: the gate stage that runs
// `make check`, and a second stage whose COPYs the gate never sees. A one-stage
// fixture could not tell a stage-scoped model from one that unions the whole
// file, which is the defect the scoping closes.
func gateGoTestCopyContractFixtureTree(t *testing.T, testFileBody string, gateStageCopyLines, otherStageCopyLines []string) (root, dockerfile string) {
	t.Helper()
	root = t.TempDir()
	makefile := strings.Join([]string{
		"check-gate: test-erun-common",
		"",
		"test-erun-common:",
		"\t@(cd erun-common && go test -count=1 ./...)",
	}, "\n")
	if err := os.WriteFile(filepath.Join(root, "Makefile"), []byte(makefile), 0o644); err != nil {
		t.Fatalf("write fixture Makefile: %v", err)
	}
	module := filepath.Join(root, "erun-common")
	if err := os.MkdirAll(module, 0o755); err != nil {
		t.Fatalf("create fixture module: %v", err)
	}
	testFile := gateGoTestFixtureRepoRootHelper + testFileBody
	if err := os.WriteFile(filepath.Join(module, "gitignore_clean_checkout_test.go"), []byte(testFile), 0o644); err != nil {
		t.Fatalf("write fixture test file: %v", err)
	}
	dockerfile = filepath.Join(root, "Dockerfile")
	lines := []string{"FROM alpine:3.20 AS test", "WORKDIR /src"}
	lines = append(lines, gateStageCopyLines...)
	lines = append(lines, "RUN make check && touch /test-ok", "", "FROM alpine:3.20 AS builder")
	lines = append(lines, otherStageCopyLines...)
	lines = append(lines, "")
	if err := os.WriteFile(dockerfile, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatalf("write fixture Dockerfile: %v", err)
	}
	return root, dockerfile
}

// gateGoTestFixtureReadsGitignore is the fixture test body every case below
// starts from: the read the shell-script half of this guard structurally could
// not see, which is what the Go-test half exists for.
const gateGoTestFixtureReadsGitignore = `
func TestGeneratedPinHistoryDoesNotDirtyACleanCheckout(t *testing.T) {
	root := repoRoot(t)
	if _, err := os.ReadFile(filepath.Join(root, ".gitignore")); err != nil {
		t.Fatalf("read the checkout's .gitignore: %v", err)
	}
}
`

// TestGateGoTestCopyContractFindingsReportsAMissingRepoRootCopy is the
// reproduction for the class this file closes: the guard that reads the
// Makefile and the Dockerfile scanned shell scripts only, so a gate Go test
// reading a repo-root file through repoRoot was structurally invisible to it.
// The release gate went red for exactly this on the tree where .gitignore was
// absent from the erun-devops test stage, while that guard passed.
//
// The two Dockerfiles differ in one line, and that line is the whole defect:
// with the COPY present the contract is satisfied, and without it the read is
// reported by name. A version of this check that resolved nothing, or that
// treated an unprovided path as satisfied, passes the first case and fails the
// second.
func TestGateGoTestCopyContractFindingsReportsAMissingRepoRootCopy(t *testing.T) {
	brokenRoot, brokenDockerfile := gateGoTestCopyContractFixtureTree(t, gateGoTestFixtureReadsGitignore, []string{
		"COPY .dockerignore /src/.dockerignore",
		"COPY Makefile /src/Makefile",
	}, nil)
	findings, scan := gateGoTestCopyContractFindings(t, brokenRoot, brokenDockerfile, []string{"erun-common"}, nil)
	if len(scan.reads) != 1 || scan.reads[0].path != ".gitignore" {
		t.Fatalf("fixture scan = %v, want the one .gitignore read the fixture makes", scan.reads)
	}
	if len(findings) != 1 {
		t.Fatalf("findings = %q, want exactly the one missing-COPY report", findings)
	}
	if !strings.Contains(findings[0], ".gitignore") || !strings.Contains(findings[0], "gitignore_clean_checkout_test.go:14") {
		t.Errorf("finding %q does not name the read it is about", findings[0])
	}

	fixedRoot, fixedDockerfile := gateGoTestCopyContractFixtureTree(t, gateGoTestFixtureReadsGitignore, []string{
		"COPY .dockerignore /src/.dockerignore",
		"COPY Makefile /src/Makefile",
		"COPY .gitignore /src/.gitignore",
	}, nil)
	if findings, _ := gateGoTestCopyContractFindings(t, fixedRoot, fixedDockerfile, []string{"erun-common"}, nil); len(findings) != 0 {
		t.Errorf("findings = %q, want none once the Dockerfile COPYs the file the read names", findings)
	}

	// The decoy stage is the stage-scoping regression: the *other* stage COPYs
	// the very file the read names, and that must not count. A model that unions
	// every stage's COPYs reports this tree as satisfied while `make check`
	// inside the image still cannot read .gitignore.
	decoyRoot, decoyDockerfile := gateGoTestCopyContractFixtureTree(t, gateGoTestFixtureReadsGitignore, []string{
		"COPY Makefile /src/Makefile",
	}, []string{
		"COPY .gitignore /src/.gitignore",
	})
	decoyFindings, _ := gateGoTestCopyContractFindings(t, decoyRoot, decoyDockerfile, []string{"erun-common"}, nil)
	if len(decoyFindings) != 1 || !strings.Contains(decoyFindings[0], ".gitignore") {
		t.Errorf("findings = %q, want the .gitignore read reported even though the other stage COPYs it — only the "+
			"gate stage's own COPYs are ones the gate can read", decoyFindings)
	}
}

// TestGateGoTestCopyContractFindingsReportsACwdRelativeMissingCopy is the
// regression for the read that names no repoRoot at all. A test that opens
// filepath.Join("..", "Formula", "erun.rb") reads repository-root state through
// `go test`'s working directory, not through a helper, so the helper-rooted scan
// above never saw it — however fatally it read, and whatever the Dockerfile had
// to COPY to keep it working. The two Dockerfiles differ in the one COPY line
// the read needs, as above.
func TestGateGoTestCopyContractFindingsReportsACwdRelativeMissingCopy(t *testing.T) {
	body := `
func TestBuildScriptsStampSymbols(t *testing.T) {
	if _, err := os.ReadFile(filepath.Join("..", "Formula", "erun.rb")); err != nil {
		t.Fatalf("read the formula: %v", err)
	}
}
`
	brokenRoot, brokenDockerfile := gateGoTestCopyContractFixtureTree(t, body, []string{
		"COPY Makefile /src/Makefile",
	}, nil)
	findings, scan := gateGoTestCopyContractFindings(t, brokenRoot, brokenDockerfile, []string{"erun-common"}, nil)
	if len(scan.reads) != 1 || scan.reads[0].path != "Formula/erun.rb" || !scan.reads[0].literal {
		t.Fatalf("fixture scan = %v, want the one CWD-relative Formula/erun.rb read resolved to a repository-root path", scan.reads)
	}
	if len(findings) != 1 || !strings.Contains(findings[0], "Formula/erun.rb") {
		t.Fatalf("findings = %q, want the CWD-relative read reported by the path it names", findings)
	}

	fixedRoot, fixedDockerfile := gateGoTestCopyContractFixtureTree(t, body, []string{
		"COPY Makefile /src/Makefile",
		"COPY Formula /src/Formula",
	}, nil)
	if findings, _ := gateGoTestCopyContractFindings(t, fixedRoot, fixedDockerfile, []string{"erun-common"}, nil); len(findings) != 0 {
		t.Errorf("findings = %q, want none once the gate stage COPYs the directory the read resolves into", findings)
	}
}

// TestGateGoTestCopyContractFindingsReportsAnAssignedLocalHelper is the
// regression for the same read written through a helper that assigns before it
// returns. That shape is ordinary Go, and a helper the scan fails to recognize
// is every read through it going unchecked — silently, because the guard's only
// signal for it is the canary list, which a new helper is not on.
func TestGateGoTestCopyContractFindingsReportsAnAssignedLocalHelper(t *testing.T) {
	body := `
func repoRootViaLocal(t testing.TB) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(filepath.Dir(file))
	return dir
}

func TestReadsThroughAnAssignedLocal(t *testing.T) {
	if _, err := os.ReadFile(filepath.Join(repoRootViaLocal(t), ".gitignore")); err != nil {
		t.Fatalf("read the checkout's .gitignore: %v", err)
	}
}
`
	root, dockerfile := gateGoTestCopyContractFixtureTree(t, body, []string{
		"COPY Makefile /src/Makefile",
	}, nil)
	findings, scan := gateGoTestCopyContractFindings(t, root, dockerfile, []string{"erun-common"}, nil)
	if !scan.repoRootHelpers["repoRootViaLocal"] {
		t.Fatal("repoRootViaLocal was not recognized as a repository-root helper, so the read through it resolved to " +
			"nothing and the guard reports the tree as satisfied")
	}
	if len(findings) != 1 || !strings.Contains(findings[0], ".gitignore") {
		t.Fatalf("findings = %q, want the .gitignore read through the assigned-local helper reported", findings)
	}
}
