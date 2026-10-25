package gitx

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam/runtimepod"
	"github.com/sophium/erun/erun-common/infra/kube"
)

// InspectGitPushAccess runs a read-only script that never invokes gh's interactive subcommands.
func InspectGitPushAccess(ctx core.Context, runner runtimepod.RemoteCommandRunnerFunc, req config.ShellLaunchParams, repoPath string) (model.GitPushAccessStatus, error) {
	out, err := kube.RunTracedRemoteCommand(ctx, runner, req, "git-push-access-script", gitPushAccessScript(repoPath))
	if err != nil {
		return model.GitPushAccessStatus{}, fmt.Errorf("read git push access%s: %w", kube.FormatRemoteCommandStderr(out.Stderr), err)
	}
	if ctx.DryRun {
		return model.GitPushAccessStatus{}, nil
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

func parseGitPushAccessReport(stdout string) model.GitPushAccessStatus {
	var status model.GitPushAccessStatus
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
