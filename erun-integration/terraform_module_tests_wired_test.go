package integration

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// terraformModulesRoot is where the published Terraform modules live. Each
// module is a directory under it; the ones carrying behaviour tests also hold
// a tests/ directory of *.tftest.hcl files.
const terraformModulesRoot = "erun-devops/terraform-erun/modules"

// terraformModuleHasTests reports whether moduleDir holds at least one
// tests/*.tftest.hcl. It mirrors scripts/terraform-test-modules.sh's rule --
// a module with tests/ but no *.tftest.hcl is not a tested module, because
// `terraform test` reports "no tests" and exits 0, which reads as a pass.
func terraformModuleHasTests(t testing.TB, moduleDir string) bool {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(moduleDir, "tests", "*.tftest.hcl"))
	if err != nil {
		t.Fatalf("glob tests under %s: %v", moduleDir, err)
	}
	return len(matches) > 0
}

// TestTerraformModuleTestsAreReachedByTheGate is the structural half of the
// contract this target closes: the published modules carried real
// `terraform test` behaviour suites and nothing ran them, so an edit that
// dropped an invariant they pin applied cleanly and every gate stayed green.
//
// The Makefile's terraform-module-tests target is what closes that, and this
// test keeps the closing from coming undone the way the gap itself opened --
// by omission. It checks the two omissions that would restore it silently:
// a target dropped from check-gate's prerequisites (the target still exists,
// still passes locally, and `make check` no longer runs it), and a tested
// module with no committed .terraform.lock.hcl (the runner refuses one at run
// time, but only once the gate reaches it, and by then the failure reads as an
// environment problem rather than as an unpinned provider set).
//
// It deliberately does not verify the discovery script's own output by
// executing it: the target runs that script on every gate run, so its behavior
// is exercised for real by the same target this test checks is wired in, and
// spawning it here would have to route through internal/harnessexec to satisfy
// exec_bound_test.go. What this test adds is the part no run reports -- that
// the wiring is still there.
//
// It reads files outside its compiled inputs (the Makefile and the module
// tree), so it must run uncached; scripts/integration-test.sh already runs
// this module with -count=1, and the erun-devops test stage COPYs both inputs.
func TestTerraformModuleTestsAreReachedByTheGate(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)

	modulesDir := filepath.Join(root, terraformModulesRoot)
	entries, err := os.ReadDir(modulesDir)
	if err != nil {
		t.Fatalf("read %s: %v -- the module tree is the input this gate exists for, so a missing tree must fail here rather than skip", terraformModulesRoot, err)
	}

	var tested []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		moduleDir := filepath.Join(modulesDir, entry.Name())
		if !terraformModuleHasTests(t, moduleDir) {
			continue
		}
		tested = append(tested, entry.Name())
	}
	sort.Strings(tested)
	// A scan that matched nothing would make every assertion below vacuous,
	// which is the same silent-skip shape this test exists to catch.
	if len(tested) == 0 {
		t.Fatalf("found no module with a tests/*.tftest.hcl under %s: either the suite was moved out from under the gate or this scan stopped matching, and both are the regression this test exists to catch", terraformModulesRoot)
	}

	for _, name := range tested {
		lockPath := filepath.Join(modulesDir, name, ".terraform.lock.hcl")
		if _, err := os.Stat(lockPath); err != nil {
			t.Errorf("%s has behaviour tests but no committed .terraform.lock.hcl: terraform-module-tests would run its providers at whatever version the registry served that day, so a provider bump could land untested and unrecorded -- run `terraform -chdir=%s/%s providers lock -platform=linux_amd64 -platform=linux_arm64` and commit the result",
				name, terraformModulesRoot, name)
		}
	}

	makefileText := readMakefile(t, root)
	if !containsString(makeTargetPrerequisites(t, makefileText, "check-gate"), "terraform-module-tests") {
		t.Errorf("check-gate does not list terraform-module-tests as a prerequisite: the target that runs the %d tested Terraform module(s) (%s) would exist and pass on its own while `make check` never ran it -- which is exactly the state this gate closes",
			len(tested), strings.Join(tested, ", "))
	}

	// The recipe is what makes the prerequisite mean something: a target wired
	// into check-gate but pointed at neither the discovery rule nor the
	// self-test would run some other set of modules, or none, and still exit 0.
	recipe := makeTargetRecipe(t, makefileText, "terraform-module-tests")
	for _, want := range []string{"scripts/terraform-test-modules.sh", "scripts/terraform-module-test.sh", "scripts/terraform-module-tests_test.sh"} {
		if !strings.Contains(recipe, want) {
			t.Errorf("terraform-module-tests' recipe does not reference %s, so it no longer runs what this gate claims it runs", want)
		}
	}
}
