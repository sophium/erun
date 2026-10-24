package review

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/gitx"
	"github.com/sophium/erun/erun-common/infra/platformclient"
	"github.com/sophium/erun/erun-common/infra/registry"
)

// Bypassed-push verdicts.
const (
	BypassVerdictReconciled = "RECONCILED"
	// BypassVerdictRelease means a tag points at a pushed commit, so a release,
	// not a gated merge, landed it.
	BypassVerdictRelease = "RELEASE"
	// BypassVerdictUnexpectedActor overrides the evidence verdict.
	BypassVerdictUnexpectedActor = "UNEXPECTED_ACTOR"
	BypassVerdictUnreconciled    = "UNRECONCILED"
)

// ReconcileBypassDependencies are the GitHub HTTP and gh token seams.
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

// maxRuleSuitePages caps pagination; a capped run reports it stopped early.
const maxRuleSuitePages = 20

type reconcileBypassInputs struct {
	owner, repo, targetBranch, since string
	rulesetID                        int64
	expectedActors                   []string
}

func resolveReconcileBypassInputs(ctx core.Context, params model.ReconcileBypassParams) (reconcileBypassInputs, error) {
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
		ctx.Trace("reconcile-bypass: no expected bypass actors named; reporting on build and release evidence alone")
	}
	return reconcileBypassInputs{
		owner: owner, repo: repo, targetBranch: target,
		rulesetID: params.RulesetID, since: strings.TrimSpace(params.Since),
		expectedActors: expected,
	}, nil
}

func resolveGitHubRepoFromRemoteOrOrigin(ctx core.Context, remoteURL, traceLabel string) (owner, repo string, err error) {
	remote := strings.TrimSpace(remoteURL)
	if remote == "" {
		ctx.Trace(traceLabel + ": no --remote-url given; reading origin from the current checkout")
		remote, err = OriginRemoteURL()
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

func trimmedNonEmpty(values []string) []string {
	trimmed := make([]string, 0, len(values))
	for _, value := range values {
		if cleaned := strings.TrimSpace(value); cleaned != "" {
			trimmed = append(trimmed, cleaned)
		}
	}
	return trimmed
}

// ReconcileBypass reports every push that bypassed the ruleset and whether a
// successful build of a commit it landed covers it.
func ReconcileBypass(ctx core.Context, store config.CloudReadStore, alias string, params model.ReconcileBypassParams, cloudDeps cloud.CloudDependencies, deps ReconcileBypassDependencies) (model.ReconcileBypassResult, error) {
	inputs, err := resolveReconcileBypassInputs(ctx, params)
	if err != nil {
		return model.ReconcileBypassResult{}, err
	}
	deps = normalizeReconcileBypassDependencies(deps)
	result := model.ReconcileBypassResult{Owner: inputs.owner, Repo: inputs.repo, RulesetID: inputs.rulesetID, TargetBranch: inputs.targetBranch}

	listURL := githubRuleSuitesURL(inputs.owner, inputs.repo, inputs.targetBranch, inputs.since)
	ctx.Trace("github: GET " + listURL)

	client, provider, err := cloud.NewPlatformClientForAlias(ctx, store, alias, cloudDeps)
	if err != nil {
		return model.ReconcileBypassResult{}, err
	}
	traceReconcileBypassFollowUpCalls(ctx, provider, inputs)
	if ctx.DryRun {
		return result, nil
	}

	ctx.Trace("reconcile-bypass: resolving a github token")
	token, ok := deps.ResolveToken(inputs.owner)
	if !ok {
		ctx.Trace("reconcile-bypass: token resolution failed: no gh CLI session or GITHUB_TOKEN/GH_TOKEN set")
		return model.ReconcileBypassResult{}, fmt.Errorf(
			"no GitHub token available to read rule suites; run 'gh auth login' or set GITHUB_TOKEN")
	}
	return finishReconcileBypass(ctx, client, deps.Client, token, listURL, inputs, result)
}

// traceReconcileBypassFollowUpCalls makes a dry run show the conditional reads
// too.
func traceReconcileBypassFollowUpCalls(ctx core.Context, provider model.CloudProviderConfig, inputs reconcileBypassInputs) {
	ctx.Trace(fmt.Sprintf("github: GET %srepos/%s/%s/rulesets/rule-suites/{ruleSuiteId} per bypassed suite, to confirm ruleset %d's own rule was the bypassed one",
		registry.GithubAPIBaseURL, inputs.owner, inputs.repo, inputs.rulesetID))
	cloud.TracePlatformCall(ctx, provider, "GET", "/v1/builds", "commit={sha}", "successful=true", "per landed commit, tip first")
	ctx.Trace(fmt.Sprintf("github: GET %srepos/%s/%s/compare/{beforeSha}...{afterSha} per push whose tip has no successful build, to account for every commit the push added",
		registry.GithubAPIBaseURL, inputs.owner, inputs.repo))
	ctx.Trace(fmt.Sprintf("github: GET %srepos/%s/%s/tags?per_page=100 (once, up to %d pages) to tell a release's own push from an ungated one",
		registry.GithubAPIBaseURL, inputs.owner, inputs.repo, maxReleaseTagPages))
}

func finishReconcileBypass(ctx core.Context, client *platformclient.PlatformClient, httpClient *http.Client, token, listURL string, inputs reconcileBypassInputs, result model.ReconcileBypassResult) (model.ReconcileBypassResult, error) {
	suites, truncated, err := listBypassedRuleSuites(context.Background(), httpClient, token, listURL)
	if err != nil {
		return model.ReconcileBypassResult{}, err
	}
	if truncated {
		ctx.Info(fmt.Sprintf("reconcile-bypass: stopped after %d pages of rule suites; narrow --since to see the rest", maxRuleSuitePages))
	}
	pushes, err := resolveBypassedPushesForRuleset(context.Background(), httpClient, token, inputs.owner, inputs.repo, inputs.rulesetID, suites)
	if err != nil {
		return model.ReconcileBypassResult{}, err
	}

	builds := newSuccessfulBuildIndex(context.Background(), client)
	result.Unreconciled, result.UnexpectedActors, err = accountForBypassedPushes(
		context.Background(), httpClient, token, inputs, pushes, builds)
	if err != nil {
		return model.ReconcileBypassResult{}, err
	}
	result.Pushes = pushes
	return result, nil
}

// A release lands three commits and its tip was never built, so checking only
// tips would flag every release.
func accountForBypassedPushes(ctx context.Context, client *http.Client, token string, inputs reconcileBypassInputs, pushes []model.BypassedPush, builds *successfulBuildIndex) (unreconciled, unexpectedActors int, err error) {
	tags := newReleaseTagIndex(ctx, client, token, inputs.owner, inputs.repo)
	for i := range pushes {
		push := &pushes[i]
		if err := accountForOneBypassedPush(ctx, client, token, inputs, push, builds, tags); err != nil {
			return 0, 0, err
		}
		if !actorIsExpected(inputs.expectedActors, push.Actor) {
			unexpectedActors++
		}
		if push.BuildID == "" && push.ReleaseTag == "" {
			unreconciled++
		}
	}
	return unreconciled, unexpectedActors, nil
}

// An unexpected actor overrides even a built verdict.
func accountForOneBypassedPush(ctx context.Context, client *http.Client, token string, inputs reconcileBypassInputs, push *model.BypassedPush, builds *successfulBuildIndex, tags *releaseTagIndex) error {
	verdict, reason, err := resolveBypassedPushEvidence(ctx, client, token, inputs, push, builds, tags)
	if err != nil {
		return err
	}
	push.Verdict, push.Reason = verdict, reason
	if actorIsExpected(inputs.expectedActors, push.Actor) {
		return nil
	}
	push.Verdict = BypassVerdictUnexpectedActor
	push.Reason = fmt.Sprintf("%s is not one of the expected bypass identities (%s)",
		push.Actor, strings.Join(inputs.expectedActors, ", "))
	if push.BuildID != "" {
		push.Reason += "; the content itself is covered by build " + push.BuildID
	}
	if push.ReleaseTag != "" {
		push.Reason += "; the content itself was published by release tag " + push.ReleaseTag
	}
	return nil
}

// resolveBypassedPushEvidence errors only on an unreadable build history,
// which must not be blamed on every push.
func resolveBypassedPushEvidence(ctx context.Context, client *http.Client, token string, inputs reconcileBypassInputs, push *model.BypassedPush, builds *successfulBuildIndex, tags *releaseTagIndex) (verdict, reason string, err error) {
	buildID, err := builds.buildOf(push.Commit)
	if err != nil {
		return "", "", err
	}
	if buildID != "" {
		push.BuildID = buildID
		return BypassVerdictReconciled, "", nil
	}
	commits, err := listGitHubCompareCommits(ctx, client, token, inputs.owner, inputs.repo, push.BeforeCommit, push.Commit)
	if err != nil {
		return unreconciledBypassedPush("could not read the commits this push added: ", err)
	}
	for _, commit := range commits {
		buildID, err := builds.buildOf(commit)
		if err != nil {
			return "", "", err
		}
		if buildID != "" {
			push.BuildID = buildID
			return BypassVerdictReconciled, "", nil
		}
	}
	tag, err := tags.tagPointingAt(commits)
	if err != nil {
		return unreconciledBypassedPush("could not read this repository's tags: ", err)
	}
	if tag != "" {
		push.ReleaseTag = tag
		return BypassVerdictRelease, "published by release tag " + tag + ", not built as a merge", nil
	}
	return BypassVerdictUnreconciled, fmt.Sprintf("no successful build and no tag account for the %d commit(s) this push added", len(commits)), nil
}

func unreconciledBypassedPush(reasonPrefix string, cause error) (verdict, reason string, err error) {
	return BypassVerdictUnreconciled, reasonPrefix + cause.Error(), nil
}

// successfulBuildIndex asks the platform once per commit: one push's tip is
// often the next push's before-commit.
type successfulBuildIndex struct {
	ctx      context.Context
	client   *platformclient.PlatformClient
	byCommit map[string]string
}

func newSuccessfulBuildIndex(ctx context.Context, client *platformclient.PlatformClient) *successfulBuildIndex {
	return &successfulBuildIndex{ctx: ctx, client: client, byCommit: map[string]string{}}
}

func (index *successfulBuildIndex) buildOf(commit string) (string, error) {
	commit = strings.TrimSpace(commit)
	if commit == "" {
		return "", nil
	}
	if buildID, ok := index.byCommit[commit]; ok {
		return buildID, nil
	}
	successful := true
	page, err := index.client.ListAllBuilds(index.ctx, platformclient.PlatformBuildListFilter{CommitID: commit, Successful: &successful, Limit: 1})
	if err != nil {
		return "", fmt.Errorf("list successful builds of %s: %w", commit, err)
	}
	buildID := ""
	for _, build := range page.Builds {
		if build.Successful && build.CommitID == commit {
			buildID = build.BuildID
			break
		}
	}
	index.byCommit[commit] = buildID
	return buildID, nil
}

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

// maxReleaseTagPages bounds the tag walk; truncation can only turn a release
// into UNRECONCILED, never excuse a push.
const maxReleaseTagPages = 5

// releaseTagIndex fetches tags lazily, once.
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

func OriginRemoteURL() (string, error) {
	stdout := new(strings.Builder)
	stderr := new(strings.Builder)
	if err := gitx.GitCommandRunner("", AsWriter(stdout), AsWriter(stderr), "remote", "get-url", "origin"); err != nil {
		return "", fmt.Errorf("remote-url is required: no --remote-url given and reading origin failed: %w%s",
			err, core.FormatGitCommandStderr(stderr.String()))
	}
	remote := strings.TrimSpace(stdout.String())
	if remote == "" {
		return "", fmt.Errorf("remote-url is required: the current checkout has no origin remote")
	}
	return remote, nil
}
