package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func TestRelease(t *testing.T) {
	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"release", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "release/help", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_in_git_repo", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedGitRepo(t, setup.Cwd)
		result := erun.Run(t, []string{"release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "release/dry_run_in_git_repo", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_develop_emits_candidate_plan", func(t *testing.T) {
		// Exercises release.go on the develop branch: --dry-run must emit a
		// `mode=candidate` plan with sync-remote / push stages, the
		// resolved docker image reference, and the rc.<count> version
		// suffix on stdout.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
		result := erun.Run(t, []string{"release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if !strings.HasPrefix(strings.TrimSpace(result.Stdout), "1.4.2-rc.") {
			t.Errorf("expected stdout to start with candidate version 1.4.2-rc., got:\n%s", result.Stdout)
		}
		for _, want := range []string{
			"release: branch=develop mode=candidate version=1.4.2-rc.",
			"stage: sync-remote",
			"git fetch origin",
			"git rebase origin/develop",
			"git commit -m '[skip ci] release 1.4.2-rc.",
			"git tag -a",
			"stage: push",
			"git push --follow-tags origin develop",
		} {
			if !strings.Contains(result.Stderr, want) {
				t.Errorf("expected dry-run output to contain %q, got stderr:\n%s", want, result.Stderr)
			}
		}
	})

	t.Run("dry_run_main_with_develop_emits_stable_plan", func(t *testing.T) {
		// Exercises release.go stable path on main when develop also
		// exists: --dry-run must include both sync-remote (main) and
		// sync-develop stages, and push to both branches.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "main")
		fixture.RunGit(t, setup.Cwd, "branch", "develop")
		result := erun.Run(t, []string{"release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if got := strings.TrimSpace(result.Stdout); got != "1.4.2" {
			t.Errorf("expected stdout to be 1.4.2, got %q", got)
		}
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
			if !strings.Contains(result.Stderr, want) {
				t.Errorf("expected stable plan to contain %q, got stderr:\n%s", want, result.Stderr)
			}
		}
	})

	t.Run("dry_run_main_without_develop_pushes_only_main", func(t *testing.T) {
		// Exercises release.go stable path on main when develop does not
		// exist: --dry-run must not include sync-develop or develop in
		// the push target.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "main")
		result := erun.Run(t, []string{"release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if got := strings.TrimSpace(result.Stdout); got != "1.4.2" {
			t.Errorf("expected stdout to be 1.4.2, got %q", got)
		}
		if strings.Contains(result.Stderr, "stage: sync-develop") || strings.Contains(result.Stderr, "git checkout develop") {
			t.Errorf("did not expect develop sync in output:\n%s", result.Stderr)
		}
		if !strings.Contains(result.Stderr, "git push --follow-tags origin main") {
			t.Errorf("expected main-only push in output, got stderr:\n%s", result.Stderr)
		}
		if strings.Contains(result.Stderr, "git push --follow-tags origin main develop") {
			t.Errorf("did not expect develop in push target:\n%s", result.Stderr)
		}
	})

	t.Run("dry_run_includes_linux_release_scripts", func(t *testing.T) {
		// Exercises release.go linux package release path: --dry-run must
		// trace the per-component release script invocation with
		// ERUN_BUILD_VERSION when the host supports Linux package builds.
		// The integration harness only runs on Linux, so the support
		// branch is always taken.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
		linuxComponentDir := filepath.Join(setup.Cwd, "erun-devops", "linux", "erun-cli")
		if err := os.MkdirAll(linuxComponentDir, 0o755); err != nil {
			t.Fatalf("mkdir linux component dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(linuxComponentDir, "release.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatalf("write release.sh: %v", err)
		}
		fixture.RunGit(t, setup.Cwd, "add", ".")
		fixture.RunGit(t, setup.Cwd, "commit", "-m", "add linux release script")

		result := erun.Run(t, []string{"release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		for _, want := range []string{
			"ERUN_BUILD_VERSION=1.4.2-rc.",
			"./release.sh",
		} {
			if !strings.Contains(result.Stderr, want) {
				t.Errorf("expected linux release trace to contain %q, got stderr:\n%s", want, result.Stderr)
			}
		}
	})

	t.Run("dry_run_force_includes_tag_deletion_for_stale_release_tag", func(t *testing.T) {
		// Exercises release.go --force path: when the release tag already
		// exists remotely on a commit other than HEAD, --dry-run must
		// trace tag deletion (local + origin) before recreating the tag.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "main")
		remoteRoot := filepath.Join(setup.Home, "origin.git")
		fixture.RunGit(t, setup.Home, "init", "-q", "--bare", remoteRoot)
		fixture.RunGit(t, setup.Cwd, "remote", "add", "origin", remoteRoot)
		fixture.RunGit(t, setup.Cwd, "push", "-u", "origin", "main")
		fixture.RunGit(t, setup.Cwd, "tag", "-a", "v1.4.2", "-m", "Release 1.4.2")
		fixture.RunGit(t, setup.Cwd, "push", "origin", "v1.4.2")
		// Advance HEAD past the tagged commit.
		fixture.RunGit(t, setup.Cwd, "commit", "--allow-empty", "-m", "advance head")
		fixture.RunGit(t, setup.Cwd, "push", "origin", "main")

		result := erun.Run(t, []string{"release", "--dry-run", "--force"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		for _, want := range []string{
			"git tag -d v1.4.2",
			"git push --delete origin v1.4.2",
			"git tag -a v1.4.2 -m 'Release 1.4.2'",
		} {
			if !strings.Contains(result.Stderr, want) {
				t.Errorf("expected dry-run output to contain %q, got stderr:\n%s", want, result.Stderr)
			}
		}
	})
}
