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

// TestGeneratedPinHistoryDoesNotDirtyACleanCheckout: build scripts stamp "-dirty" on any porcelain output,
// so .erun/pin-history.json must be ignored while .erun/config.yaml stays trackable.
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
	mustGit(t, checkout, home, "config", "user.email", "test@example")
	mustGit(t, checkout, home, "config", "user.name", "Test")

	// Checked before committing: git never reports a tracked path as ignored.
	if ignored, _ := git(t, checkout, home, "check-ignore", "-q", "--", ".erun/config.yaml"); ignored {
		t.Fatalf("the repo's .gitignore excludes .erun/config.yaml: that file is the per-project layer " +
			"(`paths.*`, `containerregistries`) and has to stay trackable, or the onboarding it carries works " +
			"on this machine only. Ignore the generated files beside it rather than the directory.")
	}

	mustGit(t, checkout, home, "add", "-A")
	mustGit(t, checkout, home, "commit", "-q", "-m", "seed a clean tree carrying an onboarded project config")

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

// git neutralizes the ambient git config so ~/.gitconfig cannot make the gate pass.
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

func mustGit(t testing.TB, dir, home string, args ...string) {
	t.Helper()
	if ok, out := git(t, dir, home, args...); !ok {
		t.Fatalf("git %v failed:\n%s", args, out)
	}
}
