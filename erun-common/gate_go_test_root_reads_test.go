package eruncommon

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
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
	// this guard asserts what the image provides for each of them.
	{"erun-common/dockerfile_copy_contract_test.go", "script"}: "script ranges over the shell scripts the Makefile's check-gate targets name, " +
		"each already confirmed to exist in the checkout by checkGateShellScripts; the shell-script half of this guard " +
		"checks what the erun-devops image provides for every one of them",

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
	depth, found := 0, false
	ast.Inspect(body, func(n ast.Node) bool {
		ret, ok := n.(*ast.ReturnStmt)
		if !ok {
			return true
		}
		for _, result := range ret.Results {
			if ok, d := gateGoTestDirNesting(result, callerVar); ok {
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

func gateGoTestDirNesting(expr ast.Expr, callerVar string) (bool, int) {
	switch v := expr.(type) {
	case *ast.Ident:
		return v.Name == callerVar, 0
	case *ast.CallExpr:
		sel, ok := v.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Dir" || len(v.Args) != 1 {
			return false, 0
		}
		inner, depth := gateGoTestDirNesting(v.Args[0], callerVar)
		if !inner {
			return false, 0
		}
		return true, depth + 1
	}
	return false, 0
}

// collectGateGoTestReadsInFile finds the filepath.Join calls rooted at the
// repository root. Root variables are scoped to the function that binds them:
// `root` is a name tests use for temp directories too, and treating every
// one of those as the repository root would report temp paths as repo-root
// reads.
func collectGateGoTestReadsInFile(
	root, path string,
	fset *token.FileSet,
	file *ast.File,
	pkgConsts map[string]string,
	helpers map[string]int,
) []gateGoTestRead {
	rel := gateRepoRelativePath(root, path)
	var reads []gateGoTestRead
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		consts := gateGoTestConsts(pkgConsts, fn)
		rootVars := gateGoTestRootVars(fn, helpers)
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if read, ok := gateGoTestJoinRead(call, fset, rel, consts, rootVars, helpers); ok {
				reads = append(reads, read)
			}
			return true
		})
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
// it is rooted at one.
func gateGoTestJoinRead(
	call *ast.CallExpr,
	fset *token.FileSet,
	file string,
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
	if !gateGoTestIsRepoRoot(call.Args[0], rootVars, helpers) {
		return gateGoTestRead{}, false
	}
	path, expr, literal := gateGoTestJoinArgs(call.Args[1:], consts)
	return gateGoTestRead{
		file:    file,
		line:    fset.Position(call.Pos()).Line,
		expr:    expr,
		path:    path,
		literal: literal,
	}, true
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
	provided := erunDevopsProvidedSrcPaths(t, dockerfile)
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
// not trace back to that caller.
func helperTakingRoot(t testing.TB, root, workspace string) {
	_, _ = os.ReadFile(filepath.Join(root, workspace, "package.json"))
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
	}
	if len(scan.reads) != len(wants) {
		for _, read := range scan.reads {
			t.Logf("read: %s:%d literal=%v path=%q expr=%s", read.file, read.line, read.literal, read.path, read.expr)
		}
		t.Fatalf("found %d reads, want %d -- a read the scan misses is a read the guard cannot check, and a read "+
			"it invents (a temp directory, a module-scoped helper, a helper's root parameter) is a demand no COPY "+
			"can satisfy", len(scan.reads), len(wants))
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
	if scan.repoRootHelpers["harnessModuleRoot"] {
		t.Errorf("harnessModuleRoot resolves this module's directory, one level below the repository root, and was " +
			"wrongly classified as a repository-root helper: reads through it would be checked against the wrong path")
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

func TestErunDevopsProvidedSrcPathsExpandsDirectoryCopies(t *testing.T) {
	dir := t.TempDir()
	dockerfile := filepath.Join(dir, "Dockerfile")
	body := strings.Join([]string{
		"COPY .dockerignore /src/.dockerignore",
		"COPY package.json yarn.lock /src/",
		"COPY erun-cli /src/erun-cli",
		"COPY --from=node /usr/local/bin/node /usr/local/bin/node",
		"COPY --chmod=0755 erun-devops/docker/erun-devops/entrypoint.sh /usr/local/bin/erun-devops-entrypoint",
		`# COPY /src/mentioned-in-a-comment`,
	}, "\n")
	if err := os.WriteFile(dockerfile, []byte(body), 0o644); err != nil {
		t.Fatalf("write Dockerfile fixture: %v", err)
	}
	provided := erunDevopsProvidedSrcPaths(t, dockerfile)
	want := []string{"/src/.dockerignore", "/src/package.json", "/src/yarn.lock", "/src/erun-cli"}
	if !slices.Equal(provided, want) {
		t.Errorf("provided = %v, want %v -- a COPY with a directory destination lands each source under it by name, "+
			"and /src itself must never be reported, because it would satisfy every path this guard asks about",
			provided, want)
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

// gateGoTestCopyContractFixtureTree writes a miniature repository that has
// everything the contract reads -- a Makefile naming a Go test module, that
// module's test file, and a Dockerfile whose COPY set provides exactly the
// files named here -- and returns its root and the Dockerfile path.
func gateGoTestCopyContractFixtureTree(t *testing.T, dockerfileCopyLines []string) (root, dockerfile string) {
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
	// A module-directory file's grandparent is the repository root, which is
	// the shape repoRoot has in every module this scan reads.
	testFile := `package fixture

func repoRoot(t testing.TB) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Dir(filepath.Dir(file))
}

func TestGeneratedPinHistoryDoesNotDirtyACleanCheckout(t *testing.T) {
	root := repoRoot(t)
	if _, err := os.ReadFile(filepath.Join(root, ".gitignore")); err != nil {
		t.Fatalf("read the checkout's .gitignore: %v", err)
	}
}
`
	if err := os.WriteFile(filepath.Join(module, "gitignore_clean_checkout_test.go"), []byte(testFile), 0o644); err != nil {
		t.Fatalf("write fixture test file: %v", err)
	}
	dockerfile = filepath.Join(root, "Dockerfile")
	body := strings.Join(append(append([]string{}, dockerfileCopyLines...), ""), "\n")
	if err := os.WriteFile(dockerfile, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture Dockerfile: %v", err)
	}
	return root, dockerfile
}

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
	brokenRoot, brokenDockerfile := gateGoTestCopyContractFixtureTree(t, []string{
		"COPY .dockerignore /src/.dockerignore",
		"COPY Makefile /src/Makefile",
	})
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

	fixedRoot, fixedDockerfile := gateGoTestCopyContractFixtureTree(t, []string{
		"COPY .dockerignore /src/.dockerignore",
		"COPY Makefile /src/Makefile",
		"COPY .gitignore /src/.gitignore",
	})
	if findings, _ := gateGoTestCopyContractFindings(t, fixedRoot, fixedDockerfile, []string{"erun-common"}, nil); len(findings) != 0 {
		t.Errorf("findings = %q, want none once the Dockerfile COPYs the file the read names", findings)
	}
}
