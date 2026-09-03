package integration

// bare_required_input_test.go is a repo-state structural gate for root
// AGENTS.md § "Smooth, Seamless, No Dead Ends": a validation failure that
// names only a generic missing-input phrase, with no operation and no
// recovery, is exactly failure mode 1 ("advice that cannot work" — here,
// no advice at all). An audit once found 13 production call sites emitting
// exactly `fmt.Errorf("tenant is required")`, plus two siblings in the same
// erun-common function, after an earlier fix addressed one lone producer and
// closed its own follow-up audit item unperformed. This is the check that
// closes the gap: it fails the build the next time one of these bare
// phrases reappears in non-test Go source anywhere in the repo. It runs as
// an ordinary `go test` in this module, so it is part of
// `make integration-test`/`make check` with no extra wiring — the same
// precedent as desktop_surface_test.go.
//
// erun-mcp's exec_agent shipped uncallable because its target-resolution
// failure used a THIRD phrasing, "tenant and environment are required" —
// the original gate matched only the two literal strings above, so this
// exact defect walked straight through it. Matching against a fixed set of
// literals will always be one phrasing behind; bareRequiredInputPattern
// below matches the *shape* instead (any bare combination of "tenant"
// and/or "environment", singular or plural, in either order), so a future
// reordering or pluralization cannot slip through the same way.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// bareRequiredInputPattern matches a bare "<subject> is/are required" with no
// operation and no recovery, for the closed set of generic nouns this gate
// tracks: "tenant" and "environment", alone or combined via "and". This is a
// deliberately narrow generalization -- from an enumeration of exact
// literals to a shape covering their permutations -- not a blanket "any
// subject is required" scanner. The fix that added this gate converted every
// "tenant is required" producer plus its immediate "environment is
// required" sibling in the same erun-common function, and deliberately did
// not chase every "<X> is required" string in the repo (on the order of 150
// at the time of writing, including several more "cloud provider alias is
// required" sites that same audit found but left alone). Most of the rest
// already name their own specific subject (e.g. "review id is required",
// "target branch is required", "cloud provider alias is required") and are
// a lower-severity instance of the same class, not the "four words, no
// operation, no subject beyond a generic noun, no recovery" shape this gate
// targets. Widen this pattern's subject set only for a phrase that
// genuinely matches that shape on a reachable path — not for every
// validation string that happens to end in "is required".
var bareRequiredInputPattern = regexp.MustCompile(`^(?:tenant|environment)(?: and (?:tenant|environment))? (?:is|are) required$`)

// bareRequiredInputBaseline is a shrink-only baseline (the same pattern as
// KnownUnsurfacedRoutes in erun-backend-api/internal/routes/route_audit.go)
// for the "tenant and environment are required" hits that widening the
// pattern above newly caught: 65 pre-existing call sites across erun-cli,
// erun-common, and erun-ui, none of them reachable from the exec_agent bug
// this gate change was made for. All 65 have since been rewritten to name
// their own operation and recovery (see erun-common's errMissingTenantOrEnvironment
// and erun-ui's errMissingTenantOrEnvironment), so the baseline is empty: it
// stays declared, rather than deleted, so a reintroduced bare literal in one
// of these files gets zero tolerance like any other file, and so a future
// widening of this baseline documents the same shrink-only contract. This
// baseline may only shrink: fixing a site and forgetting to remove its entry
// here fails TestBareRequiredInputBaselineIsCurrent below, the same way a
// stale KnownUnsurfacedRoutes entry fails its own gate. The key is the file
// path relative to the repo root; the value is the exact count of matching
// literals still in that file.
var bareRequiredInputBaseline = map[string]int{}

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
	file     string // path relative to the scan root, forward-slash separated
	value    string
}

func (h bareRequiredInputHit) Message() string {
	return fmt.Sprintf("%s: bare required-input error %q reintroduced — name the operation and a recovery instead (see erun-common/open.go's ErrOpenTenantNotProvided for the shape)", h.position, h.value)
}

// findBareRequiredInputLiterals parses every non-test .go file under root and
// returns one hit per string literal matching bareRequiredInputPattern.
// AST-based rather than a text grep so a match inside a comment (not
// reachable at runtime) does not fail the gate.
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
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return fmt.Errorf("relativize %s: %w", path, relErr)
		}
		rel = filepath.ToSlash(rel)
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			value, unquoteErr := strconv.Unquote(lit.Value)
			if unquoteErr != nil || !bareRequiredInputPattern.MatchString(value) {
				return true
			}
			hits = append(hits, bareRequiredInputHit{position: fset.Position(lit.Pos()).String(), file: rel, value: value})
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("scan repo for bare required-input literals: %v", err)
	}
	return hits
}

// TestNoBareRequiredInputError fails when a bare tenant/environment
// required-input literal (bareRequiredInputPattern) reappears in non-test Go
// source anywhere in the repo, beyond what bareRequiredInputBaseline already
// carries for a given file. A file with no baseline entry gets zero
// tolerance -- any hit there is a brand new instance of the bug. A file with
// a baseline entry may not exceed it: that would be a new call site added
// next to the tracked ones, not fixing them.
func TestNoBareRequiredInputError(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	counts := map[string]int{}
	for _, hit := range findBareRequiredInputLiterals(t, root) {
		counts[hit.file]++
		if counts[hit.file] > bareRequiredInputBaseline[hit.file] {
			t.Errorf("%s", hit.Message())
		}
	}
}

// TestBareRequiredInputBaselineIsCurrent fails when a baselined file's actual
// hit count has dropped below what bareRequiredInputBaseline still claims --
// the same shrink-only enforcement FindStaleBaselineEntries applies to
// KnownUnsurfacedRoutes. A fix that lowers a file's count without lowering
// its baseline entry here would otherwise let the debt silently look larger
// than it is forever.
//
// A baselined file absent from root is skipped rather than compared, the
// same reasoning as issueReferenceBaseline's own baseline-is-current check:
// a narrowed tree (a container build context COPYing a subset of the repo)
// can legitimately omit a file a full checkout has, and a walker cannot tell
// "cleaned up" from "not here" by hit count alone. The full-checkout run
// still enforces the shrink-only contract for every file, since only there
// do all baselined files exist to compare.
func TestBareRequiredInputBaselineIsCurrent(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	counts := map[string]int{}
	for _, hit := range findBareRequiredInputLiterals(t, root) {
		counts[hit.file]++
	}
	for file, baseline := range bareRequiredInputBaseline {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(file))); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			t.Fatalf("stat %s: %v", file, err)
		}
		if actual := counts[file]; actual < baseline {
			t.Errorf("%s: bareRequiredInputBaseline claims %d hit(s) but only %d remain -- lower the baseline entry (see https://github.com/sophium/erun/issues/1506)", file, baseline, actual)
		}
	}
}

// TestFindBareRequiredInputLiteralsExclusions locks the three ways this
// scanner must stay quiet on synthetic data: a banned phrase inside a
// _test.go file (a test asserting the old message during a migration), a
// banned phrase inside a comment (never reachable at runtime), and an
// unrelated string that merely ends in "is required". Only a real
// production-code literal should be reported.
func TestFindBareRequiredInputLiteralsExclusions(t *testing.T) {
	t.Parallel()
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
	if hits[0].file != "production.go" {
		t.Fatalf("expected the hit to carry the relative file path, got %q", hits[0].file)
	}
}

// TestBareRequiredInputPatternCatchesShapeVariants is the regression for the
// bug this gate widening exists to fix: exec_agent's target resolution
// failed with "tenant and environment are required", a third phrasing the
// old literal-enumeration gate did not know about. This locks that the
// pattern generalizes over conjunction order and singular/plural, not just
// the two original literals, while still leaving an unrelated
// specifically-named subject alone.
func TestBareRequiredInputPatternCatchesShapeVariants(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"tenant is required", true},
		{"environment is required", true},
		{"tenant and environment are required", true},
		{"environment and tenant are required", true},
		{"tenant and environment is required", true},
		{"review id is required", false},
		{"cloud provider alias is required", false},
		{"tenant and environment are required to deploy", false},
	} {
		if got := bareRequiredInputPattern.MatchString(tc.value); got != tc.want {
			t.Errorf("bareRequiredInputPattern.MatchString(%q) = %v, want %v", tc.value, got, tc.want)
		}
	}
}
