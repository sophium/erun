package eruncommon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// close_pull_request.go closes the GitHub pull request a merge queue gate
// actually shipped. `erun exec gate-merge` lands a squash commit whose SHA is
// not the branch head GitHub tracks, so GitHub never reconciles a queued
// merge with its open pull request on its own -- the PR stays open forever
// with no link to what actually shipped (erun#1895). This runs after `erun
// review report-merged` has already succeeded and closes that gap: find the
// branch's open pull request (a no-op, not an error, when there is none),
// refuse loudly if its head moved since the gate fetched it, otherwise
// record the landing commit in a comment and close it.

// ClosePullRequestParams is the `erun exec close-pr` input.
type ClosePullRequestParams struct {
	// RemoteURL is the github.com remote the pull request lives on.
	RemoteURL string
	// Branch is the source branch whose open pull request should close --
	// the review's sourceBranch.
	Branch string
	// TargetBranch is the pull request's base branch -- the review's
	// targetBranch. Disambiguates when Branch has open pull requests
	// against more than one base.
	TargetBranch string
	// GatedCommit is Branch's tip at the moment the gate actually fetched
	// and tested it (GateMergeWorkingTreeResult.SourceCommit). Closing is
	// refused when the pull request's current head no longer matches this:
	// something pushed to Branch after the gate fetched it, so the gated
	// content is not what closing would discard.
	GatedCommit string
	// LandingCommit is the commit that actually landed on TargetBranch,
	// recorded in a comment on the pull request so a later reader does not
	// have to search TargetBranch's history for it.
	LandingCommit string
}

// ClosePullRequestResult is what actually happened.
type ClosePullRequestResult struct {
	Owner  string `json:"owner"`
	Repo   string `json:"repo"`
	Branch string `json:"branch"`
	// Found is false when Branch has no open pull request against
	// TargetBranch -- a queued plain branch is legitimate, so this is a
	// no-op result, not an error.
	Found  bool `json:"found"`
	Number int  `json:"number,omitempty"`
	Closed bool `json:"closed"`
}

// ClosePullRequestHeadMovedError reports that Branch's open pull request's
// head no longer matches GatedCommit -- the same class of loud, named
// refusal `erun exec push`'s own branch-mismatch check gives, because
// closing the pull request here would silently discard whatever moved the
// branch after the gate fetched it.
type ClosePullRequestHeadMovedError struct {
	Branch, GatedCommit, ActualHead string
	Number                          int
}

func (e *ClosePullRequestHeadMovedError) Error() string {
	return fmt.Sprintf(
		"refusing to close pull request #%d for %s: its head is %s, not %s -- the commit the gate actually tested. Something pushed to %s after the gate fetched it, so the gated content is not what closing would discard.",
		e.Number, e.Branch, e.ActualHead, e.GatedCommit, e.Branch,
	)
}

// ClosePullRequestDependencies lets tests replace the HTTP call and gh token
// resolution without a real network or a real gh CLI.
type ClosePullRequestDependencies struct {
	Client       *http.Client
	ResolveToken func(owner string) (string, bool)
}

func normalizeClosePullRequestDependencies(deps ClosePullRequestDependencies) ClosePullRequestDependencies {
	if deps.Client == nil {
		deps.Client = &http.Client{Timeout: 15 * time.Second}
	}
	if deps.ResolveToken == nil {
		deps.ResolveToken = resolveGitHubAPIToken
	}
	return deps
}

// closePullRequestInputs is ClosePullRequest's validated, trimmed input, kept
// separate from ClosePullRequestParams so validation happens exactly once.
type closePullRequestInputs struct {
	branch, target, gatedCommit, landingCommit string
	owner, repo                                string
}

// resolveClosePullRequestInputs validates and traces ClosePullRequest's
// inputs, isolated so ClosePullRequest itself stays under the module's
// cyclomatic complexity limit.
func resolveClosePullRequestInputs(ctx Context, params ClosePullRequestParams) (closePullRequestInputs, error) {
	ctx.Trace("close-pr: resolving inputs")
	inputs := closePullRequestInputs{
		branch:        strings.TrimSpace(params.Branch),
		target:        strings.TrimSpace(params.TargetBranch),
		gatedCommit:   strings.TrimSpace(params.GatedCommit),
		landingCommit: strings.TrimSpace(params.LandingCommit),
	}
	if inputs.branch == "" || inputs.target == "" || inputs.gatedCommit == "" || inputs.landingCommit == "" {
		ctx.Trace("close-pr: input resolution failed: branch, target branch, gated commit, and landing commit are all required")
		return closePullRequestInputs{}, fmt.Errorf("branch, target branch, gated commit, and landing commit are all required")
	}

	ctx.Trace("close-pr: resolving owner/repo from remote-url")
	owner, repo, err := parseGitHubOwnerRepo(params.RemoteURL)
	if err != nil {
		ctx.Trace("close-pr: remote-url resolution failed: " + err.Error())
		return closePullRequestInputs{}, err
	}
	ctx.Trace(fmt.Sprintf("close-pr: owner = %s, repo = %s", owner, repo))
	inputs.owner, inputs.repo = owner, repo
	return inputs, nil
}

// ClosePullRequest finds Branch's open pull request against TargetBranch on
// GitHub, refuses if its head has moved past GatedCommit, otherwise comments
// LandingCommit on it and closes it.
func ClosePullRequest(ctx Context, params ClosePullRequestParams, deps ClosePullRequestDependencies) (ClosePullRequestResult, error) {
	inputs, err := resolveClosePullRequestInputs(ctx, params)
	if err != nil {
		return ClosePullRequestResult{}, err
	}
	deps = normalizeClosePullRequestDependencies(deps)
	result := ClosePullRequestResult{Owner: inputs.owner, Repo: inputs.repo, Branch: inputs.branch}

	listURL := githubListPullRequestsURL(inputs.owner, inputs.repo, inputs.branch, inputs.target)
	ctx.Trace("github: GET " + listURL)
	if ctx.DryRun {
		return result, nil
	}

	ctx.Trace("close-pr: resolving a github token")
	token, ok := deps.ResolveToken(inputs.owner)
	if !ok {
		ctx.Trace("close-pr: token resolution failed: no gh CLI session or GITHUB_TOKEN/GH_TOKEN set")
		return ClosePullRequestResult{}, fmt.Errorf(
			"no GitHub token available to close a pull request; run 'gh auth login' or set GITHUB_TOKEN")
	}
	return findAndClosePullRequest(ctx, deps, listURL, token, inputs, result)
}

// findAndClosePullRequest runs the mutating half of ClosePullRequest, isolated
// so the validation and dry-run branching above it don't inflate that
// function's complexity -- the same split GateMergeWorkingTree uses.
func findAndClosePullRequest(ctx Context, deps ClosePullRequestDependencies, listURL, token string, inputs closePullRequestInputs, result ClosePullRequestResult) (ClosePullRequestResult, error) {
	pulls, err := listOpenGitHubPullRequests(context.Background(), deps.Client, token, listURL, inputs.owner, inputs.repo)
	if err != nil {
		return ClosePullRequestResult{}, err
	}
	if len(pulls) == 0 {
		ctx.Trace(fmt.Sprintf("close-pr: no open pull request for %s -> %s, nothing to close", inputs.branch, inputs.target))
		return result, nil
	}
	if len(pulls) > 1 {
		return ClosePullRequestResult{}, fmt.Errorf(
			"found %d open pull requests for %s -> %s; expected at most one", len(pulls), inputs.branch, inputs.target)
	}
	pull := pulls[0]
	result.Number = pull.Number

	if pull.Head.SHA != inputs.gatedCommit {
		return ClosePullRequestResult{}, &ClosePullRequestHeadMovedError{
			Branch: inputs.branch, GatedCommit: inputs.gatedCommit, ActualHead: pull.Head.SHA, Number: pull.Number,
		}
	}

	comment := fmt.Sprintf("Merged via the erun merge queue as %s on %s.", inputs.landingCommit, inputs.target)
	if err := postGitHubIssueComment(context.Background(), deps.Client, token, inputs.owner, inputs.repo, pull.Number, comment); err != nil {
		return ClosePullRequestResult{}, err
	}
	if err := closeGitHubPullRequest(context.Background(), deps.Client, token, inputs.owner, inputs.repo, pull.Number); err != nil {
		return ClosePullRequestResult{}, err
	}

	result.Found = true
	result.Closed = true
	return result, nil
}

type githubPullRequest struct {
	Number int `json:"number"`
	Head   struct {
		SHA string `json:"sha"`
	} `json:"head"`
}

// githubListPullRequestsURL builds the open-pull-requests-for-branch lookup
// URL with branch/target properly query-escaped, since either may contain
// characters (like "/") that are only safe inside a query value once encoded.
func githubListPullRequestsURL(owner, repo, branch, target string) string {
	query := url.Values{
		"head":  {owner + ":" + branch},
		"base":  {target},
		"state": {"open"},
	}
	return fmt.Sprintf("%srepos/%s/%s/pulls?%s", githubAPIBaseURL, owner, repo, query.Encode())
}

func listOpenGitHubPullRequests(ctx context.Context, client *http.Client, token, listURL, owner, repo string) ([]githubPullRequest, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, listURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build github list pull requests request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list pull requests from github: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var respBody bytes.Buffer
		_, _ = respBody.ReadFrom(resp.Body)
		return nil, fmt.Errorf("github returned %d listing pull requests for %s/%s: %s",
			resp.StatusCode, owner, repo, strings.TrimSpace(respBody.String()))
	}
	var pulls []githubPullRequest
	if err := json.NewDecoder(resp.Body).Decode(&pulls); err != nil {
		return nil, fmt.Errorf("decode github pull requests response: %w", err)
	}
	return pulls, nil
}

func postGitHubIssueComment(ctx context.Context, client *http.Client, token, owner, repo string, number int, body string) error {
	encoded, err := json.Marshal(struct {
		Body string `json:"body"`
	}{Body: body})
	if err != nil {
		return fmt.Errorf("encode pull request comment body: %w", err)
	}
	url := fmt.Sprintf("%srepos/%s/%s/issues/%d/comments", githubAPIBaseURL, owner, repo, number)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("build github issue comment request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("post pull request comment to github: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var respBody bytes.Buffer
		_, _ = respBody.ReadFrom(resp.Body)
		return fmt.Errorf("github returned %d commenting on %s/%s#%d: %s",
			resp.StatusCode, owner, repo, number, strings.TrimSpace(respBody.String()))
	}
	return nil
}

func closeGitHubPullRequest(ctx context.Context, client *http.Client, token, owner, repo string, number int) error {
	encoded, err := json.Marshal(struct {
		State string `json:"state"`
	}{State: "closed"})
	if err != nil {
		return fmt.Errorf("encode pull request close body: %w", err)
	}
	url := fmt.Sprintf("%srepos/%s/%s/pulls/%d", githubAPIBaseURL, owner, repo, number)
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, url, bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("build github close pull request request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("close pull request on github: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var respBody bytes.Buffer
		_, _ = respBody.ReadFrom(resp.Body)
		return fmt.Errorf("github returned %d closing %s/%s#%d: %s",
			resp.StatusCode, owner, repo, number, strings.TrimSpace(respBody.String()))
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}
