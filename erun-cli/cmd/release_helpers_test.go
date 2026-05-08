package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// createReleaseGitRepo materializes a minimal erun-devops layout with a
// VERSION file, dockerfiles, and Helm chart, then runs `git init -b
// <branch>` and produces one initial commit. Used by `build_test.go` for
// the release-mode build path that still has unit-only coverage because
// the real-execution branches it exercises (Docker registry auth retry,
// login prompt) are not reachable from a `--dry-run` integration scenario
// without a stub.
func createReleaseGitRepo(t *testing.T, branch string) string {
	t.Helper()

	projectRoot := t.TempDir()
	releaseRoot := filepath.Join(projectRoot, "erun-devops")
	for _, dir := range []string{
		filepath.Join(releaseRoot, "k8s", "api"),
		filepath.Join(releaseRoot, "docker", "api"),
		filepath.Join(releaseRoot, "docker", "base"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	requireNoError(t, os.WriteFile(filepath.Join(releaseRoot, "VERSION"), []byte("1.4.2\n"), 0o644), "write VERSION")
	requireNoError(t, os.WriteFile(filepath.Join(releaseRoot, "k8s", "api", "Chart.yaml"), []byte("apiVersion: v2\nname: api\nversion: 0.1.0\nappVersion: 0.1.0\n"), 0o644), "write Chart.yaml")
	requireNoError(t, os.WriteFile(filepath.Join(releaseRoot, "docker", "api", "Dockerfile"), []byte("FROM alpine:3.22\n"), 0o644), "write Dockerfile")
	requireNoError(t, os.WriteFile(filepath.Join(releaseRoot, "docker", "base", "Dockerfile"), []byte("FROM alpine:3.22\n"), 0o644), "write other Dockerfile")
	requireNoError(t, os.WriteFile(filepath.Join(releaseRoot, "docker", "base", "VERSION"), []byte("9.9.9\n"), 0o644), "write other VERSION")

	runGitCommand(t, projectRoot, "init", "-b", branch)
	runGitCommand(t, projectRoot, "config", "user.email", "codex@example.com")
	runGitCommand(t, projectRoot, "config", "user.name", "Codex")
	runGitCommand(t, projectRoot, "add", ".")
	runGitCommand(t, projectRoot, "commit", "-m", "initial")
	return projectRoot
}

func runGitCommand(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, output)
	}
}
