package eruncommon

import (
	"fmt"
	"strings"
)

// GitPushAccessStatus reports what a remote-worktree environment's project
// checkout can do against its origin remote: fetch (read) and push (write)
// are independent capabilities, and a public GitHub repository fetches
// anonymously for the entire life of a piece of work. That asymmetry is what
// makes this expensive to discover the hard way -- an environment can look
// completely healthy for hours of real work and only fail at the final
// `git push`, the worst possible moment to discover there was never a
// credential.
type GitPushAccessStatus struct {
	// RemoteURL is empty when the checkout has no origin remote at all (or no
	// checkout exists yet); every other field is meaningless in that case.
	RemoteURL string
	// FetchOK reports whether `git ls-remote` against origin succeeded.
	FetchOK bool
	// GHAuthenticated reports whether `gh auth status` succeeded for the
	// remote's host. It is read-only -- this never runs `gh auth login`,
	// `gh auth refresh`, or `gh auth switch`, so inspecting this status can
	// never itself start gh's interactive device-code/browser flow, which
	// cannot complete in a headless pod (see interactiveGHAuthAllowed in
	// build_docker_commands.go for the same reasoning applied to erun's own
	// GHCR push-scope refresh).
	GHAuthenticated bool
	// PushCredential reports whether any credential that could push resolved.
	// A `gh` session only counts when it either carries the classic OAuth
	// `repo` scope or reports no scope list at all (a fine-grained PAT or
	// GitHub App token uses a different permission model that `gh auth
	// status` can't enumerate, so an absent scope list is not evidence of
	// absent permission -- same reasoning as verifyGHCRPushScopeFor in
	// ghcr_push_preflight.go: `gh auth status` succeeding only proves the
	// session authenticates, not that it can push, so a read:org/gist-only
	// token must not read as a push credential). GH_TOKEN/GITHUB_TOKEN always
	// count. For an SSH remote with none of the above, a key the remote host
	// accepts is detected by the ssh client's own exit-code convention: ssh
	// reserves exit 255 for its own connection/auth failure and passes through
	// whatever the remote process returns otherwise, so a non-255 exit means
	// the remote accepted the key even though most git hosts (GitHub, GitLab,
	// Bitbucket, self-hosted) then refuse the shell command itself -- grepping
	// stderr for GitHub's own wording ("successfully authenticated") read a
	// valid key against any other host as no credential at all.
	PushCredential bool
}

// FetchWorksPushDoesNot is the specific asymmetry this check exists to
// surface: an environment that can read its origin remote but has no
// credential that could ever push to it.
func (s GitPushAccessStatus) FetchWorksPushDoesNot() bool {
	return s.RemoteURL != "" && s.FetchOK && !s.PushCredential
}

// InspectGitPushAccess reads back, from a remote-worktree environment's
// project checkout, whether origin resolves, whether an anonymous fetch
// succeeds, and whether a push credential is configured. The script it runs
// is read-only: it never mutates git or gh state and never invokes gh's
// interactive login/refresh/switch subcommands.
func InspectGitPushAccess(ctx Context, runner RemoteCommandRunnerFunc, req ShellLaunchParams, repoPath string) (GitPushAccessStatus, error) {
	out, err := RunTracedRemoteCommand(ctx, runner, req, "git-push-access-script", gitPushAccessScript(repoPath))
	if err != nil {
		return GitPushAccessStatus{}, fmt.Errorf("read git push access%s: %w", formatRemoteCommandStderr(out.Stderr), err)
	}
	if ctx.DryRun {
		return GitPushAccessStatus{}, nil
	}
	return parseGitPushAccessReport(out.Stdout), nil
}

func gitPushAccessScript(repoPath string) string {
	return strings.Join([]string{
		"set -eu",
		fmt.Sprintf("cd %s 2>/dev/null || { printf 'remote=\\n'; exit 0; }", shellQuote(repoPath)),
		"remote=$(git remote get-url origin 2>/dev/null || true)",
		`printf 'remote=%s\n' "$remote"`,
		`if [ -z "$remote" ]; then exit 0; fi`,
		`case "$remote" in`,
		`  https://*|http://*) host=$(printf '%s' "$remote" | sed -E 's#^[a-z]+://([^/]+)/.*#\1#') ;;`,
		`  git@*) host=$(printf '%s' "$remote" | sed -E 's#^git@([^:]+):.*#\1#') ;;`,
		`  ssh://*) host=$(printf '%s' "$remote" | sed -E 's#^ssh://([^/@]+@)?([^/]+)/.*#\2#') ;;`,
		`  *) host="" ;;`,
		"esac",
		`fetch_ok=0`,
		`if git ls-remote --exit-code "$remote" HEAD >/dev/null 2>&1; then fetch_ok=1; fi`,
		`printf 'fetch_ok=%s\n' "$fetch_ok"`,
		`gh_auth=0`,
		`gh_status=""`,
		`if [ -n "$host" ] && command -v gh >/dev/null 2>&1; then`,
		`  gh_status=$(gh auth status -h "$host" 2>&1) && gh_auth=1`,
		`fi`,
		`printf 'gh_authenticated=%s\n' "$gh_auth"`,
		`push_credential=0`,
		`if [ "$gh_auth" -eq 1 ]; then`,
		`  if printf '%s' "$gh_status" | grep -q "Token scopes:"; then`,
		`    if printf '%s' "$gh_status" | grep -q "'repo'"; then push_credential=1; fi`,
		`  else`,
		`    push_credential=1`,
		`  fi`,
		"fi",
		`if [ "$push_credential" -eq 0 ] && { [ -n "${GH_TOKEN:-}" ] || [ -n "${GITHUB_TOKEN:-}" ]; }; then push_credential=1; fi`,
		`if [ "$push_credential" -eq 0 ] && [ -n "$host" ] && command -v ssh >/dev/null 2>&1; then`,
		`  case "$remote" in`,
		`    git@*|ssh://*)`,
		`      ssh_exit=0`,
		`      ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new -T "git@$host" >/dev/null 2>&1 || ssh_exit=$?`,
		`      if [ "$ssh_exit" -ne 255 ]; then push_credential=1; fi`,
		`      ;;`,
		"  esac",
		"fi",
		`printf 'push_credential=%s\n' "$push_credential"`,
	}, "\n")
}

func parseGitPushAccessReport(stdout string) GitPushAccessStatus {
	var status GitPushAccessStatus
	for _, line := range strings.Split(stdout, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.TrimSpace(key) {
		case "remote":
			status.RemoteURL = value
		case "fetch_ok":
			status.FetchOK = value == "1"
		case "gh_authenticated":
			status.GHAuthenticated = value == "1"
		case "push_credential":
			status.PushCredential = value == "1"
		}
	}
	return status
}
