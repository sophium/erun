package eruncommon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// A required GitHub status check has nothing to require until something
// reports a status under its context. The merge queue gate (`erun exec
// gate-merge`, `erun build`, `erun review record-build --gate`) already knows
// whether the prospective merge is green or red; this is the missing last
// step that turns that knowledge into a GitHub commit status, so branch
// protection on the target branch can finally require it.
//
// The status always targets the review's source-branch tip commit, never the
// local prospective squash-merge commit gate-merge produces: GitHub only
// evaluates a required check against a commit reachable from the open pull
// request, and the squash commit never exists there until after the gate has
// already passed and pushed.

// ReportCommitStatusState is a state GitHub's Statuses API accepts.
type ReportCommitStatusState string

const (
	CommitStatusSuccess ReportCommitStatusState = "success"
	CommitStatusFailure ReportCommitStatusState = "failure"
	CommitStatusError   ReportCommitStatusState = "error"
	CommitStatusPending ReportCommitStatusState = "pending"
)

// DefaultCommitStatusContext is the check name a required-status-checks rule
// points at when the caller does not name a more specific one.
const DefaultCommitStatusContext = "erun/merge-gate"

// ReportCommitStatusParams is the `erun exec report-commit-status` input.
type ReportCommitStatusParams struct {
	// RemoteURL is the github.com remote the status is reported against, in
	// any form git accepts (ssh, https, with or without a .git suffix).
	RemoteURL string
	// Commit is the full commit SHA the status attaches to.
	Commit string
	// State is the outcome to report.
	State ReportCommitStatusState
	// Context is the check name a required-status-checks rule names. Empty
	// defaults to DefaultCommitStatusContext.
	Context string
	// Description is a short, human-readable summary, naming which gate step
	// failed when State is not success.
	Description string
	// TargetURL is an optional link a reader clicks through to from the
	// status (e.g. a build log).
	TargetURL string
}

// ReportCommitStatusResult is what was actually reported.
type ReportCommitStatusResult struct {
	Owner   string `json:"owner"`
	Repo    string `json:"repo"`
	Commit  string `json:"commit"`
	State   string `json:"state"`
	Context string `json:"context"`
}

// ReportCommitStatusDependencies lets tests replace the HTTP call and gh
// token resolution without a real network or a real gh CLI.
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

var validCommitStatusStates = map[ReportCommitStatusState]bool{
	CommitStatusSuccess: true,
	CommitStatusFailure: true,
	CommitStatusError:   true,
	CommitStatusPending: true,
}

// ReportCommitStatus reports a commit status on GitHub for params.Commit, the
// SHA a required status check on the remote's branch protection can point at.
func ReportCommitStatus(ctx Context, params ReportCommitStatusParams, deps ReportCommitStatusDependencies) (ReportCommitStatusResult, error) {
	ctx.Trace("report-commit-status: resolving commit")
	commit := strings.TrimSpace(params.Commit)
	if commit == "" {
		ctx.Trace("report-commit-status: commit resolution failed: commit is required")
		return ReportCommitStatusResult{}, fmt.Errorf("commit is required")
	}
	ctx.Trace("report-commit-status: commit = " + commit)

	ctx.Trace("report-commit-status: resolving state")
	if !validCommitStatusStates[params.State] {
		ctx.Trace(fmt.Sprintf("report-commit-status: state resolution failed: %q is not one of success, failure, error, pending", params.State))
		return ReportCommitStatusResult{}, fmt.Errorf("state %q is not one of success, failure, error, pending", params.State)
	}
	ctx.Trace("report-commit-status: state = " + string(params.State))

	ctx.Trace("report-commit-status: resolving description")
	if strings.TrimSpace(params.Description) == "" {
		ctx.Trace("report-commit-status: description resolution failed: description is required")
		return ReportCommitStatusResult{}, fmt.Errorf("description is required")
	}

	ctx.Trace("report-commit-status: resolving owner/repo from remote-url")
	owner, repo, err := parseGitHubOwnerRepo(params.RemoteURL)
	if err != nil {
		ctx.Trace("report-commit-status: remote-url resolution failed: " + err.Error())
		return ReportCommitStatusResult{}, err
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
		githubAPIBaseURL, owner, repo, commit, params.State, statusContext, params.Description,
	))
	result := ReportCommitStatusResult{Owner: owner, Repo: repo, Commit: commit, State: string(params.State), Context: statusContext}
	if ctx.DryRun {
		return result, nil
	}

	ctx.Trace("report-commit-status: resolving a github token")
	token, ok := deps.ResolveToken(owner)
	if !ok {
		ctx.Trace("report-commit-status: token resolution failed: no gh CLI session or GITHUB_TOKEN/GH_TOKEN set")
		return ReportCommitStatusResult{}, fmt.Errorf(
			"no GitHub token available to report a commit status; run 'gh auth login' or set GITHUB_TOKEN")
	}
	body, err := json.Marshal(commitStatusRequestBody{
		State:       string(params.State),
		Context:     statusContext,
		Description: params.Description,
		TargetURL:   strings.TrimSpace(params.TargetURL),
	})
	if err != nil {
		return ReportCommitStatusResult{}, fmt.Errorf("encode commit status body: %w", err)
	}
	if err := postGitHubCommitStatus(context.Background(), deps.Client, token, owner, repo, commit, body); err != nil {
		return ReportCommitStatusResult{}, err
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
	url := fmt.Sprintf("%srepos/%s/%s/statuses/%s", githubAPIBaseURL, owner, repo, commit)
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

// resolveGitHubAPIToken resolves a gh-issued token for the general GitHub
// REST API, mirroring resolveGHCRBasicAuth's gh-then-env-var precedence
// without the ghcr-specific basic-auth wrapping.
func resolveGitHubAPIToken(owner string) (string, bool) {
	if token, ok := resolveGHCRTokenViaGH(owner); ok {
		return token, true
	}
	return ghcrTokenFromEnv()
}

// parseGitHubOwnerRepo extracts owner/repo from a github.com remote URL in
// any form git accepts: git@github.com:owner/repo(.git), https://github.com/
// owner/repo(.git), ssh://git@github.com/owner/repo(.git).
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
