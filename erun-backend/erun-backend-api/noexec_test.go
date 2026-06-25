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

// TestBackendRunsNoExternalBinaries enforces the architecture invariant that
// erun-backend-api drives every automation through Go SDKs and never executes an
// external binary — no `aws`/`kubectl`/`helm`/`docker` subprocess, from the API
// handlers or the DBOS provisioning/deploy workflows. This is what keeps the API
// pod a thin, executable-free image (see AGENTS.md § "The backend drives every
// automation through Go SDKs, never by executing external binaries").
//
// It fails if any production (non-test) .go file in the module imports os/exec,
// or calls one of erun-common's CLI-shelling helpers (the CLI/desktop transport's
// path). The backend must use its own SDK paths instead: internal/deploy
// (client-go + helm.sh/helm/v3) and internal/provision (aws-sdk-go-v2).
func TestBackendRunsNoExternalBinaries(t *testing.T) {
	bannedImports := map[string]bool{
		`"os/exec"`: true,
	}
	// erun-common helpers that shell out to a CLI binary. eruncommon.InitCloudContext
	// is deliberately NOT banned: it takes injected dependencies, and the backend
	// supplies SDK-backed ones (internal/provision/awssdk.go) + a no-op RunKubectl.
	bannedCommonCalls := map[string]bool{
		"Command":                   true,
		"RunHelmDeploy":             true,
		"DeployHelmChart":           true,
		"EnsureKubernetesNamespace": true,
		"RunDeploySpec":             true,
		"RunDeploySpecs":            true,
		"RunRawCommand":             true,
		"RawCommandRunner":          true,
	}

	root, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	scanned := 0
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return perr
		}
		scanned++
		rel, _ := filepath.Rel(root, path)
		for _, imp := range file.Imports {
			if bannedImports[imp.Path.Value] {
				t.Errorf("%s imports %s — erun-backend-api must drive automations via Go SDKs, not subprocesses", rel, imp.Path.Value)
			}
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "eruncommon" {
				return true
			}
			if bannedCommonCalls[sel.Sel.Name] {
				t.Errorf("%s calls eruncommon.%s — that shells out to a CLI; the backend must use its own SDK path (internal/deploy, internal/provision)", rel, sel.Sel.Name)
			}
			return true
		})
		return nil
	})
	if walkErr != nil {
		t.Fatal(walkErr)
	}
	// Guard against a vacuous pass: if the walk scanned nothing, the check is a
	// no-op and the invariant is unenforced.
	if scanned == 0 {
		t.Fatalf("scanned no production .go files under %s — the walk is misconfigured", root)
	}
	t.Logf("verified %d production .go files run no external binaries", scanned)
}
