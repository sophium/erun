package gitx

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/kube"
)

// GitPushAccessStatus reports fetch and push capability separately: a public repo fetches
// anonymously, so a missing push credential otherwise surfaces only at the final push.
type GitPushAccessStatus struct {
	// RemoteURL is empty when there is no origin; other fields are then meaningless.
	RemoteURL string
	FetchOK   bool
	// GHAuthenticated never runs gh login/refresh/switch, whose interactive flow cannot finish in a pod.
	GHAuthenticated bool
	// PushCredential: a gh session counts only with `repo` scope or no scope list (fine-grained
	// tokens list none); GH_TOKEN/GITHUB_TOKEN always count. For SSH, any exit other than 255
	// (ssh's own failure code) means the host accepted the key.
	PushCredential bool
}

// FetchWorksPushDoesNot reports a readable origin with no credential that could push.
func (s GitPushAccessStatus) FetchWorksPushDoesNot() bool {
	return s.RemoteURL != "" && s.FetchOK && !s.PushCredential
}

// InspectGitPushAccess runs a read-only script that never invokes gh's interactive subcommands.
func InspectGitPushAccess(ctx core.Context, runner kube.RemoteCommandRunnerFunc, req config.ShellLaunchParams, repoPath string) (GitPushAccessStatus, error) {
	out, err := kube.RunTracedRemoteCommand(ctx, runner, req, "git-push-access-script", gitPushAccessScript(repoPath))
	if err != nil {
		return GitPushAccessStatus{}, fmt.Errorf("read git push access%s: %w", kube.FormatRemoteCommandStderr(out.Stderr), err)
	}
	if ctx.DryRun {
		return GitPushAccessStatus{}, nil
	}
	return parseGitPushAccessReport(out.Stdout), nil
}

func gitPushAccessScript(repoPath string) string {
	return strings.Join([]string{
		"set -eu",
		fmt.Sprintf("cd %s 2>/dev/null || { printf 'remote=\\n'; exit 0; }", core.ShellQuote(repoPath)),
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
