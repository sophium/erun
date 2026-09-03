package integration

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// A Go module or Yarn package with a real test suite protects nothing until
// `make check` actually runs it. erun-mcp shipped 282 passing test cases
// reachable by no gate for exactly the reason erun-backend-api's ldflags
// regression and erun-kit's/erun-ui/frontend's own `yarn test` did before
// them: each module was left out of the Makefile's dependency graph by
// omission, not by a reviewed decision, and nobody noticed because the tests
// still passed whenever a contributor happened to run them by hand.
//
// This gate makes a fifth instance of that class impossible. It enumerates
// every real Go module and Yarn package in the checkout that has its own
// tests, and requires an entry in buildCheckCoverage classifying how `make
// check` reaches it: a Makefile target that is actually wired into
// `check-gate` and whose own recipe actually references the module (verified
// against the Makefile's real text, not just asserted), or a documented,
// deliberate reason `make check` must not run it. Modeled on
// erun-backend-api's tenant_scope_test.go: an omission is a build failure,
// not a silent gap, and an exclusion has to name itself and say why.
type coverageKind int

const (
	// gatedByMakeTarget means target names a Makefile target that is a
	// prerequisite of check-gate and whose own recipe references this
	// module's directory, so `make check` actually runs its tests.
	gatedByMakeTarget coverageKind = iota
	// deliberatelyExcluded means this module's tests are intentionally not
	// run by `make check`, for the reason recorded alongside the entry.
	deliberatelyExcluded
)

func (k coverageKind) String() string {
	switch k {
	case gatedByMakeTarget:
		return "gatedByMakeTarget"
	case deliberatelyExcluded:
		return "deliberatelyExcluded"
	default:
		return "unknown"
	}
}

type coverageEntry struct {
	kind   coverageKind
	target string
	reason string
}

var buildCheckCoverage = map[string]coverageEntry{
	"erun-mcp": {
		kind:   gatedByMakeTarget,
		target: "test-erun-mcp",
		reason: "the Makefile's test-erun-mcp target runs `cd erun-mcp && go test ./...`",
	},
	"erun-backend/erun-backend-api": {
		kind:   gatedByMakeTarget,
		target: "test-erun-backend-api",
		reason: "the Makefile's test-erun-backend-api target runs `cd erun-backend/erun-backend-api && go test ./...`",
	},
	"erun-ui": {
		kind:   gatedByMakeTarget,
		target: "test-erun-ui",
		reason: "the Makefile's test-erun-ui target runs `cd erun-ui && go test -race ./...`",
	},
	"erun-integration": {
		kind:   gatedByMakeTarget,
		target: "integration-test-gate",
		reason: "the Makefile's integration-test-gate target runs erun-integration/scripts/integration-test.sh, which runs `go test ./...` from this module",
	},
	"erun-devops/dns01-webhook": {
		kind:   gatedByMakeTarget,
		target: "test-erun-dns01-webhook",
		reason: "the Makefile's test-erun-dns01-webhook target runs `cd erun-devops/dns01-webhook && go test ./...`",
	},
	"erun-cli": {
		kind: deliberatelyExcluded,
		reason: "erun-cli/AGENTS.md's Validation section: CLI behavior is gated end-to-end by this suite driving the " +
			"compiled binary, not by this module's own unit tests -- a unit test overlapping an integration scenario " +
			"is deleted, not carried alongside it",
	},
	"erun-common": {
		kind: deliberatelyExcluded,
		reason: "erun-common/AGENTS.md's Validation section: erun-common behavior is gated end-to-end by this suite, " +
			"not by this module's own unit tests -- a unit test overlapping an integration scenario is deleted, not " +
			"carried alongside it",
	},
	"erun-kit": {
		kind:   gatedByMakeTarget,
		target: "test-frontend",
		reason: "the Makefile's test-frontend target runs `cd erun-kit && ... && yarn test`",
	},
	"erun-console": {
		kind:   gatedByMakeTarget,
		target: "test-frontend",
		reason: "the Makefile's test-frontend target runs `cd erun-console && ... && yarn test`",
	},
	"erun-ui/frontend": {
		kind:   gatedByMakeTarget,
		target: "test-frontend",
		reason: "the Makefile's test-frontend target runs `cd erun-ui/frontend && ... && yarn test`",
	},
	"erun-ui/playwright": {
		kind: deliberatelyExcluded,
		reason: "needs a built desktop app and a real k3d cluster; runs on its own schedule, never in the per-commit " +
			"gate (root AGENTS.md's Makefile comment; erun-ui/playwright/AGENTS.md)",
	},
	"erun-console/playwright": {
		kind: deliberatelyExcluded,
		reason: "opt-in real-Zitadel OIDC end-to-end suite, skipped unless ERUN_E2E_CONSOLE_OIDC=1 (set only by its " +
			"own run.sh after standing up the stack); never part of the per-commit gate (erun-console/playwright/AGENTS.md)",
	},
}

// skipDirNames are directories this gate never descends into: version
// control metadata, and generated/vendored content that carries no committed
// source of its own (node_modules, a frontend build's dist output, the
// integration suite's own coverage scratch dir, erun-ui/frontend's
// gitignored Wails bindings, and a coding agent's own gitignored worktree
// scratch space, which can hold a full second checkout of every module).
var skipDirNames = map[string]bool{
	".git":         true,
	".claude":      true,
	"node_modules": true,
	"wailsjs":      true,
	"dist":         true,
	"coverage":     true,
}

// TestBuildCheckGateCoversEveryTestSuite is the structural half of the
// contract described above. It does not run make itself: the only reliable
// signal is "does a module with real tests have a reviewed answer for how
// `make check` reaches it" -- verifying the *content* of that answer against
// the Makefile's real text is what stops the answer from silently going
// stale, the same way tenantScopeClassification's staleness check does.
func TestBuildCheckGateCoversEveryTestSuite(t *testing.T) {
	root := repoRoot(t)

	found := append(goModulesWithTests(t, root), jsPackagesWithTests(t, root)...)
	sort.Strings(found)
	if len(found) == 0 {
		t.Fatal("found no Go modules or Yarn packages with their own tests -- the scan is misconfigured")
	}

	makefileText := readMakefile(t, root)
	checkGatePrereqs := makeTargetPrerequisites(t, makefileText, "check-gate")

	for _, name := range found {
		entry, ok := buildCheckCoverage[name]
		if !ok {
			t.Errorf("%s has its own tests but no entry in buildCheckCoverage -- classify it as %s (name the "+
				"Makefile target that runs its tests) or %s (say why `make check` must not run them)",
				name, gatedByMakeTarget, deliberatelyExcluded)
			continue
		}

		switch entry.kind {
		case gatedByMakeTarget:
			if entry.target == "" {
				t.Errorf("%s is classified %s but names no target", name, gatedByMakeTarget)
				continue
			}
			if !containsString(checkGatePrereqs, entry.target) {
				t.Errorf("%s claims target %q, but %q is not a prerequisite of check-gate in the Makefile -- "+
					"a target that check-gate never reaches does not run in `make check`", name, entry.target, entry.target)
				continue
			}
			recipe := makeTargetRecipe(t, makefileText, entry.target)
			if !strings.Contains(recipe, name) {
				t.Errorf("%s claims target %q, but that target's own recipe in the Makefile never references %q -- "+
					"the classification does not match what the Makefile actually runs", name, entry.target, name)
			}
		case deliberatelyExcluded:
			if strings.TrimSpace(entry.reason) == "" {
				t.Errorf("%s is classified %s with no reason", name, deliberatelyExcluded)
			}
		default:
			t.Errorf("%s has an unrecognized coverageKind %v", name, entry.kind)
		}
	}

	foundSet := make(map[string]bool, len(found))
	for _, name := range found {
		foundSet[name] = true
	}
	var stale []string
	for name := range buildCheckCoverage {
		if !foundSet[name] {
			stale = append(stale, name)
		}
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		t.Errorf("buildCheckCoverage names modules/packages that no longer have their own tests (renamed, removed, "+
			"or tests deleted): %v", stale)
	}
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// goModulesWithTests returns the repo-root-relative directory of every Go
// module (a directory holding a go.mod) that has at least one *_test.go file
// of its own -- a module with no tests has nothing this gate needs to see
// reached, and correctly needs no classification.
func goModulesWithTests(t testing.TB, root string) []string {
	t.Helper()
	var modules []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && skipDirNames[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != "go.mod" {
			return nil
		}
		modDir := filepath.Dir(path)
		hasTests, herr := moduleHasOwnTestFiles(modDir)
		if herr != nil {
			return herr
		}
		if !hasTests {
			return nil
		}
		rel, relErr := filepath.Rel(root, modDir)
		if relErr != nil {
			return relErr
		}
		modules = append(modules, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return modules
}

// moduleHasOwnTestFiles reports whether modDir's own tree contains a
// *_test.go file, stopping at any nested module boundary (a subdirectory
// that itself holds a go.mod) since a nested module's tests are not part of
// `go test ./...` run from modDir.
func moduleHasOwnTestFiles(modDir string) (bool, error) {
	found := false
	err := filepath.WalkDir(modDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if found {
			return filepath.SkipAll
		}
		if d.IsDir() {
			if path == modDir {
				return nil
			}
			if skipDirNames[d.Name()] {
				return filepath.SkipDir
			}
			if _, statErr := os.Stat(filepath.Join(path, "go.mod")); statErr == nil {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), "_test.go") {
			found = true
		}
		return nil
	})
	return found, err
}

// jsPackagesWithTests returns the repo-root-relative directory of every
// package.json declaring a non-empty "test" script.
func jsPackagesWithTests(t testing.TB, root string) []string {
	t.Helper()
	var packages []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && skipDirNames[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != "package.json" {
			return nil
		}
		hasTest, perr := packageJSONHasTestScript(path)
		if perr != nil {
			return perr
		}
		if !hasTest {
			return nil
		}
		rel, relErr := filepath.Rel(root, filepath.Dir(path))
		if relErr != nil {
			return relErr
		}
		packages = append(packages, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return packages
}

func packageJSONHasTestScript(path string) (bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	var manifest struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return false, fmt.Errorf("%s: %w", path, err)
	}
	return strings.TrimSpace(manifest.Scripts["test"]) != "", nil
}

func readMakefile(t testing.TB, root string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// makeTargetDefinition matches a real "target: prerequisites" line -- never
// a variable assignment (`:=`/`?=`/`+=` always have a space before the
// operator in this Makefile, so they never match a bare-colon target line)
// or a recipe line (which always starts with a tab). This is the same
// plain-text "read the source, don't execute it" approach the other
// structural gates in this file use, not a make(1) evaluator.
var makeTargetDefinition = regexp.MustCompile(`^([A-Za-z0-9_.\-/]+):\s*(.*)$`)

// makeTargetPrerequisites returns the whitespace-separated prerequisite list
// from target's own "target: prereq1 prereq2" line.
func makeTargetPrerequisites(t testing.TB, makefileText, target string) []string {
	t.Helper()
	for _, line := range strings.Split(makefileText, "\n") {
		if strings.HasPrefix(line, "#") {
			continue
		}
		m := makeTargetDefinition.FindStringSubmatch(line)
		if m == nil || m[1] != target {
			continue
		}
		return strings.Fields(m[2])
	}
	t.Fatalf("Makefile has no %q target", target)
	return nil
}

// makeTargetRecipe returns the tab-indented recipe body immediately
// following target's own definition line, joined back into one string so a
// caller can search it for a module path.
func makeTargetRecipe(t testing.TB, makefileText, target string) string {
	t.Helper()
	lines := strings.Split(makefileText, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "#") {
			continue
		}
		m := makeTargetDefinition.FindStringSubmatch(line)
		if m == nil || m[1] != target {
			continue
		}
		var recipe strings.Builder
		for _, next := range lines[i+1:] {
			if !strings.HasPrefix(next, "\t") {
				break
			}
			recipe.WriteString(next)
			recipe.WriteByte('\n')
		}
		return recipe.String()
	}
	t.Fatalf("Makefile has no %q target", target)
	return ""
}
