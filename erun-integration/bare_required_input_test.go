package integration

// Gate for root AGENTS.md "Smooth, Seamless, No Dead Ends": fails when a bare
// tenant/environment "is required" error reappears in non-test Go source.

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

// bareRequiredInputPattern matches the shape, not fixed literals, so reorderings
// and plurals cannot slip through. Widen it only for phrases that are just as bare.
var bareRequiredInputPattern = regexp.MustCompile(`^(?:tenant|environment)(?: and (?:tenant|environment))? (?:is|are) required$`)

// bareRequiredInputBaseline is shrink-only; keyed by repo-relative path.
var bareRequiredInputBaseline = map[string]int{}

func skipDirForBareRequiredInputScan(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", "dist", "wailsjs", ".claude", ".claude-plugin", ".vscode":
		return true
	default:
		return false
	}
}

type bareRequiredInputHit struct {
	position string
	file     string // path relative to the scan root, forward-slash separated
	value    string
}

func (h bareRequiredInputHit) Message() string {
	return fmt.Sprintf("%s: bare required-input error %q reintroduced — name the operation and a recovery instead (see erun-common/open.go's ErrOpenTenantNotProvided for the shape)", h.position, h.value)
}

// findBareRequiredInputLiterals is AST-based so matches in comments are ignored.
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
		hits = append(hits, bareRequiredInputLiteralsInFile(fset, file, filepath.ToSlash(rel))...)
		return nil
	})
	if err != nil {
		t.Fatalf("scan repo for bare required-input literals: %v", err)
	}
	return hits
}

func bareRequiredInputLiteralsInFile(fset *token.FileSet, file *ast.File, rel string) []bareRequiredInputHit {
	var hits []bareRequiredInputHit
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
	return hits
}

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

// A baselined file absent from root is skipped: a narrowed build context may
// omit it.
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
