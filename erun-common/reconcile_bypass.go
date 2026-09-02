package eruncommon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// reconcile_bypass.go implements the read half of erun#1912's recorded
// decision (see erun-backend-api/AGENTS.md "GitHub branch protection cannot
// tell the queue's push from a bypass"): narrowing who holds the ruleset's
// bypass grant to a dedicated queue identity is ops-side work outside this
// codebase, but a bypass is structurally unavoidable for the pull_request
// rule as long as the queue pushes raw commits instead of merging through
// the GitHub API. So every bypassed push should be checkable after the fact
// against a real gate run, rather than trusted because of who pushed it.
//
// GitHub's rule-suites API (`GET .../rulesets/rule-suites`) is the ledger of
// every push GitHub evaluated against a ruleset, including which ones used a
// bypass; gate_runs (erun#1931) is erun's own record of what actually gated
// green. This cross-references the two: a bypassed push whose landed commit
// (after_sha) matches no PASSED gate run's mergeCommit for the same target
// branch is reported unreconciled, loudly.

// ReconcileBypassParams is the `erun exec reconcile-bypass` input.
type ReconcileBypassParams struct {
	// RemoteURL is the github.com remote the ruleset lives on.
	RemoteURL string
	// RulesetID is the specific ruleset to check bypasses against. Required
	// and never defaulted: a bypass GitHub attributes to some other ruleset
	// must not be silently folded into this one's reconciliation.
	RulesetID int64
	// TargetBranch is the ruleset's protected branch, e.g. "main".
	TargetBranch string
	// Since narrows the GitHub lookup window to one of rule-suites' own
	// time_period values (hour, day, week, month). Empty keeps GitHub's own
	// default window.
	Since string
}

// BypassedPush is one push GitHub's rule-suites ledger recorded as having
// bypassed a rule that RulesetID enforces on the target branch.
type BypassedPush struct {
	RuleSuiteID int64  `json:"ruleSuiteId"`
	Actor       string `json:"actor"`
	// Commit is the push's after_sha -- the commit that actually landed on
	// the target branch.
	Commit   string `json:"commit"`
	PushedAt string `json:"pushedAt"`
	// BypassedRule names which of the ruleset's own rules was bypassed for
	// this push, e.g. "pull_request".
	BypassedRule string `json:"bypassedRule"`
	// Reconciled is true when a PASSED gate run's mergeCommit exactly
	// matches Commit.
	Reconciled bool   `json:"reconciled"`
	GateRunID  string `json:"gateRunId,omitempty"`
}

// ReconcileBypassResult is the full reconciliation report.
type ReconcileBypassResult struct {
	Owner        string         `json:"owner"`
	Repo         string         `json:"repo"`
	RulesetID    int64          `json:"rulesetId"`
	TargetBranch string         `json:"targetBranch"`
	Pushes       []BypassedPush `json:"pushes"`
	// Unreconciled counts pushes with no matching PASSED gate run -- the
	// number a caller should treat as loud, not zero-value noise.
	Unreconciled int `json:"unreconciled"`
}

// ReconcileBypassDependencies lets tests replace the GitHub HTTP call and gh
// token resolution without a real network or a real gh CLI.
type ReconcileBypassDependencies struct {
	Client       *http.Client
	ResolveToken func(owner string) (string, bool)
}

func normalizeReconcileBypassDependencies(deps ReconcileBypassDependencies) ReconcileBypassDependencies {
	if deps.Client == nil {
		deps.Client = &http.Client{Timeout: 15 * time.Second}
	}
	if deps.ResolveToken == nil {
		deps.ResolveToken = resolveGitHubAPIToken
	}
	return deps
}

// maxRuleSuitePages bounds the rule-suites pagination walk so a long branch
// history (or a wide --since) cannot make this loop forever. A capped run
// reports that it stopped early rather than silently mistaking a partial
// result for a complete one.
const maxRuleSuitePages = 20

// reconcileBypassInputs is ReconcileBypass's validated, trimmed input, kept
// separate from ReconcileBypassParams so validation happens exactly once.
type reconcileBypassInputs struct {
	owner, repo, targetBranch, since string
	rulesetID                        int64
}

func resolveReconcileBypassInputs(ctx Context, params ReconcileBypassParams) (reconcileBypassInputs, error) {
	ctx.Trace("reconcile-bypass: resolving inputs")
	target := strings.TrimSpace(params.TargetBranch)
	if target == "" {
		ctx.Trace("reconcile-bypass: input resolution failed: target branch is required")
		return reconcileBypassInputs{}, fmt.Errorf("target branch is required")
	}
	if params.RulesetID <= 0 {
		ctx.Trace("reconcile-bypass: input resolution failed: ruleset id is required")
		return reconcileBypassInputs{}, fmt.Errorf("ruleset id is required")
	}
	owner, repo, err := parseGitHubOwnerRepo(params.RemoteURL)
	if err != nil {
		ctx.Trace("reconcile-bypass: remote-url resolution failed: " + err.Error())
		return reconcileBypassInputs{}, err
	}
	ctx.Trace(fmt.Sprintf("reconcile-bypass: owner = %s, repo = %s, rulesetId = %d, targetBranch = %s", owner, repo, params.RulesetID, target))
	return reconcileBypassInputs{
		owner: owner, repo: repo, targetBranch: target,
		rulesetID: params.RulesetID, since: strings.TrimSpace(params.Since),
	}, nil
}

// ReconcileBypass cross-references GitHub's own bypass ledger for
// params.RulesetID/params.TargetBranch against gate_runs, and reports every
// bypassed push next to whether a PASSED gate run's mergeCommit covers it.
func ReconcileBypass(ctx Context, store CloudReadStore, alias string, params ReconcileBypassParams, cloudDeps CloudDependencies, deps ReconcileBypassDependencies) (ReconcileBypassResult, error) {
	inputs, err := resolveReconcileBypassInputs(ctx, params)
	if err != nil {
		return ReconcileBypassResult{}, err
	}
	deps = normalizeReconcileBypassDependencies(deps)
	result := ReconcileBypassResult{Owner: inputs.owner, Repo: inputs.repo, RulesetID: inputs.rulesetID, TargetBranch: inputs.targetBranch}

	listURL := githubRuleSuitesURL(inputs.owner, inputs.repo, inputs.targetBranch, inputs.since)
	ctx.Trace("github: GET " + listURL)

	client, provider, err := newPlatformClientForAlias(ctx, store, alias, cloudDeps)
	if err != nil {
		return ReconcileBypassResult{}, err
	}
	filter := PlatformGateRunFilter{TargetBranch: inputs.targetBranch, Status: "PASSED"}
	tracePlatformCall(ctx, provider, "GET", "/v1/gate-runs", gateRunFilterTraceDetails(filter)...)
	if ctx.DryRun {
		return result, nil
	}

	ctx.Trace("reconcile-bypass: resolving a github token")
	token, ok := deps.ResolveToken(inputs.owner)
	if !ok {
		ctx.Trace("reconcile-bypass: token resolution failed: no gh CLI session or GITHUB_TOKEN/GH_TOKEN set")
		return ReconcileBypassResult{}, fmt.Errorf(
			"no GitHub token available to read rule suites; run 'gh auth login' or set GITHUB_TOKEN")
	}
	return finishReconcileBypass(ctx, client, deps.Client, token, listURL, inputs, filter, result)
}

// finishReconcileBypass runs the networked half of ReconcileBypass, isolated
// so the validation and dry-run branching above it don't inflate that
// function's complexity -- the same split ClosePullRequest uses.
func finishReconcileBypass(ctx Context, client *PlatformClient, httpClient *http.Client, token, listURL string, inputs reconcileBypassInputs, filter PlatformGateRunFilter, result ReconcileBypassResult) (ReconcileBypassResult, error) {
	suites, truncated, err := listBypassedRuleSuites(context.Background(), httpClient, token, listURL)
	if err != nil {
		return ReconcileBypassResult{}, err
	}
	if truncated {
		ctx.Info(fmt.Sprintf("reconcile-bypass: stopped after %d pages of rule suites; narrow --since to see the rest", maxRuleSuitePages))
	}
	pushes, err := resolveBypassedPushesForRuleset(context.Background(), httpClient, token, inputs.owner, inputs.repo, inputs.rulesetID, suites)
	if err != nil {
		return ReconcileBypassResult{}, err
	}

	passed, err := client.ListGateRuns(context.Background(), filter)
	if err != nil {
		return ReconcileBypassResult{}, fmt.Errorf("list passed gate runs: %w", err)
	}
	reconcileAgainstGateRuns(pushes, passed)

	result.Pushes = pushes
	for _, push := range pushes {
		if !push.Reconciled {
			result.Unreconciled++
		}
	}
	return result, nil
}

// githubRuleSuiteSummary is one entry from GitHub's rule-suites list.
type githubRuleSuiteSummary struct {
	ID        int64  `json:"id"`
	ActorName string `json:"actor_name"`
	AfterSHA  string `json:"after_sha"`
	PushedAt  string `json:"pushed_at"`
	Result    string `json:"result"`
}

type githubRuleEvaluation struct {
	RuleSource struct {
		Type string `json:"type"`
		ID   int64  `json:"id"`
	} `json:"rule_source"`
	Result   string `json:"result"`
	RuleType string `json:"rule_type"`
}

type githubRuleSuiteDetail struct {
	RuleEvaluations []githubRuleEvaluation `json:"rule_evaluations"`
}

// githubRuleSuitesURL builds the bypassed-rule-suites lookup URL for owner/
// repo/targetBranch, optionally narrowed by since (rule-suites' own
// time_period values: hour, day, week, month).
func githubRuleSuitesURL(owner, repo, targetBranch, since string) string {
	values := url.Values{
		"ref":               {"refs/heads/" + targetBranch},
		"rule_suite_result": {"bypass"},
		"per_page":          {"100"},
	}
	if since != "" {
		values.Set("time_period", since)
	}
	return fmt.Sprintf("%srepos/%s/%s/rulesets/rule-suites?%s", githubAPIBaseURL, owner, repo, values.Encode())
}

// listBypassedRuleSuites walks GitHub's rule-suites pagination (via the
// response's Link header) up to maxRuleSuitePages, returning every bypassed
// suite it read and whether it stopped early because more pages remained.
func listBypassedRuleSuites(ctx context.Context, client *http.Client, token, firstURL string) ([]githubRuleSuiteSummary, bool, error) {
	var all []githubRuleSuiteSummary
	next := firstURL
	for page := 0; next != "" && page < maxRuleSuitePages; page++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, next, nil)
		if err != nil {
			return nil, false, fmt.Errorf("build github rule suites request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/vnd.github+json")
		resp, err := client.Do(req)
		if err != nil {
			return nil, false, fmt.Errorf("list rule suites from github: %w", err)
		}
		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, false, fmt.Errorf("read github rule suites response: %w", readErr)
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, false, fmt.Errorf("github returned %d listing rule suites: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}
		var batch []githubRuleSuiteSummary
		if err := json.Unmarshal(body, &batch); err != nil {
			return nil, false, fmt.Errorf("decode github rule suites response: %w", err)
		}
		all = append(all, batch...)
		next = githubNextPageLink(resp.Header.Get("Link"))
	}
	return all, next != "", nil
}

// githubNextPageLink extracts the rel="next" URL from a GitHub Link header,
// or "" when there is no next page.
func githubNextPageLink(linkHeader string) string {
	for _, part := range strings.Split(linkHeader, ",") {
		segments := strings.Split(strings.TrimSpace(part), ";")
		if len(segments) < 2 {
			continue
		}
		rawURL := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(segments[0]), "<"), ">")
		for _, seg := range segments[1:] {
			if strings.TrimSpace(seg) == `rel="next"` {
				return rawURL
			}
		}
	}
	return ""
}

// resolveBypassedPushesForRuleset fetches each bypassed suite's own detail
// and keeps only the pushes where rulesetID's own rule -- not some other
// ruleset's -- is the one a rule evaluation shows actually failing (and
// therefore needed the bypass).
func resolveBypassedPushesForRuleset(ctx context.Context, client *http.Client, token, owner, repo string, rulesetID int64, suites []githubRuleSuiteSummary) ([]BypassedPush, error) {
	pushes := make([]BypassedPush, 0, len(suites))
	for _, suite := range suites {
		if suite.Result != "bypass" {
			continue
		}
		detail, err := getGitHubRuleSuiteDetail(ctx, client, token, owner, repo, suite.ID)
		if err != nil {
			return nil, err
		}
		rule, ok := bypassedRuleForRuleset(detail.RuleEvaluations, rulesetID)
		if !ok {
			continue
		}
		pushes = append(pushes, BypassedPush{
			RuleSuiteID: suite.ID, Actor: suite.ActorName, Commit: suite.AfterSHA,
			PushedAt: suite.PushedAt, BypassedRule: rule,
		})
	}
	return pushes, nil
}

// bypassedRuleForRuleset returns the rule_type of the first evaluation that
// belongs to rulesetID and would have failed without the bypass.
func bypassedRuleForRuleset(evaluations []githubRuleEvaluation, rulesetID int64) (string, bool) {
	for _, eval := range evaluations {
		if eval.RuleSource.Type == "ruleset" && eval.RuleSource.ID == rulesetID && eval.Result == "fail" {
			return eval.RuleType, true
		}
	}
	return "", false
}

func getGitHubRuleSuiteDetail(ctx context.Context, client *http.Client, token, owner, repo string, id int64) (githubRuleSuiteDetail, error) {
	requestURL := fmt.Sprintf("%srepos/%s/%s/rulesets/rule-suites/%d", githubAPIBaseURL, owner, repo, id)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return githubRuleSuiteDetail{}, fmt.Errorf("build github rule suite detail request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return githubRuleSuiteDetail{}, fmt.Errorf("get github rule suite %d: %w", id, err)
	}
	body, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if readErr != nil {
		return githubRuleSuiteDetail{}, fmt.Errorf("read github rule suite %d response: %w", id, readErr)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return githubRuleSuiteDetail{}, fmt.Errorf("github returned %d fetching rule suite %d: %s", resp.StatusCode, id, strings.TrimSpace(string(body)))
	}
	var detail githubRuleSuiteDetail
	if err := json.Unmarshal(body, &detail); err != nil {
		return githubRuleSuiteDetail{}, fmt.Errorf("decode github rule suite %d response: %w", id, err)
	}
	return detail, nil
}

// reconcileAgainstGateRuns marks each push reconciled when a PASSED gate
// run's mergeCommit exactly matches the push's landed commit.
func reconcileAgainstGateRuns(pushes []BypassedPush, passed []PlatformGateRun) {
	byCommit := make(map[string]string, len(passed))
	for _, run := range passed {
		if run.MergeCommit != "" {
			byCommit[run.MergeCommit] = run.GateRunID
		}
	}
	for i := range pushes {
		if gateRunID, ok := byCommit[pushes[i].Commit]; ok {
			pushes[i].Reconciled = true
			pushes[i].GateRunID = gateRunID
		}
	}
}
