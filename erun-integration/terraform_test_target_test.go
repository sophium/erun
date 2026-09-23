package integration

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The published erun-devops/terraform-erun modules carry real `terraform test`
// suites that run against mocked providers, and for a while nothing in this
// repository ever ran them: no target, no script, no CI. They existed, they
// passed when invoked by hand, and a module edit that broke one still shipped
// -- the suites read as coverage in review while enforcing nothing.
//
// These two tests lock the two halves of the wire-up that fixes that: the
// target has to be a real prerequisite of check-gate and has to actually
// invoke terraform over the module tree (TestCheckGateRunsTerraformModuleTests),
// and the image the gate builds has to carry both the terraform binary and the
// module sources the target reads (TestTerraformTestStageInstallsTerraformAndModuleSources).
// Either half alone is the original defect in a new place: a wired target with
// no binary in the stage fails the build, and an installed binary with no
// target runs nothing.
//
// Both read plain text rather than executing anything, the same approach as
// TestCheckGateTargetCountMatchesItsPrerequisites and
// TestBuildCheckGateCoversEveryTestSuite beside them.

// terraformModuleTree is the directory the target is expected to cover.
const terraformModuleTree = "erun-devops/terraform-erun/modules"

func TestCheckGateRunsTerraformModuleTests(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	makefileText := readMakefile(t, root)

	prereqs := makeTargetPrerequisites(t, makefileText, "check-gate")
	found := false
	for _, p := range prereqs {
		if p == "terraform-test" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("check-gate does not list terraform-test among its prerequisites (%s): "+
			"the published modules' terraform test suites would go back to being run by nothing",
			strings.Join(prereqs, " "))
	}

	recipe := makeTargetRecipe(t, makefileText, "terraform-test")

	// The command, not just the target name: a target that exists but never
	// invokes terraform is the same defect wearing a different hat.
	if !strings.Contains(recipe, "terraform test") {
		t.Error("the terraform-test recipe never invokes `terraform test`, so the target would pass without running any suite")
	}
	// init is load-bearing, not ceremony: `terraform test` does not
	// auto-initialise, and without an init it fails immediately instead of
	// running the suite.
	if !strings.Contains(recipe, "init -backend=false") {
		t.Error("the terraform-test recipe never runs `terraform init -backend=false`: `terraform test` does not auto-initialise, so it would fail rather than run the suites")
	}
	if !strings.Contains(recipe, terraformModuleTree) {
		t.Errorf("the terraform-test recipe never references %s, so it does not reach the module suites", terraformModuleTree)
	}
	// Modules are discovered by scanning the tree rather than being named one
	// by one, which is what makes a new module's tests picked up with no
	// Makefile edit. The scan is what keeps the target from silently covering
	// a shrinking subset as modules are added.
	if !strings.Contains(recipe, "*") {
		t.Errorf("the terraform-test recipe does not glob %s: a hardcoded module list would silently stop covering new modules", terraformModuleTree)
	}
	if !strings.Contains(recipe, "terraform fmt -check -recursive") {
		t.Error("the terraform-test recipe never runs `terraform fmt -check -recursive`, so module formatting is unchecked")
	}
}

func TestTerraformTestStageInstallsTerraformAndModuleSources(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)

	// The erun-devops image's own Dockerfile is its test stage's full-checkout
	// sentinel; where it is genuinely absent this test has nothing to read, so
	// skip rather than fail on a partial build context.
	const dockerfilePath = "erun-devops/docker/erun-devops/Dockerfile"
	if _, err := os.Stat(filepath.Join(root, dockerfilePath)); err != nil {
		t.Skipf("full source tree not present (partial in-build build context): %v", err)
	}
	dockerfile := mustReadRepoFile(t, root, dockerfilePath)

	testStage := dockerStageText(dockerfile, "test")
	if testStage == "" {
		t.Fatal("the erun-devops Dockerfile no longer has a stage named `test`: " +
			"that stage is what runs `make check` at image build time, so this test cannot tell whether the gate can run terraform")
	}

	if !strings.Contains(testStage, "releases.hashicorp.com/terraform") {
		t.Error("the erun-devops image test stage does not install terraform, so the terraform-test target it runs cannot execute: " +
			"the module suites would fail with `terraform: not found` in every build")
	}
	if !strings.Contains(testStage, "COPY erun-devops/terraform-erun") {
		t.Error("the erun-devops image test stage does not COPY erun-devops/terraform-erun, " +
			"so the terraform-test target would run against no module sources inside the image")
	}

	// One pin, not two. The version is declared once in the file's global ARG
	// block and re-declared (defaultless) by each stage that installs it, the
	// same shape HELM_VERSION and ATLAS_VERSION use -- so the binary the gate
	// runs and the binary a runtime pod ships cannot drift apart. A second
	// `ARG TERRAFORM_VERSION=<value>` anywhere is that drift starting.
	pinned := regexp.MustCompile(`(?m)^ARG TERRAFORM_VERSION=(\S+)\s*$`).FindAllStringSubmatch(dockerfile, -1)
	if len(pinned) != 1 {
		t.Errorf("the erun-devops Dockerfile must pin TERRAFORM_VERSION exactly once in its global ARG block, but declares %d defaults: "+
			"two defaults are how the test stage's terraform and the shipped image's terraform drift apart", len(pinned))
	}
}

// dockerStageText returns the body of the Dockerfile stage introduced by
// `FROM ... AS <name>`, ending at the next FROM. Empty when no such stage
// exists. Deliberately a plain text scan: this suite does not embed a
// Dockerfile parser, and the stage boundaries here are single literal lines.
func dockerStageText(dockerfile, name string) string {
	lines := strings.Split(dockerfile, "\n")
	suffix := "AS " + name
	start := -1
	for i, line := range lines {
		if !strings.HasPrefix(line, "FROM ") {
			continue
		}
		if start >= 0 {
			return strings.Join(lines[start:i], "\n")
		}
		if strings.HasSuffix(strings.TrimSpace(line), suffix) {
			start = i + 1
		}
	}
	if start >= 0 {
		return strings.Join(lines[start:], "\n")
	}
	return ""
}
