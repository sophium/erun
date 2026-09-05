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

// reconcile_bypass.go implements the read half of the recorded decision on
// ruleset bypasses (see erun-backend-api/AGENTS.md "GitHub branch protection
// cannot tell the queue's push from a bypass"): narrowing who holds the
// bypass grant to a dedicated queue identity is what ruleset_bypass_plan.go
// resolves, but a bypass is structurally unavoidable for the pull_request
// rule as long as the queue pushes raw commits instead of merging through
// the GitHub API. So every bypassed push should be checkable after the fact
// against a real gate run, rather than trusted because of who pushed it --
// and once one identity is supposed to be the only one holding the grant,
// checkable against that too.
//
// GitHub's rule-suites API (`GET .../rulesets/rule-suites`) is the ledger of
// every push GitHub evaluated against a ruleset, including which ones used a
// bypass; gate_runs is erun's own record of what actually gated
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
	// ExpectedActors names the identities allowed to hold the bypass grant
	// at all. Empty accepts any actor and reports on evidence alone; naming
	// them is what makes narrowing the grant to one non-human identity
	// observable rather than merely configured.
	ExpectedActors []string
}

// Verdicts a bypassed push can carry. A push is accounted for when a gate run
// gated exactly what landed, or when a release published it; anything else is
// loud.
const (
	// BypassVerdictReconciled means a PASSED gate run's merge commit is one
	// of the commits this push put on the branch.
	BypassVerdictReconciled = "RECONCILED"
	// BypassVerdictRelease means a tag in the repository points at one of
	// the commits this push carried, so a release -- which stamps, tags and
	// then pushes -- is what landed it. A release is not a gated merge and
	// must not be reported as one.
	BypassVerdictRelease = "RELEASE"
	// BypassVerdictUnexpectedActor means the bypass was exercised by an
	// identity the caller did not name, whatever the evidence says about
	// the content. That is the failure the dedicated-identity narrowing
	// exists to make visible.
	BypassVerdictUnexpectedActor = "UNEXPECTED_ACTOR"
	// BypassVerdictUnreconciled means nothing accounts for what landed.
	BypassVerdictUnreconciled = "UNRECONCILED"
)

// BypassedPush is one push GitHub's rule-suites ledger recorded as having
// bypassed a rule that RulesetID enforces on the target branch.
type BypassedPush struct {
	RuleSuiteID int64  `json:"ruleSuiteId"`
	Actor       string `json:"actor"`
	// Commit is the push's after_sha -- the branch tip this push produced.
	Commit string `json:"commit"`
	// BeforeCommit is the push's before_sha, the tip it moved from. The two
	// bound the range of commits the push actually added, which is what has
	// to be accounted for: a batched merge and every release push carry
	// more than one commit, so the tip alone is not the whole push.
	BeforeCommit string `json:"beforeCommit"`
	PushedAt     string `json:"pushedAt"`
	// BypassedRule names which of the ruleset's own rules was bypassed for
	// this push, e.g. "pull_request".
	BypassedRule string `json:"bypassedRule"`
	// Verdict is one of the BypassVerdict* values.
	Verdict string `json:"verdict"`
	// Reason says why, whenever the verdict is not RECONCILED.
	Reason    string `json:"reason,omitempty"`
	GateRunID string `json:"gateRunId,omitempty"`
	// ReleaseTag names the tag that accounts for a RELEASE verdict.
	ReleaseTag string `json:"releaseTag,omitempty"`
}

// ReconcileBypassResult is the full reconciliation report.
type ReconcileBypassResult struct {
	Owner        string         `json:"owner"`
	Repo         string         `json:"repo"`
	RulesetID    int64          `json:"rulesetId"`
	TargetBranch string         `json:"targetBranch"`
	Pushes       []BypassedPush `json:"pushes"`
	// Unreconciled counts pushes nothing accounts for -- the number a
	// caller should treat as loud, not zero-value noise.
	Unreconciled int `json:"unreconciled"`
	// UnexpectedActors counts pushes an unnamed identity bypassed. Separate
	// from Unreconciled because they are different failures: one says the
	// content was never gated, the other says the wrong identity can push
	// at all.
	UnexpectedActors int `json:"unexpectedActors"`
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
	expectedActors                   []string
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
	owner, repo, err := resolveGitHubRepoFromRemoteOrOrigin(ctx, params.RemoteURL, "reconcile-bypass")
	if err != nil {
		return reconcileBypassInputs{}, err
	}
	expected := trimmedNonEmpty(params.ExpectedActors)
	ctx.Trace(fmt.Sprintf("reconcile-bypass: owner = %s, repo = %s, rulesetId = %d, targetBranch = %s", owner, repo, params.RulesetID, target))
	if len(expected) > 0 {
		ctx.Trace("reconcile-bypass: expected bypass actors = " + strings.Join(expected, ", "))
	} else {
		ctx.Trace("reconcile-bypass: no expected bypass actors named; reporting on gate-run and release evidence alone")
	}
	return reconcileBypassInputs{
		owner: owner, repo: repo, targetBranch: target,
		rulesetID: params.RulesetID, since: strings.TrimSpace(params.Since),
		expectedActors: expected,
	}, nil
}

// resolveGitHubRepoFromRemoteOrOrigin resolves owner/repo from an explicit
// remote URL, falling back to the checkout's own origin. A command that
// always runs against the repository the operator is standing in should not
// make them retype its remote to be usable at all.
func resolveGitHubRepoFromRemoteOrOrigin(ctx Context, remoteURL, traceLabel string) (owner, repo string, err error) {
	remote := strings.TrimSpace(remoteURL)
	if remote == "" {
		ctx.Trace(traceLabel + ": no --remote-url given; reading origin from the current checkout")
		remote, err = originRemoteURL()
		if err != nil {
			ctx.Trace(traceLabel + ": remote-url resolution failed: " + err.Error())
			return "", "", err
		}
		ctx.Trace(traceLabel + ": origin = " + remote)
	}
	owner, repo, err = parseGitHubOwnerRepo(remote)
	if err != nil {
		ctx.Trace(traceLabel + ": remote-url resolution failed: " + err.Error())
		return "", "", err
	}
	return owner, repo, nil
}

func originRemoteURL() (string, error) {
	stdout := new(strings.Builder)
	stderr := new(strings.Builder)
	if err := GitCommandRunner("", asWriter(stdout), asWriter(stderr), "remote", "get-url", "origin"); err != nil {
		return "", fmt.Errorf("remote-url is required: no --remote-url given and reading origin failed: %w%s",
			err, formatGitCommandStderr(stderr.String()))
	}
	remote := strings.TrimSpace(stdout.String())
	if remote == "" {
		return "", fmt.Errorf("remote-url is required: the current checkout has no origin remote")
	}
	return remote, nil
}

func trimmedNonEmpty(values []string) []string {
	trimmed := make([]string, 0, len(values))
	for _, value := range values {
		if cleaned := strings.TrimSpace(value); cleaned != "" {
			trimmed = append(trimmed, cleaned)
		}
	}
	return trimmed
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
	traceReconcileBypassFollowUpCalls(ctx, inputs)
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

// traceReconcileBypassFollowUpCalls names the conditional GitHub reads the
// reconciliation makes once it knows which pushes the tip commit alone did
// not account for, so a dry run shows every call a real run can make rather
// than only the two it always makes.
func traceReconcileBypassFollowUpCalls(ctx Context, inputs reconcileBypassInputs) {
	ctx.Trace(fmt.Sprintf("github: GET %srepos/%s/%s/rulesets/rule-suites/{ruleSuiteId} per bypassed suite, to confirm ruleset %d's own rule was the bypassed one",
		githubAPIBaseURL, inputs.owner, inputs.repo, inputs.rulesetID))
	ctx.Trace(fmt.Sprintf("github: GET %srepos/%s/%s/compare/{beforeSha}...{afterSha} per push no passed gate run's merge commit matched, to account for every commit the push added",
		githubAPIBaseURL, inputs.owner, inputs.repo))
	ctx.Trace(fmt.Sprintf("github: GET %srepos/%s/%s/tags?per_page=100 (once, up to %d pages) to tell a release's own push from an ungated one",
		githubAPIBaseURL, inputs.owner, inputs.repo, maxReleaseTagPages))
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
	result.Unreconciled, result.UnexpectedActors = accountForBypassedPushes(
		context.Background(), httpClient, token, inputs, pushes, passed)
	result.Pushes = pushes
	return result, nil
}

// githubRuleSuiteSummary is one entry from GitHub's rule-suites list.
type githubRuleSuiteSummary struct {
	ID        int64  `json:"id"`
	ActorName string `json:"actor_name"`
	BeforeSHA string `json:"before_sha"`
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
		var batch []githubRuleSuiteSummary
		link, err := getGitHubJSONPage(ctx, client, token, next, &batch)
		if err != nil {
			return nil, false, err
		}
		all = append(all, batch...)
		next = link
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
			BeforeCommit: suite.BeforeSHA, PushedAt: suite.PushedAt, BypassedRule: rule,
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

// listGitHubCompareCommits returns every commit a push added, oldest first --
// GitHub's own answer to "what did this push actually land", rather than an
// inference from the tip commit.
func listGitHubCompareCommits(ctx context.Context, client *http.Client, token, owner, repo, base, head string) ([]string, error) {
	if strings.TrimSpace(base) == "" || strings.TrimSpace(head) == "" {
		return nil, fmt.Errorf("the rule-suites ledger recorded no before/after pair for this push")
	}
	requestURL := fmt.Sprintf("%srepos/%s/%s/compare/%s...%s", githubAPIBaseURL, owner, repo, base, head)
	var payload struct {
		Commits []struct {
			SHA string `json:"sha"`
		} `json:"commits"`
	}
	if err := getGitHubJSON(ctx, client, token, requestURL, &payload); err != nil {
		return nil, err
	}
	commits := make([]string, 0, len(payload.Commits))
	for _, commit := range payload.Commits {
		commits = append(commits, commit.SHA)
	}
	return commits, nil
}

// listGitHubTagCommits maps each tag's own commit to the tag name, walking
// pagination up to maxReleaseTagPages.
func listGitHubTagCommits(ctx context.Context, client *http.Client, token, owner, repo string) (map[string]string, error) {
	tagByCommit := make(map[string]string)
	next := fmt.Sprintf("%srepos/%s/%s/tags?per_page=100", githubAPIBaseURL, owner, repo)
	for page := 0; next != "" && page < maxReleaseTagPages; page++ {
		var batch []struct {
			Name   string `json:"name"`
			Commit struct {
				SHA string `json:"sha"`
			} `json:"commit"`
		}
		link, err := getGitHubJSONPage(ctx, client, token, next, &batch)
		if err != nil {
			return nil, err
		}
		for _, tag := range batch {
			if tag.Commit.SHA != "" {
				tagByCommit[tag.Commit.SHA] = tag.Name
			}
		}
		next = link
	}
	return tagByCommit, nil
}

func getGitHubJSON(ctx context.Context, client *http.Client, token, requestURL string, into any) error {
	_, err := getGitHubJSONPage(ctx, client, token, requestURL, into)
	return err
}

// getGitHubJSONPage performs one authenticated GET, decodes it, and returns
// the rel="next" link so a paginated caller can keep walking.
func getGitHubJSONPage(ctx context.Context, client *http.Client, token, requestURL string, into any) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return "", fmt.Errorf("build github request for %s: %w", requestURL, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("get %s: %w", requestURL, err)
	}
	body, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if readErr != nil {
		return "", fmt.Errorf("read github response for %s: %w", requestURL, readErr)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("github returned %d for %s: %s", resp.StatusCode, requestURL, strings.TrimSpace(string(body)))
	}
	if err := json.Unmarshal(body, into); err != nil {
		return "", fmt.Errorf("decode github response for %s: %w", requestURL, err)
	}
	return githubNextPageLink(resp.Header.Get("Link")), nil
}

func getGitHubRuleSuiteDetail(ctx context.Context, client *http.Client, token, owner, repo string, id int64) (githubRuleSuiteDetail, error) {
	requestURL := fmt.Sprintf("%srepos/%s/%s/rulesets/rule-suites/%d", githubAPIBaseURL, owner, repo, id)
	var detail githubRuleSuiteDetail
	if err := getGitHubJSON(ctx, client, token, requestURL, &detail); err != nil {
		return githubRuleSuiteDetail{}, err
	}
	return detail, nil
}

// accountForBypassedPushes decides each push's verdict and returns how many
// went unaccounted for and how many an unnamed identity made.
//
// Two things a queue legitimately does are not one thing. A gated merge lands
// exactly the commit a PASSED gate run built, so its tip matches directly. A
// release lands three commits at once (the tagged release commit, the
// packaging-checksum sync, the next version's stamp) and its tip is the last
// of them, which no gate run ever built -- reconciling only the tip would
// report every release this repository has ever cut as unaccounted for, and a
// report that is permanently red is a report nobody reads.
func accountForBypassedPushes(ctx context.Context, client *http.Client, token string, inputs reconcileBypassInputs, pushes []BypassedPush, passed []PlatformGateRun) (unreconciled, unexpectedActors int) {
	byCommit := make(map[string]string, len(passed))
	for _, run := range passed {
		if run.MergeCommit != "" {
			byCommit[run.MergeCommit] = run.GateRunID
		}
	}
	tags := newReleaseTagIndex(ctx, client, token, inputs.owner, inputs.repo)
	for i := range pushes {
		push := &pushes[i]
		accountForOneBypassedPush(ctx, client, token, inputs, push, byCommit, tags)
		if !actorIsExpected(inputs.expectedActors, push.Actor) {
			unexpectedActors++
		}
		if push.GateRunID == "" && push.ReleaseTag == "" {
			unreconciled++
		}
	}
	return unreconciled, unexpectedActors
}

// accountForOneBypassedPush resolves one push's evidence, then lets an
// unexpected actor override the verdict: content that was really gated is
// still a finding when the wrong identity was able to push it at all.
func accountForOneBypassedPush(ctx context.Context, client *http.Client, token string, inputs reconcileBypassInputs, push *BypassedPush, gateRunsByCommit map[string]string, tags *releaseTagIndex) {
	push.Verdict, push.Reason = resolveBypassedPushEvidence(ctx, client, token, inputs, push, gateRunsByCommit, tags)
	if actorIsExpected(inputs.expectedActors, push.Actor) {
		return
	}
	push.Verdict = BypassVerdictUnexpectedActor
	push.Reason = fmt.Sprintf("%s is not one of the expected bypass identities (%s)",
		push.Actor, strings.Join(inputs.expectedActors, ", "))
	if push.GateRunID != "" {
		push.Reason += "; the content itself is covered by gate run " + push.GateRunID
	}
	if push.ReleaseTag != "" {
		push.Reason += "; the content itself was published by release tag " + push.ReleaseTag
	}
}

func resolveBypassedPushEvidence(ctx context.Context, client *http.Client, token string, inputs reconcileBypassInputs, push *BypassedPush, gateRunsByCommit map[string]string, tags *releaseTagIndex) (verdict, reason string) {
	if gateRunID, ok := gateRunsByCommit[push.Commit]; ok {
		push.GateRunID = gateRunID
		return BypassVerdictReconciled, ""
	}
	commits, err := listGitHubCompareCommits(ctx, client, token, inputs.owner, inputs.repo, push.BeforeCommit, push.Commit)
	if err != nil {
		return BypassVerdictUnreconciled, "could not read the commits this push added: " + err.Error()
	}
	for _, commit := range commits {
		if gateRunID, ok := gateRunsByCommit[commit]; ok {
			push.GateRunID = gateRunID
			return BypassVerdictReconciled, ""
		}
	}
	tag, err := tags.tagPointingAt(commits)
	if err != nil {
		return BypassVerdictUnreconciled, "could not read this repository's tags: " + err.Error()
	}
	if tag != "" {
		push.ReleaseTag = tag
		return BypassVerdictRelease, "published by release tag " + tag + ", not gated as a merge"
	}
	return BypassVerdictUnreconciled, fmt.Sprintf("no passed gate run's merge commit and no tag account for the %d commit(s) this push added", len(commits))
}

// actorIsExpected treats an empty expectation as "any actor": naming the
// identities is opt-in, so a caller that has not narrowed the bypass grant
// yet still gets the evidence half of the report.
func actorIsExpected(expected []string, actor string) bool {
	if len(expected) == 0 {
		return true
	}
	for _, candidate := range expected {
		if strings.EqualFold(candidate, actor) {
			return true
		}
	}
	return false
}

// maxReleaseTagPages bounds the tag walk. Truncation can only ever turn a
// release into an UNRECONCILED finding, never the reverse, so the bound fails
// loud rather than silently excusing a push.
const maxReleaseTagPages = 5

// releaseTagIndex reads the repository's tags at most once, and only when a
// push actually needs them -- the common case (a gated merge whose tip is the
// gate run's own merge commit) never fetches them at all.
type releaseTagIndex struct {
	ctx    context.Context
	client *http.Client
	token  string
	owner  string
	repo   string

	loaded      bool
	err         error
	tagByCommit map[string]string
}

func newReleaseTagIndex(ctx context.Context, client *http.Client, token, owner, repo string) *releaseTagIndex {
	return &releaseTagIndex{ctx: ctx, client: client, token: token, owner: owner, repo: repo}
}

func (index *releaseTagIndex) tagPointingAt(commits []string) (string, error) {
	if !index.loaded {
		index.tagByCommit, index.err = listGitHubTagCommits(index.ctx, index.client, index.token, index.owner, index.repo)
		index.loaded = true
	}
	if index.err != nil {
		return "", index.err
	}
	for _, commit := range commits {
		if tag, ok := index.tagByCommit[commit]; ok {
			return tag, nil
		}
	}
	return "", nil
}
