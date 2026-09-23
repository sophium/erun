package integration

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/harnessexec"
)

// TestGeneratedPinHistoryDoesNotDirtyACleanCheckout is the reproduction for the
// `-dirty` stamp on an untouched tree. erun-ui/build.sh and erun-cli/run.sh both
// mark their build "-dirty" on any non-empty `git status --porcelain`, because a
// build's stamp has to describe the artifact rather than the last commit — so a
// generated file that is neither tracked nor ignored is indistinguishable there
// from an uncommitted source change. `.erun/pin-history.json`, which a re-pin
// writes beside the project config, was exactly that.
//
// The checkout is a temp directory seeded with this repo's real .gitignore
// rather than the live one: the rule under test is the rule the repo ships, and
// observing it by creating the generated file in the live tree would leave that
// tree dirty when it passed and depend on run order when it failed.
//
// The second assertion is not symmetry for its own sake. The obvious wrong fix
// is to ignore `.erun/` wholesale, which would take `.erun/config.yaml` with it —
// that file is the per-project layer and has to stay trackable, or an onboarding
// works on the one machine that wrote it and nowhere else.
func TestGeneratedPinHistoryDoesNotDirtyACleanCheckout(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	rules, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		t.Fatalf("read the checkout's .gitignore: %v", err)
	}

	checkout := t.TempDir()
	home := t.TempDir()
	mustWriteFile(t, filepath.Join(checkout, ".gitignore"), string(rules))
	mustWriteFile(t, filepath.Join(checkout, ".erun", "config.yaml"), "tenant: example\n")

	mustGit(t, checkout, home, "init", "-q", "-b", "main")
	// Repository-local, because the neutralized HOME above is what keeps this
	// gate independent of the contributor's own git config.
	mustGit(t, checkout, home, "config", "user.email", "test@example")
	mustGit(t, checkout, home, "config", "user.name", "Test")

	// Checked before the file is committed: git never reports a tracked path as
	// ignored, so asking afterwards would pass even under a bare `.erun/` rule.
	if ignored, _ := git(t, checkout, home, "check-ignore", "-q", "--", ".erun/config.yaml"); ignored {
		t.Fatalf("the repo's .gitignore excludes .erun/config.yaml: that file is the per-project layer " +
			"(`paths.*`, `containerregistries`) and has to stay trackable, or the onboarding it carries works " +
			"on this machine only. Ignore the generated files beside it rather than the directory.")
	}

	mustGit(t, checkout, home, "add", "-A")
	mustGit(t, checkout, home, "commit", "-q", "-m", "seed a clean tree carrying an onboarded project config")

	// What a re-pin leaves behind on an otherwise untouched tree.
	mustWriteFile(t, filepath.Join(checkout, ".erun", "pin-history.json"), "{\n  \"previous\": {}\n}\n")

	_, status := git(t, checkout, home, "status", "--porcelain")
	if strings.TrimSpace(status) != "" {
		t.Errorf("a re-pin's generated .erun/pin-history.json makes a clean checkout read as dirty: "+
			"`git status --porcelain` reports %q. Both build scripts stamp \"-dirty\" from exactly this "+
			"output, so a build from an untouched tree would claim its commit does not describe it, and a "+
			"genuinely modified tree would be indistinguishable from a clean one.",
			strings.TrimSpace(status))
	}
}

// TestDevopsTestStageCopiesTheRepoGitignore guards the other half of the test
// above: that the stage running it can read the file at all.
//
// TestGeneratedPinHistoryDoesNotDirtyACleanCheckout resolves repoRoot from its
// own compiled location, so inside the erun-devops image's `make check` it reads
// `<stage>/src/.gitignore` -- a directory holding only what that Dockerfile
// COPYd by explicit path. Both a tree that lost the pin-history rule and a stage
// that lost the `COPY .gitignore` red that test, but only the first is reachable
// from a pod: the second turns a green local suite into a ~4-minute failure deep
// inside `integration-test-gate` in every image build, which is how the COPY was
// silently dropped from this file once already while the pod-side run stayed
// green. Reading the Dockerfile as text is the same approach the neighbouring
// Dockerfile guards in this package take.
func TestDevopsTestStageCopiesTheRepoGitignore(t *testing.T) {
	t.Parallel()
	root, ok := findFullCheckoutRoot()
	if !ok {
		t.Skip("full source tree not present (partial in-build build context); this Dockerfile-content guard runs on a full checkout")
	}

	const dockerfilePath = "erun-devops/docker/erun-devops/Dockerfile"
	dockerfile := mustReadRepoFile(t, root, dockerfilePath)

	// Matched with its destination, and through indexOfCommandLine so a comment
	// describing the COPY cannot satisfy it -- this line is exactly what a
	// `COPY .dockerignore` next to it must not be mistaken for.
	if indexOfCommandLine(dockerfile, "COPY .gitignore /src/.gitignore") < 0 {
		t.Fatalf("%s never COPYs the repo's .gitignore into the test stage's /src. "+
			"TestGeneratedPinHistoryDoesNotDirtyACleanCheckout reads that path inside `make check` and treats a "+
			"missing file as fatal rather than skipping, so every image build fails in integration-test-gate while "+
			"the same suite passes in a pod. Add the COPY line back; do not make the test skip instead.", dockerfilePath)
	}
}

// git runs one git command in dir and returns whether it exited 0 along with its
// combined output. The ambient git configuration is neutralized so that a
// contributor's own ~/.gitconfig — a core.excludesFile, or
// status.showUntrackedFiles=no — cannot make this gate pass for a reason the
// repo's .gitignore did not cause.
func git(t testing.TB, dir, home string, args ...string) (bool, string) {
	t.Helper()
	cmd := harnessexec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"USERPROFILE="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"GIT_CONFIG_NOSYSTEM=1",
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return true, string(out)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return false, string(out)
}

// mustGit is git for the commands the scenario's own setup needs to succeed.
func mustGit(t testing.TB, dir, home string, args ...string) {
	t.Helper()
	if ok, out := git(t, dir, home, args...); !ok {
		t.Fatalf("git %v failed:\n%s", args, out)
	}
}
