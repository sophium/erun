package gitx

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/sophium/erun/erun-common/base/model"
)

// gitPushAccessTestRepo points origin at a closed local port so ls-remote fails fast.
func gitPushAccessTestRepo(t *testing.T, remote string) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"remote", "add", "origin", remote},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

// gitPushAccessStubBin puts stub gh and ssh on PATH, controlled by GH_STUB_EXIT,
// GH_STUB_STDOUT, and SSH_STUB_EXIT.
func gitPushAccessStubBin(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	gh := "#!/bin/sh\n" +
		"if [ \"$1\" = auth ] && [ \"$2\" = status ]; then\n" +
		"  printf '%s' \"$GH_STUB_STDOUT\"\n" +
		"  exit \"${GH_STUB_EXIT:-0}\"\n" +
		"fi\n" +
		"exit 1\n"
	ssh := "#!/bin/sh\nexit \"${SSH_STUB_EXIT:-0}\"\n"
	for name, content := range map[string]string{"gh": gh, "ssh": ssh} {
		path := filepath.Join(bin, name)
		if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
			t.Fatalf("write stub %s: %v", name, err)
		}
	}
	return bin
}

func runGitPushAccessScript(t *testing.T, repoPath string, env map[string]string) model.GitPushAccessStatus {
	t.Helper()
	cmd := exec.Command("sh", "-c", gitPushAccessScript(repoPath))
	cmd.Env = append(os.Environ(), "PATH="+env["PATH"]+":"+os.Getenv("PATH"))
	for k, v := range env {
		if k == "PATH" {
			continue
		}
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run git-push-access-script: %v\nstderr: %s", err, stderrOf(err))
	}
	return parseGitPushAccessReport(string(out))
}

func stderrOf(err error) string {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return string(exitErr.Stderr)
	}
	return ""
}

func TestGitPushAccessGHSessionWithoutRepoScopeIsNotAPushCredential(t *testing.T) {
	repo := gitPushAccessTestRepo(t, "https://127.0.0.1:1/acme/example.git")
	status := runGitPushAccessScript(t, repo, map[string]string{
		"PATH":           gitPushAccessStubBin(t),
		"GH_STUB_EXIT":   "0",
		"GH_STUB_STDOUT": "Logged in to 127.0.0.1:1 account acme\nToken scopes: 'gist', 'read:org'\n",
	})
	if !status.GHAuthenticated {
		t.Fatal("expected gh_authenticated=1")
	}
	if status.PushCredential {
		t.Fatal("a gh session without the repo scope must not read as a push credential")
	}
}

func TestGitPushAccessGHSessionWithRepoScopeIsAPushCredential(t *testing.T) {
	repo := gitPushAccessTestRepo(t, "https://127.0.0.1:1/acme/example.git")
	status := runGitPushAccessScript(t, repo, map[string]string{
		"PATH":           gitPushAccessStubBin(t),
		"GH_STUB_EXIT":   "0",
		"GH_STUB_STDOUT": "Logged in to 127.0.0.1:1 account acme\nToken scopes: 'gist', 'repo', 'workflow'\n",
	})
	if !status.PushCredential {
		t.Fatal("a gh session carrying the repo scope must read as a push credential")
	}
}

// Fine-grained PATs and App tokens report no scope list.
func TestGitPushAccessGHSessionWithNoScopeLineIsTrusted(t *testing.T) {
	repo := gitPushAccessTestRepo(t, "https://127.0.0.1:1/acme/example.git")
	status := runGitPushAccessScript(t, repo, map[string]string{
		"PATH":           gitPushAccessStubBin(t),
		"GH_STUB_EXIT":   "0",
		"GH_STUB_STDOUT": "Logged in to 127.0.0.1:1 account acme (keyring)\n",
	})
	if !status.PushCredential {
		t.Fatal("a gh session reporting no scope list must not be refused")
	}
}

// ssh reserves exit 255 for its own failures; any other exit means the key was accepted.
func TestGitPushAccessSSHNonGitHubWelcomeStillCounts(t *testing.T) {
	repo := gitPushAccessTestRepo(t, "git@fakehost.example:acme/example.git")
	status := runGitPushAccessScript(t, repo, map[string]string{
		"PATH":          gitPushAccessStubBin(t),
		"GH_STUB_EXIT":  "1",
		"SSH_STUB_EXIT": "0",
	})
	if !status.PushCredential {
		t.Fatal("an ssh exit other than 255 must read as a push credential regardless of wording")
	}
}

// GitHub answers a valid key with exit 1.
func TestGitPushAccessSSHGitHubStyleExitOneStillCounts(t *testing.T) {
	repo := gitPushAccessTestRepo(t, "git@fakehost.example:acme/example.git")
	status := runGitPushAccessScript(t, repo, map[string]string{
		"PATH":          gitPushAccessStubBin(t),
		"GH_STUB_EXIT":  "1",
		"SSH_STUB_EXIT": "1",
	})
	if !status.PushCredential {
		t.Fatal("an ssh exit of 1 (shell access denied post-auth) must still read as a push credential")
	}
}

func TestGitPushAccessSSHAuthFailureExit255DoesNotCount(t *testing.T) {
	repo := gitPushAccessTestRepo(t, "git@fakehost.example:acme/example.git")
	status := runGitPushAccessScript(t, repo, map[string]string{
		"PATH":          gitPushAccessStubBin(t),
		"GH_STUB_EXIT":  "1",
		"SSH_STUB_EXIT": "255",
	})
	if status.PushCredential {
		t.Fatal("an ssh exit of 255 (the client's own connection/auth failure code) must not read as a push credential")
	}
}
