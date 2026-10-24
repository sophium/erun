package review

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/registry"
)

// DefaultCommitStatusContext is the default required-check name.
const DefaultCommitStatusContext = "erun/merge-gate"

// ReportCommitStatusDependencies are the HTTP and gh token seams.
type ReportCommitStatusDependencies struct {
	Client       *http.Client
	ResolveToken func(owner string) (string, bool)
}

func normalizeReportCommitStatusDependencies(deps ReportCommitStatusDependencies) ReportCommitStatusDependencies {
	if deps.Client == nil {
		deps.Client = &http.Client{Timeout: 15 * time.Second}
	}
	if deps.ResolveToken == nil {
		deps.ResolveToken = resolveGitHubAPIToken
	}
	return deps
}

var validCommitStatusStates = map[model.ReportCommitStatusState]bool{
	model.CommitStatusSuccess: true,
	model.CommitStatusFailure: true,
	model.CommitStatusError:   true,
	model.CommitStatusPending: true,
}

// ReportCommitStatus reports a GitHub commit status for params.Commit.
func ReportCommitStatus(ctx core.Context, params model.ReportCommitStatusParams, deps ReportCommitStatusDependencies) (model.ReportCommitStatusResult, error) {
	ctx.Trace("report-commit-status: resolving commit")
	commit := strings.TrimSpace(params.Commit)
	if commit == "" {
		ctx.Trace("report-commit-status: commit resolution failed: commit is required")
		return model.ReportCommitStatusResult{}, fmt.Errorf("commit is required")
	}
	ctx.Trace("report-commit-status: commit = " + commit)

	ctx.Trace("report-commit-status: resolving state")
	if !validCommitStatusStates[params.State] {
		ctx.Trace(fmt.Sprintf("report-commit-status: state resolution failed: %q is not one of success, failure, error, pending", params.State))
		return model.ReportCommitStatusResult{}, fmt.Errorf("state %q is not one of success, failure, error, pending", params.State)
	}
	ctx.Trace("report-commit-status: state = " + string(params.State))

	ctx.Trace("report-commit-status: resolving description")
	if strings.TrimSpace(params.Description) == "" {
		ctx.Trace("report-commit-status: description resolution failed: description is required")
		return model.ReportCommitStatusResult{}, fmt.Errorf("description is required")
	}

	ctx.Trace("report-commit-status: resolving owner/repo from remote-url")
	owner, repo, err := parseGitHubOwnerRepo(params.RemoteURL)
	if err != nil {
		ctx.Trace("report-commit-status: remote-url resolution failed: " + err.Error())
		return model.ReportCommitStatusResult{}, err
	}
	ctx.Trace(fmt.Sprintf("report-commit-status: owner = %s, repo = %s", owner, repo))

	statusContext := strings.TrimSpace(params.Context)
	if statusContext == "" {
		statusContext = DefaultCommitStatusContext
		ctx.Trace("report-commit-status: context not given, defaulting to " + statusContext)
	} else {
		ctx.Trace("report-commit-status: context = " + statusContext)
	}
	deps = normalizeReportCommitStatusDependencies(deps)

	ctx.Trace(fmt.Sprintf(
		"github: POST %srepos/%s/%s/statuses/%s (state=%s, context=%s, description=%q)",
		registry.GithubAPIBaseURL, owner, repo, commit, params.State, statusContext, params.Description,
	))
	result := model.ReportCommitStatusResult{Owner: owner, Repo: repo, Commit: commit, State: string(params.State), Context: statusContext}
	if ctx.DryRun {
		return result, nil
	}

	ctx.Trace("report-commit-status: resolving a github token")
	token, ok := deps.ResolveToken(owner)
	if !ok {
		ctx.Trace("report-commit-status: token resolution failed: no gh CLI session or GITHUB_TOKEN/GH_TOKEN set")
		return model.ReportCommitStatusResult{}, fmt.Errorf(
			"no GitHub token available to report a commit status; run 'gh auth login' or set GITHUB_TOKEN")
	}
	body, err := json.Marshal(commitStatusRequestBody{
		State:       string(params.State),
		Context:     statusContext,
		Description: params.Description,
		TargetURL:   strings.TrimSpace(params.TargetURL),
	})
	if err != nil {
		return model.ReportCommitStatusResult{}, fmt.Errorf("encode commit status body: %w", err)
	}
	if err := postGitHubCommitStatus(context.Background(), deps.Client, token, owner, repo, commit, body); err != nil {
		return model.ReportCommitStatusResult{}, err
	}
	return result, nil
}

type commitStatusRequestBody struct {
	State       string `json:"state"`
	Context     string `json:"context"`
	Description string `json:"description,omitempty"`
	TargetURL   string `json:"target_url,omitempty"`
}

func postGitHubCommitStatus(ctx context.Context, client *http.Client, token, owner, repo, commit string, body []byte) error {
	url := fmt.Sprintf("%srepos/%s/%s/statuses/%s", registry.GithubAPIBaseURL, owner, repo, commit)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build github commit status request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("report commit status to github: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var respBody bytes.Buffer
		_, _ = respBody.ReadFrom(resp.Body)
		return fmt.Errorf("github returned %d reporting commit status on %s/%s@%s: %s",
			resp.StatusCode, owner, repo, commit, strings.TrimSpace(respBody.String()))
	}
	return nil
}

// resolveGitHubAPIToken uses resolveGHCRBasicAuth's gh-then-env precedence.
func resolveGitHubAPIToken(owner string) (string, bool) {
	if token, ok := registry.ResolveGHCRTokenViaGH(owner); ok {
		return token, true
	}
	return registry.GhcrTokenFromEnv()
}

func parseGitHubOwnerRepo(remoteURL string) (owner, repo string, err error) {
	trimmed := strings.TrimSuffix(strings.TrimSpace(remoteURL), ".git")
	if trimmed == "" {
		return "", "", fmt.Errorf("remote-url is required")
	}
	rest, ok := cutGitHubRemotePrefix(trimmed)
	if !ok {
		return "", "", fmt.Errorf("remote-url %q is not a recognized github.com remote", remoteURL)
	}
	rest = strings.Trim(rest, "/")
	owner, repo, ok = strings.Cut(rest, "/")
	if !ok || owner == "" || repo == "" || strings.Contains(repo, "/") {
		return "", "", fmt.Errorf("remote-url %q does not name an owner/repo", remoteURL)
	}
	return owner, repo, nil
}

func cutGitHubRemotePrefix(remoteURL string) (string, bool) {
	for _, prefix := range []string{"git@github.com:", "https://github.com/", "http://github.com/", "ssh://git@github.com/"} {
		if rest, ok := strings.CutPrefix(remoteURL, prefix); ok {
			return rest, true
		}
	}
	return "", false
}
