package erunmcp

import (
	"fmt"

	eruncommon "github.com/sophium/erun/erun-common"
)

// writeDoctorGitPushAccess is the MCP counterpart of the CLI's
// reportGitPushAccess (erun-cli/cmd/doctor_git_push_access.go): a public
// GitHub repository fetches anonymously for the entire life of a piece of
// work, so an environment whose worktree lives in a pod can look completely
// healthy right up to the moment an agent tries to push a branch or use gh --
// the worst possible time to discover there is no credential. Runs only for
// an environment whose worktree actually lives in a pod; a local-agent env
// pushes from the operator's own machine.
func writeDoctorGitPushAccess(runCtx eruncommon.Context, target eruncommon.OpenResult, req eruncommon.ShellLaunchParams) error {
	if !target.EnvConfig.RemoteWorktree() {
		return nil
	}
	status, err := eruncommon.InspectGitPushAccess(runCtx, nil, req, target.RepoPath)
	if err != nil || runCtx.DryRun || status.RemoteURL == "" {
		return err
	}
	_, err = fmt.Fprintf(runCtx.Stdout, "== Git push access ==\nRemote: %s\nFetch:  %s\nPush:   %s\n\n",
		status.RemoteURL, mcpGitFetchStatusLine(status), mcpGitPushStatusLine(target, status))
	return err
}

func mcpGitFetchStatusLine(status eruncommon.GitPushAccessStatus) string {
	if status.FetchOK {
		return "ok"
	}
	return "FAILED — this environment cannot even read the remote"
}

func mcpGitPushStatusLine(target eruncommon.OpenResult, status eruncommon.GitPushAccessStatus) string {
	if status.PushCredential {
		return "credential configured (gh session, GH_TOKEN/GITHUB_TOKEN, or an SSH key the remote accepts)"
	}
	remedy := fmt.Sprintf(
		"From an interactive shell in this environment (erun open %s %s), authenticate once with `gh auth login -h <host>` "+
			"(or `gh auth login -h <host> --with-token < token-file` to avoid the browser flow entirely). "+
			"Never attempt this from an unattended agent run -- gh's device-code/browser flow cannot complete headlessly, there is nobody to open the URL. "+
			"The credential persists on this environment's home volume across restarts.",
		target.Tenant, target.Environment)
	if status.FetchOK {
		return fmt.Sprintf("NO CREDENTIAL — this environment can fetch but cannot push a branch, open a PR, or read/comment on an issue. %s", remedy)
	}
	return fmt.Sprintf("NO CREDENTIAL — %s", remedy)
}
