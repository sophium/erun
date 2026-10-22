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
		kind:   gatedByMakeTarget,
		target: "test-erun-common",
		reason: "the Makefile's test-erun-common target runs `cd erun-common && go test -race ./...`",
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
		kind:   gatedByMakeTarget,
		target: "test-playwright",
		reason: "the Makefile's test-playwright target runs erun-ui/playwright/run.sh against the built headless app",
	},
	"erun-console/playwright": {
		kind: deliberatelyExcluded,
		reason: "opt-in real-infrastructure end-to-end suites, each skipped unless the opt-in variable its own " +
			"runner sets is present; never part of the per-commit gate (erun-console/playwright/AGENTS.md). This " +
			"entry answers for the package as a whole; consolePlaywrightRunnerGates below answers for each runner " +
			"one level down",
	},
}

// consolePlaywrightRunnerGates classifies every end-to-end runner
// erun-console/playwright/package.json declares, one entry per runner script.
// The package-level entry above is keyed on the package, so it can only ever
// name one suite in prose, and prose is what nothing keeps complete: the
// package's other four opt-in runners (test:mcp-operate-scope,
// test:mcp-attach-session, test:rest-surfaces, test:landing-layout) were
// enumerated by no entry at all, so a sixth runner could ship without anyone
// deciding whether a gate should run it. Keying by the runner's own script
// rather than by yarn's script name keeps the table free of the `:headed`
// invocation variants, which are the same script with a flag and the same gate.
//
// The gate variable is the exclusion reason in checkable form: each script sets
// it only after standing up that suite's own dependencies, so a runner invoked
// without them stops rather than passing vacuously. The test verifies each pair
// against the script's real text instead of trusting the table, the same way the
// Makefile-backed entries above are verified against the Makefile's real text.
var consolePlaywrightRunnerGates = map[string]string{
	"./run.sh":                    "ERUN_E2E_CONSOLE_OIDC",
	"./run-mcp-operate-scope.sh":  "ERUN_E2E_CONSOLE_MCP_OPERATE",
	"./run-mcp-attach-session.sh": "ERUN_E2E_CONSOLE_MCP_ATTACH",
	"./run-rest-surfaces.sh":      "ERUN_E2E_CONSOLE_REST",
	"./run-landing-layout.sh":     "ERUN_E2E_CONSOLE_LANDING_LAYOUT",
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
	t.Parallel()
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

// TestConsolePlaywrightRunnersAreClassified is the runner-level half of the
// same contract. TestBuildCheckGateCoversEveryTestSuite above sees
// erun-console/playwright as one package, because one of its scripts is called
// "test" and packageJSONHasTestScript reads only that name -- so a runner added
// beside it is enumerated by nothing and can go silent exactly the way the four
// this table names did.
func TestConsolePlaywrightRunnersAreClassified(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	pkgDir := filepath.Join(root, "erun-console", "playwright")

	scripts, err := packageJSONScripts(filepath.Join(pkgDir, "package.json"))
	if err != nil {
		t.Fatal(err)
	}

	// declared maps each runner script back to the yarn script names that
	// invoke it, so a failure names the runner an operator would actually type.
	declared := make(map[string][]string)
	for name, command := range scripts {
		if name != "test" && !strings.HasPrefix(name, "test:") {
			continue
		}
		script, ok := runnerScriptPath(command)
		if !ok {
			t.Errorf("erun-console/playwright's %q runner (%s) does not invoke a \"./*.sh\" script -- this gate "+
				"cannot classify what it runs, so widen the table deliberately rather than leaving it unmatched",
				name, command)
			continue
		}
		declared[script] = append(declared[script], name)
	}
	if len(declared) == 0 {
		t.Fatal("erun-console/playwright/package.json declares no test runner -- the scan is misconfigured")
	}

	for script, runners := range declared {
		sort.Strings(runners)
		invoked := strings.Join(runners, ", ")
		gate, ok := consolePlaywrightRunnerGates[script]
		if !ok {
			t.Errorf("erun-console/playwright's %s runner(s) run %s, which consolePlaywrightRunnerGates does not "+
				"classify -- name the opt-in variable that gates it, or say why no gate should run it",
				invoked, script)
			continue
		}
		if gate == "" {
			t.Errorf("erun-console/playwright's %s runner(s) are classified with no opt-in variable, which gates "+
				"nothing", invoked)
			continue
		}
		body, readErr := os.ReadFile(filepath.Join(pkgDir, filepath.FromSlash(script)))
		if readErr != nil {
			t.Errorf("erun-console/playwright's %s runner(s) classify %s as gated on %s: %v", invoked, script, gate, readErr)
			continue
		}
		if !strings.Contains(string(body), gate) {
			t.Errorf("erun-console/playwright's %s runner(s) claim %s is gated on %s, but that script never names "+
				"it -- the classification does not match what the runner actually does", invoked, script, gate)
		}
	}

	var stale []string
	for script := range consolePlaywrightRunnerGates {
		if _, ok := declared[script]; !ok {
			stale = append(stale, script)
		}
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		t.Errorf("consolePlaywrightRunnerGates names runner scripts erun-console/playwright/package.json no "+
			"longer invokes (renamed or removed): %v", stale)
	}
}

// runnerScriptPath matches the leading "./<name>.sh" a runner's package.json
// command invokes, ignoring any flag arguments that follow it.
var runnerScriptPathPattern = regexp.MustCompile(`(\./[A-Za-z0-9_.\-]+\.sh)\b`)

// runnerScriptPath returns the runner script a package.json test command
// invokes, reporting false when the command has no such leading script.
func runnerScriptPath(command string) (string, bool) {
	m := runnerScriptPathPattern.FindStringSubmatch(command)
	if m == nil {
		return "", false
	}
	return m[1], true
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

// packageJSONScripts returns a package.json's declared script bodies, keyed by
// script name.
func packageJSONScripts(path string) (map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var manifest struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return manifest.Scripts, nil
}

func packageJSONHasTestScript(path string) (bool, error) {
	scripts, err := packageJSONScripts(path)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(scripts["test"]) != "", nil
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
