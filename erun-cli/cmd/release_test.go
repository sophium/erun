package cmd

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	common "github.com/sophium/erun/erun-common"
)

func TestReleaseCommandDryRun(t *testing.T) {
	projectRoot := createReleaseGitRepo(t, "develop")
	if err := common.SaveProjectConfig(projectRoot, common.ProjectConfig{}); err != nil {
		t.Fatalf("SaveProjectConfig failed: %v", err)
	}

	cmd := newTestRootCmd(testRootDeps{
		FindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		RunGit: func(string, io.Writer, io.Writer, ...string) error {
			t.Fatal("unexpected git execution during dry-run")
			return nil
		},
	})
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"release", "--dry-run"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if got := strings.TrimSpace(stdout.String()); !strings.HasPrefix(got, "1.4.2-rc.") {
		t.Fatalf("unexpected stdout: %q", got)
	}
	output := stderr.String()
	for _, want := range []string{
		"release: branch=develop mode=candidate version=1.4.2-rc.",
		"docker image: erunpaas/api:1.4.2-rc.",
		"git commit -m '[skip ci] release 1.4.2-rc.",
		"git tag -a",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected dry-run output to contain %q, got:\n%s", want, output)
		}
	}

	versionData, err := os.ReadFile(filepath.Join(projectRoot, "erun-devops", "VERSION"))
	if err != nil {
		t.Fatalf("read VERSION: %v", err)
	}
	if got := string(versionData); got != "1.4.2\n" {
		t.Fatalf("unexpected VERSION content after dry-run: %q", got)
	}
}

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
	if err := os.WriteFile(filepath.Join(releaseRoot, "VERSION"), []byte("1.4.2\n"), 0o644); err != nil {
		t.Fatalf("write VERSION: %v", err)
	}
	if err := os.WriteFile(filepath.Join(releaseRoot, "k8s", "api", "Chart.yaml"), []byte("apiVersion: v2\nname: api\nversion: 0.1.0\nappVersion: 0.1.0\n"), 0o644); err != nil {
		t.Fatalf("write Chart.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(releaseRoot, "docker", "api", "Dockerfile"), []byte("FROM alpine:3.22\n"), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(releaseRoot, "docker", "base", "Dockerfile"), []byte("FROM alpine:3.22\n"), 0o644); err != nil {
		t.Fatalf("write other Dockerfile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(releaseRoot, "docker", "base", "VERSION"), []byte("9.9.9\n"), 0o644); err != nil {
		t.Fatalf("write other VERSION: %v", err)
	}

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
