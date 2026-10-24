package mergequeue

import (
	"fmt"
	"strings"
	"testing"
)

func mergeRegressionFiles(files map[string]string) func(string) ([]byte, error) {
	return func(path string) ([]byte, error) {
		content, ok := files[path]
		if !ok {
			return nil, fmt.Errorf("open %s: no such file", path)
		}
		return []byte(content), nil
	}
}

const mergeRegressionReproduces = "Reproduces: the gates tab renders the empty state instead of panel.Error when ListGateRuns returns a 404\n"

var (
	mergeRegressionThingTest      = map[string]string{"erun-integration/thing_test.go": `t.Run("gate_runs_read_failure_shows_the_error", ...)`}
	mergeRegressionStagedWithTest = []string{"erun-common/thing.go", "erun-integration/thing_test.go"}
)

// mergeRegressionPolicyCases covers both what the policy refuses and what it lets through.
var mergeRegressionPolicyCases = []struct {
	name    string
	branch  string
	bodies  []string
	staged  []string
	files   map[string]string
	wantErr string
}{
	{
		name:   "a feature branch with no declaration is out of scope",
		branch: "feature/1-new-thing",
		bodies: []string{"Add the thing\n"},
		staged: []string{"erun-common/thing.go"},
	},
	{
		name:   "Defect-Fix: no takes a bug/ branch out of scope",
		branch: "bug/2123-thing",
		bodies: []string{"Land it\n\nDefect-Fix: no\n"},
		staged: []string{"erun-common/thing.go"},
	},
	{
		name:    "Defect-Fix: yes brings a feature branch into scope",
		branch:  "feature/2123-thing",
		bodies:  []string{"Fix it\n\nDefect-Fix: yes\n"},
		staged:  []string{"erun-common/thing.go"},
		wantErr: `no "Regression-Test:" trailer`,
	},
	{
		name:    "a defect fix that adds no test is refused",
		branch:  "bug/9999-a-defect",
		bodies:  []string{"Fix the thing\n"},
		staged:  []string{"erun-common/thing.go"},
		wantErr: `no "Regression-Test:" trailer`,
	},
	{
		name:    "a defect fix that adds a test but names none is still refused",
		branch:  "bug/9999-a-defect",
		bodies:  []string{"Fix the thing\n\n" + mergeRegressionReproduces},
		staged:  mergeRegressionStagedWithTest,
		files:   mergeRegressionThingTest,
		wantErr: `no "Regression-Test:" trailer`,
	},
	{
		name:    "a named test without a Reproduces: line is refused",
		branch:  "bug/9999-a-defect",
		bodies:  []string{"Fix\n\nRegression-Test: erun-integration/thing_test.go::gate_runs_read_failure_shows_the_error\n"},
		staged:  mergeRegressionStagedWithTest,
		files:   mergeRegressionThingTest,
		wantErr: `needs a "Reproduces:" trailer`,
	},
	{
		name:    "naming a test this change did not touch is refused",
		branch:  "bug/9999-a-defect",
		bodies:  []string{"Fix it\n\n" + mergeRegressionReproduces + "Regression-Test: erun-integration/thing_test.go::gate_runs_read_failure_shows_the_error\n"},
		staged:  []string{"erun-common/thing.go"},
		files:   mergeRegressionThingTest,
		wantErr: "is not added or modified by this change",
	},
	{
		name:    "naming a case that is not in the file is refused",
		branch:  "bug/9999-a-defect",
		bodies:  []string{"Fix it\n\n" + mergeRegressionReproduces + "Regression-Test: erun-integration/thing_test.go::case_that_does_not_exist\n"},
		staged:  mergeRegressionStagedWithTest,
		files:   mergeRegressionThingTest,
		wantErr: `does not contain the case "case_that_does_not_exist"`,
	},
	{
		name:    "naming a non-test file is refused",
		branch:  "bug/9999-a-defect",
		bodies:  []string{"Fix it\n\n" + mergeRegressionReproduces + "Regression-Test: erun-common/thing.go::Foo\n"},
		staged:  []string{"erun-common/thing.go"},
		files:   map[string]string{"erun-common/thing.go": "func Foo() {}"},
		wantErr: "is not a test file",
	},
	{
		name:    "a declaration without the <path>::<case> form is refused",
		branch:  "bug/9999-a-defect",
		bodies:  []string{"Fix it\n\n" + mergeRegressionReproduces + "Regression-Test: erun-integration/thing_test.go\n"},
		staged:  mergeRegressionStagedWithTest,
		files:   mergeRegressionThingTest,
		wantErr: "is not in the <path>::<case> form",
	},
	{
		name:    "a path outside the repository is refused before it is read",
		branch:  "bug/9999-a-defect",
		bodies:  []string{"Fix it\n\n" + mergeRegressionReproduces + "Regression-Test: none\nRegression-Test-Exemption: covered-by-existing: the scenario already drives it\nRegression-Test-Existing: ../outside_test.go::case\n"},
		staged:  []string{"erun-common/thing.go"},
		wantErr: "is not a repository-relative path",
	},
	{
		name:    "declaring both none and a named test is refused",
		branch:  "bug/9999-a-defect",
		bodies:  []string{"Fix\n\n" + mergeRegressionReproduces + "Regression-Test: none\nRegression-Test: erun-integration/thing_test.go::gate_runs_read_failure_shows_the_error\n"},
		staged:  mergeRegressionStagedWithTest,
		files:   mergeRegressionThingTest,
		wantErr: "pick one",
	},
	{
		name:   "a properly declared defect fix passes, with its declaration spread over commits",
		branch: "bug/9999-a-defect",
		bodies: []string{
			"Add the failing case\n\nRegression-Test: erun-integration/thing_test.go::gate_runs_read_failure_shows_the_error\n",
			"Fix it\n\n" + mergeRegressionReproduces,
		},
		staged: mergeRegressionStagedWithTest,
		files:  mergeRegressionThingTest,
	},
	{
		name:   "a hard-wrapped case name resolves whole",
		branch: "bug/9999-a-defect",
		bodies: []string{"Fix it\n\n" + mergeRegressionReproduces + "Regression-Test: erun-ui/playwright/tests/card.spec.ts::a hover card dropped while the boot\nlands is re-hovered\n"},
		staged: []string{"erun-ui/playwright/tests/card.spec.ts"},
		files:  map[string]string{"erun-ui/playwright/tests/card.spec.ts": "test('a hover card dropped while the boot lands is re-hovered', ...)"},
	},
	{
		name:   "a docs-only exemption passes when only documentation is staged",
		branch: "bug/9999-a-defect",
		bodies: []string{"Correct the page\n\n" + mergeRegressionReproduces + "Regression-Test: none\nRegression-Test-Exemption: docs-only: the page described the chart default backwards\n"},
		staged: []string{"erun-docs/docs/deployment/data-retention.md", "erun-docs/static/img/retention.png"},
	},
	{
		name:    "a docs-only exemption is refused when an executable file is staged",
		branch:  "bug/9999-a-defect",
		bodies:  []string{"Correct the page\n\n" + mergeRegressionReproduces + "Regression-Test: none\nRegression-Test-Exemption: docs-only: the page described the chart default backwards\n"},
		staged:  []string{"erun-docs/docs/deployment/data-retention.md", "erun-common/retention.go"},
		wantErr: "non-documentation file erun-common/retention.go",
	},
	{
		name:   "a revert exemption passes when a commit says what it reverts",
		branch: "bug/9999-a-defect",
		bodies: []string{"Revert \"Add the thing\"\n\nThis reverts commit 0123456789abcdef.\n\n" + mergeRegressionReproduces + "Regression-Test: none\nRegression-Test-Exemption: revert: the reverted change broke chart rendering on arm64\n"},
		staged: []string{"erun-common/thing.go"},
	},
	{
		name:    "a revert exemption is refused when no commit is a revert",
		branch:  "bug/9999-a-defect",
		bodies:  []string{"Not a revert\n\n" + mergeRegressionReproduces + "Regression-Test: none\nRegression-Test-Exemption: revert: the reverted change broke chart rendering on arm64\n"},
		staged:  []string{"erun-common/thing.go"},
		wantErr: `"This reverts commit <sha>"`,
	},
	{
		name:   "covered-by-existing passes on a resolving test that need not be staged",
		branch: "bug/9999-a-defect",
		bodies: []string{"Fix\n\n" + mergeRegressionReproduces + "Regression-Test: none\nRegression-Test-Exemption: covered-by-existing: the scenario already drives this registry disagreement\nRegression-Test-Existing: erun-integration/thing_test.go::gate_runs_read_failure_shows_the_error\n"},
		staged: []string{"erun-common/thing.go"},
		files:  mergeRegressionThingTest,
	},
	{
		name:    "covered-by-existing is refused without a Regression-Test-Existing",
		branch:  "bug/9999-a-defect",
		bodies:  []string{"Fix\n\n" + mergeRegressionReproduces + "Regression-Test: none\nRegression-Test-Exemption: covered-by-existing: the scenario already drives this registry disagreement\n"},
		staged:  []string{"erun-common/thing.go"},
		wantErr: `needs "Regression-Test-Existing:`,
	},
	{
		name:    "covered-by-existing is refused when the existing case does not resolve",
		branch:  "bug/9999-a-defect",
		bodies:  []string{"Fix\n\n" + mergeRegressionReproduces + "Regression-Test: none\nRegression-Test-Exemption: covered-by-existing: the scenario already drives this registry disagreement\nRegression-Test-Existing: erun-integration/missing_test.go::some_case\n"},
		staged:  []string{"erun-common/thing.go"},
		wantErr: "Regression-Test-Existing: \"erun-integration/missing_test.go\" cannot be read",
	},
	{
		name:    "none without an exemption is refused",
		branch:  "bug/9999-a-defect",
		bodies:  []string{"Fix\n\n" + mergeRegressionReproduces + "Regression-Test: none\n"},
		staged:  []string{"erun-common/thing.go"},
		wantErr: `needs "Regression-Test-Exemption: <kind>: <reason>"`,
	},
	{
		name:    "an exemption kind outside the closed set is refused",
		branch:  "bug/9999-a-defect",
		bodies:  []string{"Fix\n\n" + mergeRegressionReproduces + "Regression-Test: none\nRegression-Test-Exemption: too-hard: it would take a while to write a test\n"},
		staged:  []string{"erun-common/thing.go"},
		wantErr: `unknown exemption kind "too-hard"`,
	},
	{
		name:    "a no-reproducible-failure reason too thin to weigh is refused",
		branch:  "bug/9999-a-defect",
		bodies:  []string{"Fix\n\n" + mergeRegressionReproduces + "Regression-Test: none\nRegression-Test-Exemption: no-reproducible-failure: n/a\n"},
		staged:  []string{"erun-common/thing.go"},
		wantErr: "at least 30 characters",
	},
	{
		name:   "a no-reproducible-failure with a real reason passes",
		branch: "bug/9999-a-defect",
		bodies: []string{"Fix\n\n" + mergeRegressionReproduces + "Regression-Test: none\nRegression-Test-Exemption: no-reproducible-failure: the wrong glyph baseline is only visible to a human reading the titlebar\n"},
		staged: []string{"erun-common/thing.go"},
	},
}

func TestEvaluateMergeRegressionPolicy(t *testing.T) {
	for _, tt := range mergeRegressionPolicyCases {
		t.Run(tt.name, func(t *testing.T) {
			err := evaluateMergeRegressionPolicy(tt.branch, tt.bodies, tt.staged, mergeRegressionFiles(tt.files))
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("expected the policy to hold, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected an error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}
