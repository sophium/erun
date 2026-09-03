package eruncommon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ruleset_bypass_plan.go is what has to exist before a repository's ruleset
// bypass grant can safely be narrowed to one non-human identity (see
// erun-backend-api/AGENTS.md "GitHub branch protection cannot tell the
// queue's push from a bypass"). The narrowing itself is a repository-settings
// edit an operator performs; what a tool can do -- and what nobody should
// attempt a governance change without -- is resolve the exact edit from the
// ruleset as it actually is, prove the new identity can already push before
// anything is taken away, and hand back the payload that puts it all back.
//
// The order is the whole point. Granting the queue identity bypass alongside
// today's actors leaves both paths open, so the queue can be proven under the
// new identity while the old one still works; only then is the broad grant
// demoted. Reversing those two steps is what leaves a repository unmergeable
// with no way in.
//
// This command never writes to GitHub. A ruleset edit governs every
// contributor's merges, it happens once, and erun has no other
// repository-settings mutation surface to verify such a path against -- so the
// payloads are emitted for a human to apply and read back, rather than
// applied by a code path no gate here could exercise end to end.

// Bypass modes a ruleset actor can hold.
const (
	// RulesetBypassAlways lets the actor push directly, skipping every rule
	// in the ruleset. The queue's own push needs this and nothing less: it
	// pushes a raw commit, so no pull-request-scoped bypass can cover it.
	RulesetBypassAlways = "always"
	// RulesetBypassPullRequest keeps an emergency lever for a human without
	// leaving direct pushes open -- they must open a pull request, which is
	// what makes the exception auditable.
	RulesetBypassPullRequest = "pull_request"
	// RulesetBypassExempt skips enforcement without recording a bypass.
	// Reconciliation reads GitHub's own bypass ledger, so an exempt actor is
	// invisible to it: the push simply never appears. Never plan one.
	RulesetBypassExempt = "exempt"
)

// PlanRulesetBypassParams is the `erun exec plan-ruleset-bypass` input.
type PlanRulesetBypassParams struct {
	// RemoteURL is the github.com remote the ruleset lives on. Empty reads
	// origin from the current checkout.
	RemoteURL string
	// RulesetID is the ruleset whose bypass list is being narrowed.
	RulesetID int64
	// TargetBranch, when set, is checked against the ruleset's own
	// conditions: planning a narrowing for a branch this ruleset does not
	// govern would produce an edit that protects nothing.
	TargetBranch string
	// QueueActorType is the GitHub actor type the queue identity is:
	// User (a dedicated machine account), Integration (a GitHub App),
	// Team, RepositoryRole, or DeployKey.
	QueueActorType string
	// QueueActor names the identity: a login for User, otherwise the
	// numeric actor id GitHub uses for that type.
	QueueActor string
	// OutDir is where the three payload files are written.
	OutDir string
}

// RulesetBypassActor is one entry of a ruleset's bypass list.
type RulesetBypassActor struct {
	ActorID    *int64 `json:"actor_id"`
	ActorType  string `json:"actor_type"`
	BypassMode string `json:"bypass_mode"`
}

// PlanRulesetBypassResult is the full plan: what the ruleset carries now, the
// identity that would become its only always-bypass actor, and the files each
// step applies.
type PlanRulesetBypassResult struct {
	Owner        string `json:"owner"`
	Repo         string `json:"repo"`
	RulesetID    int64  `json:"rulesetId"`
	RulesetName  string `json:"rulesetName"`
	TargetBranch string `json:"targetBranch,omitempty"`
	// CurrentBypassActors is the live bypass list the plan was computed
	// from, so a reader can tell a stale plan from a current one.
	CurrentBypassActors []RulesetBypassActor `json:"currentBypassActors"`
	// CallerCanBypass is GitHub's own answer for the token that read the
	// ruleset: always, pull_requests_only, never, or exempt. This is the
	// per-identity check the plan's verification steps rest on.
	CallerCanBypass string `json:"callerCanBypass,omitempty"`
	QueueActorType  string `json:"queueActorType"`
	QueueActor      string `json:"queueActor"`
	QueueActorID    int64  `json:"queueActorId"`
	// QueueActorPushAccess is the queue identity's repository permission,
	// read from GitHub. An identity that cannot push cannot be the one the
	// bypass grant narrows to, whatever the ruleset says.
	QueueActorPushAccess string `json:"queueActorPushAccess,omitempty"`
	// Stage1File grants the queue identity bypass alongside today's actors.
	Stage1File string `json:"stage1File"`
	// Stage2File demotes every other always-bypass actor to pull_request.
	Stage2File string `json:"stage2File"`
	// RollbackFile restores the bypass list exactly as it is today.
	RollbackFile string `json:"rollbackFile"`
}

// PlanRulesetBypassDependencies lets tests replace the GitHub HTTP call and
// gh token resolution without a real network or a real gh CLI.
type PlanRulesetBypassDependencies struct {
	Client       *http.Client
	ResolveToken func(owner string) (string, bool)
}

func normalizePlanRulesetBypassDependencies(deps PlanRulesetBypassDependencies) PlanRulesetBypassDependencies {
	if deps.Client == nil {
		deps.Client = &http.Client{Timeout: 15 * time.Second}
	}
	if deps.ResolveToken == nil {
		deps.ResolveToken = resolveGitHubAPIToken
	}
	return deps
}

// planRulesetBypassInputs is the validated, trimmed input.
type planRulesetBypassInputs struct {
	owner, repo, targetBranch, actorType, actor, outDir string
	rulesetID                                           int64
}

// rulesetBypassActorTypes are the actor types GitHub accepts on a repository
// ruleset's bypass list.
var rulesetBypassActorTypes = []string{"User", "Integration", "Team", "RepositoryRole", "OrganizationAdmin", "DeployKey"}

func resolvePlanRulesetBypassInputs(ctx Context, params PlanRulesetBypassParams) (planRulesetBypassInputs, error) {
	ctx.Trace("plan-ruleset-bypass: resolving inputs")
	if params.RulesetID <= 0 {
		ctx.Trace("plan-ruleset-bypass: input resolution failed: ruleset id is required")
		return planRulesetBypassInputs{}, fmt.Errorf("ruleset id is required")
	}
	requestedType := strings.TrimSpace(params.QueueActorType)
	if requestedType == "" {
		requestedType = "User"
	}
	actorType, ok := canonicalRulesetBypassActorType(requestedType)
	if !ok {
		ctx.Trace("plan-ruleset-bypass: input resolution failed: unsupported queue actor type " + requestedType)
		return planRulesetBypassInputs{}, fmt.Errorf("queue actor type %q is not one of %s",
			requestedType, strings.Join(rulesetBypassActorTypes, ", "))
	}
	actor := strings.TrimSpace(params.QueueActor)
	if actor == "" {
		ctx.Trace("plan-ruleset-bypass: input resolution failed: queue actor is required")
		return planRulesetBypassInputs{}, fmt.Errorf(
			"queue actor is required: the identity that becomes the ruleset's only always-bypass actor")
	}
	owner, repo, err := resolveGitHubRepoFromRemoteOrOrigin(ctx, params.RemoteURL, "plan-ruleset-bypass")
	if err != nil {
		return planRulesetBypassInputs{}, err
	}
	outDir := strings.TrimSpace(params.OutDir)
	if outDir == "" {
		outDir = "."
	}
	ctx.Trace(fmt.Sprintf("plan-ruleset-bypass: owner = %s, repo = %s, rulesetId = %d, queue actor = %s %s",
		owner, repo, params.RulesetID, actorType, actor))
	return planRulesetBypassInputs{
		owner: owner, repo: repo, targetBranch: strings.TrimSpace(params.TargetBranch),
		actorType: actorType, actor: actor, outDir: outDir, rulesetID: params.RulesetID,
	}, nil
}

// PlanRulesetBypass resolves the exact two-stage ruleset edit that makes one
// non-human identity the only actor holding an always bypass, verifies the
// preconditions that edit depends on, and writes the payload each stage (and
// the rollback) applies.
func PlanRulesetBypass(ctx Context, params PlanRulesetBypassParams, deps PlanRulesetBypassDependencies) (PlanRulesetBypassResult, error) {
	inputs, err := resolvePlanRulesetBypassInputs(ctx, params)
	if err != nil {
		return PlanRulesetBypassResult{}, err
	}
	deps = normalizePlanRulesetBypassDependencies(deps)
	result := PlanRulesetBypassResult{
		Owner: inputs.owner, Repo: inputs.repo, RulesetID: inputs.rulesetID,
		TargetBranch: inputs.targetBranch, QueueActorType: inputs.actorType, QueueActor: inputs.actor,
	}
	result.Stage1File = rulesetPlanFilePath(inputs, "stage1")
	result.Stage2File = rulesetPlanFilePath(inputs, "stage2")
	result.RollbackFile = rulesetPlanFilePath(inputs, "rollback")

	ctx.Trace(fmt.Sprintf("github: GET %srepos/%s/%s/rulesets/%d", githubAPIBaseURL, inputs.owner, inputs.repo, inputs.rulesetID))
	if inputs.actorType == "User" {
		ctx.Trace(fmt.Sprintf("github: GET %susers/%s to resolve the queue identity's actor id", githubAPIBaseURL, inputs.actor))
	}
	ctx.Trace(fmt.Sprintf("github: GET %srepos/%s/%s/collaborators/{queueActor}/permission to prove the queue identity can already push",
		githubAPIBaseURL, inputs.owner, inputs.repo))
	ctx.TraceCommand("", "write-json", result.RollbackFile)
	ctx.TraceCommand("", "write-json", result.Stage1File)
	ctx.TraceCommand("", "write-json", result.Stage2File)
	if ctx.DryRun {
		return result, nil
	}

	ctx.Trace("plan-ruleset-bypass: resolving a github token")
	token, ok := deps.ResolveToken(inputs.owner)
	if !ok {
		ctx.Trace("plan-ruleset-bypass: token resolution failed: no gh CLI session or GITHUB_TOKEN/GH_TOKEN set")
		return PlanRulesetBypassResult{}, fmt.Errorf(
			"no GitHub token available to read the ruleset; run 'gh auth login' or set GITHUB_TOKEN")
	}
	return finishPlanRulesetBypass(ctx, deps.Client, token, inputs, result)
}

// finishPlanRulesetBypass runs the networked half, isolated so the validation
// and dry-run branching above it stay readable -- the same split
// ReconcileBypass and ClosePullRequest use.
func finishPlanRulesetBypass(ctx Context, client *http.Client, token string, inputs planRulesetBypassInputs, result PlanRulesetBypassResult) (PlanRulesetBypassResult, error) {
	ruleset, err := getGitHubRuleset(context.Background(), client, token, inputs.owner, inputs.repo, inputs.rulesetID)
	if err != nil {
		return PlanRulesetBypassResult{}, err
	}
	result.RulesetName = ruleset.Name
	result.CurrentBypassActors = ruleset.BypassActors
	result.CallerCanBypass = ruleset.CurrentUserCanBypass
	if ruleset.BypassActors == nil {
		return PlanRulesetBypassResult{}, fmt.Errorf(
			"github did not return ruleset %d's bypass actors: it only returns them to a token with write access to the ruleset, so plan this with an admin token",
			inputs.rulesetID)
	}
	if err := checkRulesetGovernsBranch(ctx, ruleset, inputs.targetBranch); err != nil {
		return PlanRulesetBypassResult{}, err
	}

	actorID, err := resolveQueueActorID(ctx, client, token, inputs)
	if err != nil {
		return PlanRulesetBypassResult{}, err
	}
	result.QueueActorID = actorID
	access, err := resolveQueueActorPushAccess(ctx, client, token, inputs)
	if err != nil {
		return PlanRulesetBypassResult{}, err
	}
	result.QueueActorPushAccess = access

	stage1 := rulesetWithQueueActorGranted(ruleset, actorID, inputs.actorType)
	stage2 := rulesetWithOtherActorsDemoted(stage1, actorID, inputs.actorType)
	for _, write := range []struct {
		path    string
		payload githubRulesetUpdate
	}{
		{result.RollbackFile, rulesetUpdatePayload(ruleset, ruleset.BypassActors)},
		{result.Stage1File, stage1},
		{result.Stage2File, stage2},
	} {
		if err := writeRulesetPlanFile(ctx, write.path, write.payload); err != nil {
			return PlanRulesetBypassResult{}, err
		}
	}
	return result, nil
}

// githubRuleset is the shape of a repository ruleset this plan reads and
// rewrites. rules and conditions stay raw: the plan changes the bypass list
// and must hand every other part of the ruleset back to GitHub byte for byte,
// since the update endpoint replaces rather than merges.
type githubRuleset struct {
	ID                   int64                `json:"id"`
	Name                 string               `json:"name"`
	Target               string               `json:"target"`
	Enforcement          string               `json:"enforcement"`
	Conditions           json.RawMessage      `json:"conditions"`
	Rules                json.RawMessage      `json:"rules"`
	BypassActors         []RulesetBypassActor `json:"bypass_actors"`
	CurrentUserCanBypass string               `json:"current_user_can_bypass"`
}

// githubRulesetUpdate is the PUT body for a ruleset update.
type githubRulesetUpdate struct {
	Name         string               `json:"name"`
	Target       string               `json:"target"`
	Enforcement  string               `json:"enforcement"`
	Conditions   json.RawMessage      `json:"conditions,omitempty"`
	Rules        json.RawMessage      `json:"rules,omitempty"`
	BypassActors []RulesetBypassActor `json:"bypass_actors"`
}

func getGitHubRuleset(ctx context.Context, client *http.Client, token, owner, repo string, id int64) (githubRuleset, error) {
	requestURL := fmt.Sprintf("%srepos/%s/%s/rulesets/%d", githubAPIBaseURL, owner, repo, id)
	var ruleset githubRuleset
	if err := getGitHubJSON(ctx, client, token, requestURL, &ruleset); err != nil {
		return githubRuleset{}, err
	}
	return ruleset, nil
}

// checkRulesetGovernsBranch refuses a plan whose named branch this ruleset
// does not actually cover: the edit would be applied to the wrong ruleset and
// the branch it was meant to protect would stay exactly as open as before.
func checkRulesetGovernsBranch(ctx Context, ruleset githubRuleset, branch string) error {
	if branch == "" {
		return nil
	}
	var conditions struct {
		RefName struct {
			Include []string `json:"include"`
		} `json:"ref_name"`
	}
	if len(ruleset.Conditions) > 0 {
		if err := json.Unmarshal(ruleset.Conditions, &conditions); err != nil {
			return fmt.Errorf("decode ruleset %d conditions: %w", ruleset.ID, err)
		}
	}
	for _, include := range conditions.RefName.Include {
		if include == "refs/heads/"+branch || include == "~ALL" || include == "~DEFAULT_BRANCH" {
			ctx.Trace("plan-ruleset-bypass: ruleset governs " + branch + " (" + include + ")")
			return nil
		}
	}
	return fmt.Errorf("ruleset %d does not govern %s (it includes %s); planning a bypass narrowing here would protect nothing",
		ruleset.ID, branch, strings.Join(conditions.RefName.Include, ", "))
}

// resolveQueueActorID turns the named identity into the numeric actor id the
// bypass list holds. A login is resolved through GitHub rather than trusted,
// so a typo cannot become a grant to an account nobody meant.
func resolveQueueActorID(ctx Context, client *http.Client, token string, inputs planRulesetBypassInputs) (int64, error) {
	if inputs.actorType == "DeployKey" || inputs.actorType == "OrganizationAdmin" {
		ctx.Trace("plan-ruleset-bypass: " + inputs.actorType + " carries no actor id")
		return 0, nil
	}
	if inputs.actorType != "User" {
		id, err := strconv.ParseInt(inputs.actor, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("queue actor %q must be the numeric actor id for actor type %s",
				inputs.actor, inputs.actorType)
		}
		return id, nil
	}
	var user struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
		Type  string `json:"type"`
	}
	if err := getGitHubJSON(context.Background(), client, token, githubAPIBaseURL+"users/"+inputs.actor, &user); err != nil {
		return 0, fmt.Errorf("resolve queue identity %q on github: %w", inputs.actor, err)
	}
	ctx.Trace(fmt.Sprintf("plan-ruleset-bypass: queue identity %s is user id %d (%s)", user.Login, user.ID, user.Type))
	return user.ID, nil
}

// resolveQueueActorPushAccess is the precondition that makes the ordering
// safe: an identity that cannot push to the repository at all would be handed
// the only bypass grant, and the next gated merge would have nowhere to go.
func resolveQueueActorPushAccess(ctx Context, client *http.Client, token string, inputs planRulesetBypassInputs) (string, error) {
	if inputs.actorType != "User" {
		ctx.Trace("plan-ruleset-bypass: push access for a " + inputs.actorType +
			" actor is not a collaborator permission; verify it with a real `git push --dry-run` as that identity before stage 2")
		return "", nil
	}
	var permission struct {
		Permission string `json:"permission"`
	}
	requestURL := fmt.Sprintf("%srepos/%s/%s/collaborators/%s/permission", githubAPIBaseURL, inputs.owner, inputs.repo, inputs.actor)
	if err := getGitHubJSON(context.Background(), client, token, requestURL, &permission); err != nil {
		return "", fmt.Errorf("read %s's permission on %s/%s: %w", inputs.actor, inputs.owner, inputs.repo, err)
	}
	switch permission.Permission {
	case "write", "admin", "maintain":
		ctx.Trace("plan-ruleset-bypass: queue identity has " + permission.Permission + " access")
		return permission.Permission, nil
	default:
		return "", fmt.Errorf(
			"queue identity %s has %q access to %s/%s and cannot push: grant it write access before narrowing the bypass to it",
			inputs.actor, permission.Permission, inputs.owner, inputs.repo)
	}
}

// rulesetWithQueueActorGranted is stage 1: the queue identity gains an always
// bypass and nothing is taken away, so both the new and the current path work
// while the queue is proven under the new identity.
func rulesetWithQueueActorGranted(ruleset githubRuleset, actorID int64, actorType string) githubRulesetUpdate {
	actors := make([]RulesetBypassActor, 0, len(ruleset.BypassActors)+1)
	actors = append(actors, ruleset.BypassActors...)
	if !hasBypassActor(actors, actorID, actorType) {
		actors = append(actors, newRulesetBypassActor(actorID, actorType, RulesetBypassAlways))
	}
	return rulesetUpdatePayload(ruleset, actors)
}

// rulesetWithOtherActorsDemoted is stage 2: every other always-bypass actor
// keeps a pull-request-scoped lever and loses direct-push bypass, leaving the
// queue identity as the only actor that can push straight to the branch. An
// exempt actor is demoted too -- it is invisible to the bypass ledger, so
// leaving one in place would keep a path reconciliation can never see.
func rulesetWithOtherActorsDemoted(stage1 githubRulesetUpdate, actorID int64, actorType string) githubRulesetUpdate {
	actors := make([]RulesetBypassActor, 0, len(stage1.BypassActors))
	for _, actor := range stage1.BypassActors {
		if sameBypassActor(actor, actorID, actorType) {
			actors = append(actors, actor)
			continue
		}
		if actor.BypassMode == RulesetBypassAlways || actor.BypassMode == RulesetBypassExempt {
			actor.BypassMode = RulesetBypassPullRequest
		}
		actors = append(actors, actor)
	}
	stage1.BypassActors = actors
	return stage1
}

func rulesetUpdatePayload(ruleset githubRuleset, actors []RulesetBypassActor) githubRulesetUpdate {
	return githubRulesetUpdate{
		Name: ruleset.Name, Target: ruleset.Target, Enforcement: ruleset.Enforcement,
		Conditions: ruleset.Conditions, Rules: ruleset.Rules, BypassActors: actors,
	}
}

func newRulesetBypassActor(actorID int64, actorType, mode string) RulesetBypassActor {
	actor := RulesetBypassActor{ActorType: actorType, BypassMode: mode}
	if actorID > 0 {
		id := actorID
		actor.ActorID = &id
	}
	return actor
}

func hasBypassActor(actors []RulesetBypassActor, actorID int64, actorType string) bool {
	for _, actor := range actors {
		if sameBypassActor(actor, actorID, actorType) {
			return true
		}
	}
	return false
}

func sameBypassActor(actor RulesetBypassActor, actorID int64, actorType string) bool {
	if !strings.EqualFold(actor.ActorType, actorType) {
		return false
	}
	if actor.ActorID == nil {
		return actorID == 0
	}
	return *actor.ActorID == actorID
}

func rulesetPlanFilePath(inputs planRulesetBypassInputs, stage string) string {
	return filepath.Join(inputs.outDir, fmt.Sprintf("ruleset-%d-%s.json", inputs.rulesetID, stage))
}

func writeRulesetPlanFile(ctx Context, path string, payload githubRulesetUpdate) error {
	body, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("encode ruleset payload for %s: %w", path, err)
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}
	if err := os.WriteFile(path, append(body, '\n'), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	ctx.Trace("plan-ruleset-bypass: wrote " + path)
	return nil
}

// canonicalRulesetBypassActorType maps a caller's spelling onto the exact
// actor_type string GitHub's API expects, so a plan is never emitted with a
// value the update endpoint would reject.
func canonicalRulesetBypassActorType(requested string) (string, bool) {
	for _, actorType := range rulesetBypassActorTypes {
		if strings.EqualFold(actorType, requested) {
			return actorType, true
		}
	}
	return "", false
}
