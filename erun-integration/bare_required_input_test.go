package integration

// bare_required_input_test.go is a repo-state structural gate for root
// AGENTS.md § "Smooth, Seamless, No Dead Ends": a validation failure that
// names only a generic missing-input phrase, with no operation and no
// recovery, is exactly failure mode 1 ("advice that cannot work" — here,
// no advice at all). An audit once found 13 production call sites emitting
// exactly `fmt.Errorf("tenant is required")`, plus two siblings in the same
// erun-common function, after an earlier fix addressed one lone producer and
// closed its own follow-up audit item unperformed. This is the check that
// closes the gap: it fails the build the next time one of these exact bare
// phrases reappears in non-test Go source anywhere in the repo. It runs as
// an ordinary `go test` in this module, so it is part of
// `make integration-test`/`make check` with no extra wiring — the same
// precedent as desktop_surface_test.go.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// bareRequiredInputPhrases is the exact set of string literals this gate
// bars from non-test Go source: a bare "<subject> is required" with no
// operation and no recovery. The fix that added this gate converted every
// "tenant is required" producer plus its immediate "environment is
// required" sibling in the same erun-common function — it deliberately did
// not chase every "<X> is required" string in the repo (on the order of 150
// at the time of writing, including several more "cloud provider alias is
// required" sites this same audit found but left alone). Most of the rest
// already name their own specific subject (e.g. "review id is required",
// "target branch is required", "cloud provider alias is required") and are
// a lower-severity instance of the same class, not the "four words, no
// operation, no subject beyond a generic noun, no recovery" shape this gate
// targets. Extend this set only for a phrase that genuinely matches that
// shape on a reachable path — not for every validation string that happens
// to end in "is required".
var bareRequiredInputPhrases = map[string]bool{
	"tenant is required":      true,
	"environment is required": true,
}

// skipDirForBareRequiredInputScan excludes directories that hold no
// hand-written production Go source: version control, installed JS
// dependencies, and generated/vendored trees.
func skipDirForBareRequiredInputScan(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", "dist", "wailsjs", ".claude", ".claude-plugin", ".vscode":
		return true
	default:
		return false
	}
}

// bareRequiredInputHit is one banned literal found in production source.
type bareRequiredInputHit struct {
	position string
	value    string
}

func (h bareRequiredInputHit) Message() string {
	return fmt.Sprintf("%s: bare required-input error %q reintroduced — name the operation and a recovery instead (see erun-common/open.go's ErrOpenTenantNotProvided for the shape)", h.position, h.value)
}

// findBareRequiredInputLiterals parses every non-test .go file under root and
// returns one hit per exact-match banned string literal. AST-based rather
// than a text grep so a match inside a comment (not reachable at runtime)
// does not fail the gate.
func findBareRequiredInputLiterals(t testing.TB, root string) []bareRequiredInputHit {
	t.Helper()
	var hits []bareRequiredInputHit
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if path != root && skipDirForBareRequiredInputScan(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return fmt.Errorf("parse %s: %w", path, parseErr)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			value, unquoteErr := strconv.Unquote(lit.Value)
			if unquoteErr != nil || !bareRequiredInputPhrases[value] {
				return true
			}
			hits = append(hits, bareRequiredInputHit{position: fset.Position(lit.Pos()).String(), value: value})
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("scan repo for bare required-input literals: %v", err)
	}
	return hits
}

// TestNoBareRequiredInputError fails when a bare "<subject> is required"
// string literal (bareRequiredInputPhrases) reappears in non-test Go source
// anywhere in the repo.
func TestNoBareRequiredInputError(t *testing.T) {
	root := repoRoot(t)
	for _, hit := range findBareRequiredInputLiterals(t, root) {
		t.Errorf("%s", hit.Message())
	}
}

// TestFindBareRequiredInputLiteralsExclusions locks the three ways this
// scanner must stay quiet on synthetic data: a banned phrase inside a
// _test.go file (a test asserting the old message during a migration), a
// banned phrase inside a comment (never reachable at runtime), and an
// unrelated string that merely ends in "is required". Only a real
// production-code literal should be reported.
func TestFindBareRequiredInputLiteralsExclusions(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"production.go": `package fixture

func requireTenant(tenant string) error {
	if tenant == "" {
		return fmt.Errorf("tenant is required")
	}
	return nil
}
`,
		"production_test.go": `package fixture

// a test migrating off the old bare message still asserts it briefly:
// "tenant is required"
func TestOldMessage(t *testing.T) {}
`,
		"commented.go": `package fixture

// requireEnvironment used to just say "environment is required" before it
// named its operation.
func requireEnvironment(environment string) error {
	return nil
}
`,
		"unrelated.go": `package fixture

func requireAlias(alias string) error {
	if alias == "" {
		return fmt.Errorf("cloud provider alias is required")
	}
	return nil
}
`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	hits := findBareRequiredInputLiterals(t, dir)
	if len(hits) != 1 {
		t.Fatalf("expected exactly one hit (production.go's live literal), got %+v", hits)
	}
	if !strings.HasSuffix(hits[0].position, "production.go:5:21") {
		t.Fatalf("expected the hit to point at production.go's literal, got %q", hits[0].position)
	}
	if hits[0].value != "tenant is required" {
		t.Fatalf("expected the hit to carry the matched phrase, got %q", hits[0].value)
	}
}
