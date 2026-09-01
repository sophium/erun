package eruncommon

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// gitPushAccessTestRepo creates a real git checkout with the given origin
// remote so gitPushAccessScript's `cd`/`git remote get-url origin` steps have
// something real to read. The remote deliberately resolves to a closed local
// port: `git ls-remote` then fails fast (connection refused) instead of
// hanging on DNS/network for a host that doesn't exist, since these tests
// only care about the gh/ssh push-credential verdict, not fetch_ok.
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

// gitPushAccessStubBin writes a stub `gh` and `ssh` onto a fresh PATH prefix
// so gitPushAccessScript's shell logic runs for real against controlled
// answers, instead of only being checked as trace text. The stub `gh` answers
// `gh auth status -h <host>` from GH_STUB_EXIT/GH_STUB_STDOUT so a test can
// simulate a session that authenticates but reports (or omits) a scope list;
// the stub `ssh` exits SSH_STUB_EXIT so a test can simulate the ssh client's
// own exit-code convention (255 for a connection/auth failure, anything else
// passed through from the remote) independent of what text the remote host
// happens to print.
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

func runGitPushAccessScript(t *testing.T, repoPath string, env map[string]string) GitPushAccessStatus {
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
	if exitErr, ok := err.(*exec.ExitError); ok {
		return string(exitErr.Stderr)
	}
	return ""
}

// A classic OAuth gh session that authenticates but was never granted `repo`
// must not read as a push credential -- the same defect class already fixed
// several times elsewhere in this repo: `gh auth status` succeeding only
// proves the session authenticates, never that it can push.
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

// A fine-grained PAT or GitHub App token reports no scope list at all -- a
// different permission model, not an absence of permission (mirrors
// TestATokenWithADifferentPermissionModelIsNotJudged in
// ghcr_push_preflight_test.go).
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

// The ssh client itself reserves exit 255 for its own connection/auth
// failure and passes through whatever the remote decided to return
// otherwise. A non-GitHub host that greets a valid key with its own welcome
// text (not GitHub's "successfully authenticated" wording) must still read
// as a push credential.
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

// GitHub itself answers a valid key with exit 1 ("successfully authenticated,
// but GitHub does not provide shell access") -- the pre-existing GitHub case
// must keep working under the exit-code check.
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
