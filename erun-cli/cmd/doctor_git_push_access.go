package cmd

import (
	"fmt"

	common "github.com/sophium/erun/erun-common"
)

// reportGitPushAccess flags the asymmetry that makes this expensive to
// discover the hard way: a public GitHub repository fetches anonymously for
// the entire life of a piece of work, so an environment whose worktree lives
// in a pod (remote-agent, runtime) can look completely healthy right up to
// the moment an agent tries to push a branch or use gh -- the worst possible
// time to discover there is no credential. Runs only for an environment whose
// worktree actually lives in a pod; a local-agent or host env pushes from the
// operator's own machine, which already carries the operator's own git/gh
// credentials.
//
// The read execs into the runtime pod, which is exactly the state doctor most
// needs to diagnose when it is down -- reading it must degrade to a clear
// "could not read" report rather than aborting the rest of doctor's checks.
func reportGitPushAccess(ctx common.Context, result common.OpenResult) error {
	if !result.EnvConfig.RemoteWorktree() {
		return nil
	}
	req := common.ShellLaunchParamsFromResult(result)
	status, err := common.InspectGitPushAccess(ctx, nil, req, result.RepoPath)
	if err != nil {
		if ctx.DryRun {
			return nil
		}
		return reportPodUnreachable(ctx, "Git push access", err)
	}
	if ctx.DryRun || status.RemoteURL == "" {
		return nil
	}
	_, err = fmt.Fprintf(ctx.Stdout, "== Git push access ==\nRemote: %s\nFetch:  %s\nPush:   %s\n\n",
		status.RemoteURL, gitFetchStatusLine(status), gitPushStatusLine(result, status))
	return err
}

func gitFetchStatusLine(status common.GitPushAccessStatus) string {
	if status.FetchOK {
		return "ok"
	}
	return "FAILED — this environment cannot even read the remote"
}

func gitPushStatusLine(result common.OpenResult, status common.GitPushAccessStatus) string {
	if status.PushCredential {
		return "credential configured (gh session, GH_TOKEN/GITHUB_TOKEN, or an SSH key the remote accepts)"
	}
	remedy := fmt.Sprintf(
		"From a shell in this environment (erun open %s %s), authenticate once with:\n"+
			"  gh auth login -h <host>\n"+
			"or, to avoid gh's browser flow entirely: gh auth login -h <host> --with-token < token-file\n"+
			"Run this only interactively over that shell -- never from an unattended agent run, which cannot complete gh's device-code/browser flow (there is nobody to open the URL).\n"+
			"The credential persists on this environment's home volume across restarts, same as an operator's AWS identity.",
		result.Tenant, result.Environment)
	if status.FetchOK {
		return fmt.Sprintf("NO CREDENTIAL — this environment can fetch (the repository is public, or a credential is already cached for reads) but cannot push a branch, open a PR, or read/comment on an issue. %s", remedy)
	}
	return fmt.Sprintf("NO CREDENTIAL — %s", remedy)
}
