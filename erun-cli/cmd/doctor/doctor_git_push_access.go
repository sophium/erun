package doctor

import (
	"fmt"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/gitx"
)

// reportGitPushAccess flags a pod worktree with no push credential, which anonymous
// fetches of a public repo hide until the first push. Degrades on an unreachable pod.
func reportGitPushAccess(ctx core.Context, result config.OpenResult, diagnosis model.DeployDiagnosisResult) error {
	if !result.EnvConfig.RemoteWorktree() {
		return nil
	}
	if diagnosis.ClusterUnreachable {
		return reportPodSkippedUnreachable(ctx, "Git push access")
	}
	req := cloud.ShellLaunchParamsFromResult(result)
	status, err := gitx.InspectGitPushAccess(ctx, nil, req, result.RepoPath)
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

func gitFetchStatusLine(status model.GitPushAccessStatus) string {
	if status.FetchOK {
		return "ok"
	}
	return "FAILED — this environment cannot even read the remote"
}

func gitPushStatusLine(result config.OpenResult, status model.GitPushAccessStatus) string {
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
