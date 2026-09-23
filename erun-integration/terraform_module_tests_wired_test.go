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
// module sources the target reads (TestTerraformTestStageInstallsTerraformAndMirror).
// Either half alone is the original defect in a new place: a wired target with
// no binary in the stage fails the build, and an installed binary with no
// target runs nothing.
//
// Both read plain text rather than executing anything, the same approach as
// TestCheckGateTargetCountMatchesItsPrerequisites and
// TestBuildCheckGateCoversEveryTestSuite beside them.

func TestCheckGateRunsTerraformModuleTests(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	makefileText := readMakefile(t, root)

	prereqs := makeTargetPrerequisites(t, makefileText, "check-gate")
	found := false
	for _, p := range prereqs {
		if p == "terraform-module-tests" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("check-gate does not list terraform-module-tests among its prerequisites (%s): "+
			"the published modules' terraform test suites would go back to being run by nothing",
			strings.Join(prereqs, " "))
	}

	recipe := makeTargetRecipe(t, makefileText, "terraform-module-tests")

	// Modules are discovered, not named one by one, which is what makes a new
	// module's tests picked up with no Makefile edit. The discovery lives in a
	// script rather than a Make wildcard so the Makefile, the image's mirror
	// bake and this suite cannot disagree about which modules have tests.
	if !strings.Contains(recipe, "scripts/terraform-test-modules.sh") {
		t.Error("the terraform-module-tests recipe does not discover modules through scripts/terraform-test-modules.sh: " +
			"a hardcoded module list would silently stop covering new modules, and a second discovery rule is how a module's tests end up reached by one caller and skipped by another")
	}
	if !strings.Contains(recipe, "scripts/terraform-module-test.sh") {
		t.Error("the terraform-module-tests recipe never runs scripts/terraform-module-test.sh, the runner that refuses an unpinned module and initializes with -lockfile=readonly")
	}
	if !strings.Contains(recipe, "terraform fmt -check -recursive") {
		t.Error("the terraform-module-tests recipe never runs `terraform fmt -check -recursive`, so module formatting is unchecked")
	}

	// The terraform commands themselves live in the runner, not in the recipe:
	// the recipe is a fan-out over `scripts/terraform-test-modules.sh`'s output,
	// and the runner is what each job executes and what the self-test drives.
	// Asserting on the recipe alone would therefore pass over a runner that had
	// stopped calling terraform, so the checks for the actual invocation read
	// the runner.
	runner := mustReadRepoFile(t, root, "scripts/terraform-module-test.sh")
	if !strings.Contains(runner, "terraform ") || !strings.Contains(runner, "test") {
		t.Error("scripts/terraform-module-test.sh never invokes `terraform test`, so the target would pass without running any suite")
	}
	// init is load-bearing, not ceremony: `terraform test` does not
	// auto-initialise, and without an init it fails immediately instead of
	// running the suite.
	if !strings.Contains(runner, "init -backend=false") {
		t.Error("scripts/terraform-module-test.sh never runs `terraform init -backend=false`: `terraform test` does not auto-initialise, so it would fail rather than run the suites")
	}
	// The flags below are asserted against the runner's executable lines, not
	// its whole text. The script explains each flag at length in its header
	// comment, so a substring check over the file as a whole would go on
	// passing after the flag was deleted from the command it describes --
	// matching the prose and missing the regression.
	code := shellCodeLines(runner)

	// -lockfile=readonly is what makes the gate test the pinned provider set
	// rather than whatever the registry published that morning, which is the
	// pin the image's mirror bake is built from.
	if !strings.Contains(code, "-lockfile=readonly") {
		t.Error("scripts/terraform-module-test.sh no longer initializes with -lockfile=readonly, so a provider constraint that moved would be silently re-resolved instead of failing the gate")
	}
	// The existence check is the other half and is not redundant with the flag:
	// terraform only refuses to *change* an existing lock file, so a module
	// with none initializes happily and writes one.
	//
	// Asserted as the test expression itself rather than as the bare filename,
	// which the script also names in its refusal messages -- matching those
	// would keep passing after the check they describe was removed, which is
	// exactly how this assertion first read. This pins the check is *wired to
	// the module being tested*; that removing the lock file really makes the
	// gate red is the self-test's job, not a claim static text can make.
	if !strings.Contains(code, `[ -f "${module}/.terraform.lock.hcl" ]`) {
		t.Error("scripts/terraform-module-test.sh no longer gates on the module's own committed .terraform.lock.hcl: " +
			"without that check a module with no lock file initializes happily and writes one, and -lockfile=readonly alone does not refuse it")
	}

	// The runner's own self-test is what makes this target's green mean
	// something: it drives the runner over a module copy whose pinned
	// invariant has been removed and requires a non-zero verdict. A runner
	// that swallowed terraform's exit status would otherwise report a green
	// gate over an untested tree, which is the defect this whole target exists
	// to remove. Dropping the job from the recipe would leave every other
	// assertion here satisfied and that hole open.
	if !strings.Contains(recipe, "terraform-module-tests_test.sh") {
		t.Error("the terraform-module-tests recipe no longer runs scripts/terraform-module-tests_test.sh: " +
			"without its mutation test, a runner that swallowed terraform's exit status would report a green gate over an untested tree")
	}
}

// shellCodeLines returns script's non-comment, non-blank lines joined back into
// one string, so an assertion about what a script *does* cannot be satisfied by
// prose describing what it used to do. Filtered rather than stripped, so a `#`
// inside an argument is left alone.
func shellCodeLines(script string) string {
	var b strings.Builder
	for _, line := range strings.Split(script, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

func TestTerraformTestStageInstallsTerraformAndMirror(t *testing.T) {
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
		t.Error("the erun-devops image test stage does not install terraform, so the terraform-module-tests target it runs cannot execute: " +
			"the module suites would fail with `terraform: not found` in every build")
	}
	if !strings.Contains(testStage, "COPY erun-devops/terraform-erun") {
		t.Error("the erun-devops image test stage does not COPY erun-devops/terraform-erun, " +
			"so the terraform-module-tests target would run against no module sources inside the image")
	}

	// The offline provider mirror is the difference between this target being
	// a normal offline gate job and a live Third-party dependency on
	// registry.terraform.io: the bake has to happen in the stage the gate runs
	// in, and TF_CLI_CONFIG_FILE is what makes the gate's own init read it.
	if !strings.Contains(testStage, "scripts/terraform-providers-mirror.sh") {
		t.Error("the erun-devops image test stage no longer bakes the terraform provider mirror: " +
			"`terraform init` in the gate would fetch providers from registry.terraform.io, so a registry outage would red every branch")
	}
	if !strings.Contains(testStage, "TF_CLI_CONFIG_FILE") {
		t.Error("the erun-devops image test stage never sets TF_CLI_CONFIG_FILE, so terraform would ignore the baked mirror and resolve providers from the registry")
	}

	// One pin, not two. The version is declared once in the file's global ARG
	// block and re-declared (defaultless) by each stage that installs it, the
	// same shape HELM_VERSION and ATLAS_VERSION use -- so the binary the gate
	// runs and the binary a runtime pod ships cannot drift apart. A second
	// `ARG TERRAFORM_VERSION=<value>` anywhere is that drift starting.
	//
	// It also has to be the same binary that built the mirror: the bake runs
	// terraform at image build, and terraform reads its own lock-file format,
	// so a mirror written by a different major would be a mirror the gate
	// cannot consistently initialize against.
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
