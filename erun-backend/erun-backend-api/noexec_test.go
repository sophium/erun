package backendapi

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// erunCommonImportPath is the module whose CLI-shelling helpers are banned here;
// erunCommonDir is where this module's go.mod replace directive points.
const (
	erunCommonImportPath = "github.com/sophium/erun/erun-common"
	erunCommonDir        = "../../erun-common"
)

// bannedImports are the standard-library doors to a subprocess.
var bannedImports = map[string]string{
	"os/exec": "spawns a subprocess",
	"syscall": "can reach Exec/ForkExec directly",
}

// bannedCommonFuncs are the erun-common helpers that shell out to a CLI binary —
// the CLI and desktop transports' path, which the backend must not take.
// InitCloudContext is deliberately absent: it takes injected dependencies, and
// the backend supplies SDK-backed ones (internal/provision/awssdk.go) plus a
// no-op RunKubectl, so it reaches AWS without a binary.
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

// TestBackendRunsNoExternalBinaries enforces the architecture invariant that
// erun-backend-api drives its automation through Go SDKs and never executes an
// external binary — no `aws`, `kubectl`, `helm`, or `docker` subprocess, from
// the HTTP handlers or from the DBOS provisioning workflow. That invariant is
// what lets the API ship as a thin alpine image carrying only the Go binary.
//
// Scope is this module's own production source. It is deliberately not the
// transitive import graph: client-go's exec auth plugin, the AWS SDK's process
// credential provider, and erun-common (which is the CLI's shared layer) all
// import os/exec legitimately, so a reachability ban could only ever be
// satisfied by dropping those dependencies. What this module's own code does
// with them is the thing worth pinning, and it is what actually decides whether
// the image needs a binary on PATH.
//
// Scheduling work elsewhere is not execution: internal/deployexec creates a
// Kubernetes Job whose container runs `erun deploy` from the erun-devops runtime
// image. That command lives in a Job spec sent to the API server, in a different
// pod — the backend process still runs nothing.
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
	// A walk that scanned nothing would pass without checking anything.
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

// erunCommonLocalNames returns every name erun-common is bound to in this file,
// so the ban holds under any import alias.
func erunCommonLocalNames(file *ast.File) map[string]bool {
	locals := map[string]bool{}
	for _, spec := range file.Imports {
		if strings.Trim(spec.Path.Value, `"`) != erunCommonImportPath {
			continue
		}
		if spec.Name != nil {
			locals[spec.Name.Name] = true
			continue
		}
		locals["eruncommon"] = true
	}
	return locals
}

// TestBannedCommonFuncsStillExist keeps the ban list honest. A name that
// erun-common has since renamed would silently guard nothing, so a rename must
// fail here and be carried across rather than quietly widening what the backend
// may call.
func TestBannedCommonFuncsStillExist(t *testing.T) {
	declared := map[string]bool{}
	entries, err := filepath.Glob(filepath.Join(erunCommonDir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range entries {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil {
				declared[fn.Name.Name] = true
			}
		}
	}
	if len(declared) == 0 {
		t.Fatalf("parsed no erun-common declarations from %s — the lookup is misconfigured", erunCommonDir)
	}
	for _, name := range bannedCommonFuncs {
		if !declared[name] {
			t.Errorf("banned helper eruncommon.%s no longer exists — update bannedCommonFuncs to the name that replaced it", name)
		}
	}
}
