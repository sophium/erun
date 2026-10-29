package backendapi

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

const (
	erunCommonImportPath = "github.com/sophium/erun/erun-common"
	erunCommonDir        = "../../erun-common"
)

var bannedImports = map[string]string{
	"os/exec": "spawns a subprocess",
	"syscall": "can reach Exec/ForkExec directly",
}

// bannedCommonFuncs shell out to a CLI binary. InitCloudContext is absent: the
// backend injects SDK-backed dependencies into it.
var bannedCommonFuncs = []string{
	"Command",
	"RawCommandRunner",
	"RunRawCommand",
	"RunHelmDeploy",
	"RunDeploySpec",
	"RunDeploySpecs",
	"DeployHelmChart",
	"EnsureKubernetesNamespace",
}

// TestBackendRunsNoExternalBinaries: the backend drives automation through Go SDKs only, so its
// image ships no binaries. Scope is this module's own source, not the transitive import graph;
// creating a Kubernetes Job (internal/deployexec) is not execution.
func TestBackendRunsNoExternalBinaries(t *testing.T) {
	root, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	scanned := 0
	walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			return parseErr
		}
		scanned++
		rel, _ := filepath.Rel(root, path)
		checkBannedImports(t, rel, file)
		checkBannedCommonCalls(t, rel, file)
		return nil
	})
	if walkErr != nil {
		t.Fatal(walkErr)
	}
	if scanned == 0 {
		t.Fatalf("scanned no production .go files under %s — the walk is misconfigured", root)
	}
	t.Logf("verified %d production .go files run no external binaries", scanned)
}

func checkBannedImports(t *testing.T, rel string, file *ast.File) {
	t.Helper()
	for _, spec := range file.Imports {
		path := strings.Trim(spec.Path.Value, `"`)
		if reason, banned := bannedImports[path]; banned {
			t.Errorf("%s imports %s (%s) — erun-backend-api must drive its automation via Go SDKs, not subprocesses", rel, path, reason)
		}
	}
}

func checkBannedCommonCalls(t *testing.T, rel string, file *ast.File) {
	t.Helper()
	locals := erunCommonLocalNames(file)
	if len(locals) == 0 {
		return
	}
	banned := make(map[string]bool, len(bannedCommonFuncs))
	for _, name := range bannedCommonFuncs {
		banned[name] = true
	}
	ast.Inspect(file, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := selector.X.(*ast.Ident)
		if !ok || !locals[pkg.Name] || !banned[selector.Sel.Name] {
			return true
		}
		t.Errorf("%s references %s.%s — that shells out to a CLI binary; the backend must use its own SDK path (internal/provision, internal/deployexec)", rel, pkg.Name, selector.Sel.Name)
		return true
	})
}

func erunCommonLocalNames(file *ast.File) map[string]bool {
	locals := map[string]bool{}
	for _, spec := range file.Imports {
		importPath := strings.Trim(spec.Path.Value, `"`)
		if importPath != erunCommonImportPath && !strings.HasPrefix(importPath, erunCommonImportPath+"/") {
			continue
		}
		switch {
		case spec.Name != nil:
			locals[spec.Name.Name] = true
		case importPath == erunCommonImportPath:
			locals["eruncommon"] = true
		default:
			locals[path.Base(importPath)] = true
		}
	}
	return locals
}

// TestBannedCommonFuncsStillExist: a renamed erun-common function would silently guard nothing.
func TestBannedCommonFuncsStillExist(t *testing.T) {
	declared := erunCommonFuncNames(t)
	if len(declared) == 0 {
		t.Fatalf("parsed no erun-common declarations from %s — the lookup is misconfigured", erunCommonDir)
	}
	for _, name := range bannedCommonFuncs {
		if !declared[name] {
			t.Errorf("banned helper eruncommon.%s no longer exists — update bannedCommonFuncs to the name that replaced it", name)
		}
	}
}

func erunCommonFuncNames(t *testing.T) map[string]bool {
	t.Helper()
	declared := map[string]bool{}
	err := filepath.WalkDir(erunCommonDir, func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return err
		}
		file, parseErr := parser.ParseFile(token.NewFileSet(), name, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			return parseErr
		}
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil {
				declared[fn.Name.Name] = true
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return declared
}
