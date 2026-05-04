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
	requireNoError(t, common.SaveProjectConfig(projectRoot, common.ProjectConfig{}), "SaveProjectConfig failed")

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

	requireNoError(t, cmd.Execute(), "Execute failed")

	if got := strings.TrimSpace(stdout.String()); !strings.HasPrefix(got, "1.4.2-rc.") {
		t.Fatalf("unexpected stdout: %q", got)
	}
	output := stderr.String()
	for _, want := range []string{
		"release: branch=develop mode=candidate version=1.4.2-rc.",
		"stage: sync-remote",
		"git fetch origin",
		"git rebase origin/develop",
		"docker image: ghcr.io/rihards-freimanis/api:1.4.2-rc.",
		"git commit -m '[skip ci] release 1.4.2-rc.",
		"git tag -a",
		"stage: push",
		"git push --follow-tags origin develop",
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

func TestReleaseCommandDryRunStableIncludesSyncAndPush(t *testing.T) {
	projectRoot := createReleaseGitRepo(t, "main")
	runGitCommand(t, projectRoot, "branch", "develop")
	requireNoError(t, common.SaveProjectConfig(projectRoot, common.ProjectConfig{}), "SaveProjectConfig failed")

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

	requireNoError(t, cmd.Execute(), "Execute failed")

	if got := strings.TrimSpace(stdout.String()); got != "1.4.2" {
		t.Fatalf("unexpected stdout: %q", got)
	}
	output := stderr.String()
	for _, want := range []string{
		"release: branch=main mode=stable version=1.4.2",
		"next version: 1.4.3",
		"stage: sync-remote",
		"git fetch origin",
		"git rebase origin/main",
		"stage: sync-develop",
		"git checkout develop",
		"git merge --no-edit -X theirs main",
		"stage: push",
		"git push --follow-tags origin main develop",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected dry-run output to contain %q, got:\n%s", want, output)
		}
	}
}

func TestReleaseCommandDryRunStableWithoutDevelopOnlyPushesMain(t *testing.T) {
	projectRoot := createReleaseGitRepo(t, "main")
	requireNoError(t, common.SaveProjectConfig(projectRoot, common.ProjectConfig{}), "SaveProjectConfig failed")

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

	requireNoError(t, cmd.Execute(), "Execute failed")

	if got := strings.TrimSpace(stdout.String()); got != "1.4.2" {
		t.Fatalf("unexpected stdout: %q", got)
	}
	output := stderr.String()
	if strings.Contains(output, "stage: sync-develop") || strings.Contains(output, "git checkout develop") {
		t.Fatalf("did not expect develop sync in output:\n%s", output)
	}
	for _, want := range []string{
		"stage: sync-remote",
		"git fetch origin",
		"git rebase origin/main",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected main sync in output to contain %q, got:\n%s", want, output)
		}
	}
	if !strings.Contains(output, "stage: push") || !strings.Contains(output, "git push --follow-tags origin main") {
		t.Fatalf("expected main-only push in output, got:\n%s", output)
	}
	if strings.Contains(output, "git push --follow-tags origin main develop") {
		t.Fatalf("did not expect develop push target in output:\n%s", output)
	}
}

func TestReleaseCommandDryRunForceIncludesTagDeletionForStaleReleaseTag(t *testing.T) {
	projectRoot := createReleaseGitRepo(t, "main")
	remoteRoot := filepath.Join(t.TempDir(), "origin.git")
	runGitCommand(t, t.TempDir(), "init", "--bare", remoteRoot)
	runGitCommand(t, projectRoot, "remote", "add", "origin", remoteRoot)
	runGitCommand(t, projectRoot, "push", "-u", "origin", "main")
	runGitCommand(t, projectRoot, "tag", "-a", "v1.4.2", "-m", "Release 1.4.2")
	runGitCommand(t, projectRoot, "push", "origin", "v1.4.2")
	requireNoError(t, os.WriteFile(filepath.Join(projectRoot, "erun-devops", "README.tmp"), []byte("change\n"), 0o644), "write temp change")
	runGitCommand(t, projectRoot, "add", "erun-devops/README.tmp")
	runGitCommand(t, projectRoot, "commit", "-m", "advance head")
	runGitCommand(t, projectRoot, "push", "origin", "main")

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
	cmd.SetArgs([]string{"release", "--dry-run", "--force"})

	requireNoError(t, cmd.Execute(), "Execute failed")

	output := stderr.String()
	for _, want := range []string{
		"git tag -d v1.4.2",
		"git push --delete origin v1.4.2",
		"git tag -a v1.4.2 -m 'Release 1.4.2'",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected dry-run output to contain %q, got:\n%s", want, output)
		}
	}
}

func TestReleaseCommandWritesOnlyVersionToStdoutDuringExecution(t *testing.T) {
	projectRoot := createReleaseGitRepo(t, "develop")
	requireNoError(t, common.SaveProjectConfig(projectRoot, common.ProjectConfig{}), "SaveProjectConfig failed")
	runGitCommand(t, projectRoot, "add", ".erun/config.yaml")
	runGitCommand(t, projectRoot, "commit", "-m", "save config")

	cmd := newTestRootCmd(testRootDeps{
		FindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		RunGit: func(_ string, stdout, stderr io.Writer, _ ...string) error {
			if _, err := io.WriteString(stdout, "git-stdout\n"); err != nil {
				return err
			}
			if _, err := io.WriteString(stderr, "git-stderr\n"); err != nil {
				return err
			}
			return nil
		},
	})
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"release"})

	requireNoError(t, cmd.Execute(), "Execute failed")

	if got := strings.TrimSpace(stdout.String()); !strings.HasPrefix(got, "1.4.2-rc.") || strings.Contains(got, "git-stdout") {
		t.Fatalf("unexpected stdout: %q", got)
	}
	if got := stderr.String(); !strings.Contains(got, "git-stdout") || !strings.Contains(got, "git-stderr") {
		t.Fatalf("expected git command output on stderr, got %q", got)
	}
}

func TestReleaseCommandDryRunIncludesLinuxReleaseScripts(t *testing.T) {
	projectRoot := createReleaseGitRepo(t, "develop")
	linuxComponentDir := filepath.Join(projectRoot, "erun-devops", "linux", "erun-cli")
	requireNoError(t, os.MkdirAll(linuxComponentDir, 0o755), "mkdir linux component dir")
	requireNoError(t, os.WriteFile(filepath.Join(linuxComponentDir, "release.sh"), []byte("#!/bin/sh\n"), 0o755), "write release.sh")
	requireNoError(t, common.SaveProjectConfig(projectRoot, common.ProjectConfig{}), "SaveProjectConfig failed")

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

	requireNoError(t, cmd.Execute(), "Execute failed")

	output := stderr.String()
	if common.LinuxPackageBuildsSupported() {
		if !strings.Contains(output, "ERUN_BUILD_VERSION=1.4.2-rc.") || !strings.Contains(output, "./release.sh") {
			t.Fatalf("expected linux release trace, got:\n%s", output)
		}
	} else if !strings.Contains(output, "skipping linux package scripts: host is not Linux or dpkg-deb is unavailable") {
		t.Fatalf("expected linux release skip trace, got:\n%s", output)
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
