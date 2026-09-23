package eruncommon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	// The Makefile target that runs the published modules' own `terraform
	// test` suites, and the script its recipe runs.
	terraformModuleTestsTarget = "terraform-module-tests"
	terraformModuleTestsScript = "scripts/terraform-module-tests.sh"
	// The root expression that script resolves its module tree through. It is
	// asserted literally rather than as a substring of the tree's path because
	// dockerfile_copy_contract_test.go's script half reads exactly this shape
	// (`${script_dir}/../<path>`) to decide the image stage provides the
	// directory: rewrite the root resolution into something the regex cannot
	// bind and that guard keeps passing while checking nothing, which is the
	// silent half of the same defect this test exists for.
	terraformModuleTestsScriptRoot = "${script_dir}/../erun-devops/terraform-erun/modules"
)

// TestTerraformModuleTestsGateCarriesItsToolchainInTheDevopsImage locks the two
// halves of the terraform-module-tests wiring that no other guard covers.
//
// The suites under erun-devops/terraform-erun/modules/*/tests are the only
// enforcement of invariants the published modules are consumed for, and no
// target in this repository ran them -- so a module edit that dropped one
// applied cleanly with the whole gate green beside it. Wiring them in is only
// worth anything if both venues run them, and those two fail in opposite
// directions: a target missing from check-gate leaves a pod-side `make check`
// silently green, while a target the erun-devops image test stage cannot run
// reds inside every image build and blocks every release -- the venue a
// pod-side gate cannot see, and the one that has already cost this repository a
// release.
//
// The tree, the script file and the target count are covered elsewhere
// (TestCheckGateScriptsResolveOnlyDirectoriesTheDevopsImageProvides and
// TestCheckGateTargetCountMatchesItsPrerequisites). What no guard could see is
// the binary: `terraform` is invoked by name, from PATH, inside a stage the
// Makefile never mentions. helm, atlas and node are in the same position today
// and stay outside this test -- this covers the toolchain this change adds
// rather than inventing a general inventory of the stage's contents.
func TestTerraformModuleTestsGateCarriesItsToolchainInTheDevopsImage(t *testing.T) {
	root := repoRootForDockerignoreTest(t)
	requireTerraformModuleTestsIsWired(t, root)
	requireTerraformModuleTestsScriptIsReal(t, root)
	requireGateStageInstallsTerraform(t, root)
}

// requireTerraformModuleTestsIsWired checks the Makefile half: the target exists,
// check-gate reaches it, and its recipe runs the script the rest of this test
// reads.
func requireTerraformModuleTestsIsWired(t *testing.T, root string) {
	t.Helper()
	rules := makefileRules(t, root)
	gate, ok := rules["check-gate"]
	if !ok {
		t.Fatal("Makefile declares no check-gate target")
	}
	wired := false
	for _, prereq := range gate.prereqs {
		if prereq == terraformModuleTestsTarget {
			wired = true
		}
	}
	if !wired {
		t.Errorf("check-gate does not list %s among its prerequisites (%s): the modules' `terraform test` "+
			"suites are then run by no target in this repository again, which is the state they sat in "+
			"while they rotted -- a module edit that drops an invariant they pin lands green",
			terraformModuleTestsTarget, strings.Join(gate.prereqs, " "))
	}
	recipe := strings.Join(rules[terraformModuleTestsTarget].recipe, "\n")
	if !strings.Contains(recipe, terraformModuleTestsScript) {
		t.Errorf("the %s recipe does not run %s, so the wiring above points at a target that no longer "+
			"holds the module loop", terraformModuleTestsTarget, terraformModuleTestsScript)
	}
}

// requireTerraformModuleTestsScriptIsReal checks the loop's own half: it runs
// the suites rather than merely existing, and it resolves its module tree
// through the expression the copy-contract guard binds to.
func requireTerraformModuleTestsScriptIsReal(t *testing.T, root string) {
	t.Helper()
	script, err := os.ReadFile(filepath.Join(root, terraformModuleTestsScript))
	if err != nil {
		t.Fatalf("read %s: %v", terraformModuleTestsScript, err)
	}
	if !strings.Contains(string(script), "terraform test") {
		t.Errorf("%s never invokes `terraform test`, so the target it is reached through gates nothing",
			terraformModuleTestsScript)
	}
	if !strings.Contains(string(script), terraformModuleTestsScriptRoot) {
		t.Errorf("%s no longer resolves its module tree through %q: the copy-contract guard binds its "+
			"directory scan to exactly that expression, so another root shape silently leaves the image "+
			"stage's COPY of the tree unchecked", terraformModuleTestsScript, terraformModuleTestsScriptRoot)
	}
}

// requireGateStageInstallsTerraform checks the binary. The stage is found by
// what it runs rather than by name, the same model the copy-contract guard
// uses, and comment lines are skipped for the same reason it skips them: the
// prose above this stage's install names terraform too, and matching prose
// would pass this test with no install in the stage at all.
func requireGateStageInstallsTerraform(t *testing.T, root string) {
	t.Helper()
	stagePath := filepath.Join(root, "erun-devops", "docker", "erun-devops", "Dockerfile")
	for _, line := range dockerfileGateStageLines(t, stagePath) {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.Contains(trimmed, "/usr/local/bin/terraform") {
			return
		}
	}
	t.Errorf("the erun-devops image's gate stage (%s) never installs /usr/local/bin/terraform, so "+
		"`make check` passes in a full checkout and fails in every image build -- the target the gate "+
		"reaches through %s cannot run there, exactly as test-atlas-validate could not before that stage "+
		"was taught to carry it", stagePath, terraformModuleTestsScript)
}
