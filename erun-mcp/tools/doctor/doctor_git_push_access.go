package doctor

import (
	"fmt"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/gitx"
	"github.com/sophium/erun/erun-common/primitive/deploy"
)

// writeDoctorGitPushAccess checks push credentials for a pod-backed worktree,
// since public repos fetch anonymously and the gap only shows at push time.
func writeDoctorGitPushAccess(runCtx core.Context, target config.OpenResult, req config.ShellLaunchParams, diagnosis deploy.DeployDiagnosisResult) error {
	if !target.EnvConfig.RemoteWorktree() {
		return nil
	}
	if diagnosis.ClusterUnreachable {
		return writeDoctorPodUnreachableSkip(runCtx, "Git push access")
	}
	status, err := gitx.InspectGitPushAccess(runCtx, nil, req, target.RepoPath)
	if err != nil || runCtx.DryRun || status.RemoteURL == "" {
		return err
	}
	_, err = fmt.Fprintf(runCtx.Stdout, "== Git push access ==\nRemote: %s\nFetch:  %s\nPush:   %s\n\n",
		status.RemoteURL, mcpGitFetchStatusLine(status), mcpGitPushStatusLine(target, status))
	return err
}

func mcpGitFetchStatusLine(status gitx.GitPushAccessStatus) string {
	if status.FetchOK {
		return "ok"
	}
	return "FAILED — this environment cannot even read the remote"
}

func mcpGitPushStatusLine(target config.OpenResult, status gitx.GitPushAccessStatus) string {
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
