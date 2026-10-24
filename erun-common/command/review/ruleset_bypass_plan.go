package review

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

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/registry"
)

// Narrowing the ruleset bypass grant to one queue identity
// (erun-backend-api/AGENTS.md). Stage 1 grants alongside today's actors before
// stage 2 demotes them; reversed, the repository becomes unmergeable. Payloads
// are emitted for a human to apply, never written to GitHub.

// Bypass modes a ruleset actor can hold.
const (
	// RulesetBypassAlways is what the queue needs: it pushes raw commits, which no
	// pull-request-scoped bypass covers.
	RulesetBypassAlways = "always"
	// RulesetBypassPullRequest keeps an auditable emergency lever for humans.
	RulesetBypassPullRequest = "pull_request"
	// RulesetBypassExempt is invisible to the bypass ledger; never plan one.
	RulesetBypassExempt = "exempt"
)

// PlanRulesetBypassParams is the `erun exec plan-ruleset-bypass` input.
type PlanRulesetBypassParams struct {
	// RemoteURL defaults to the checkout's origin.
	RemoteURL string
	RulesetID int64
	// TargetBranch, when set, must be governed by the ruleset.
	TargetBranch string
	// QueueActorType is User, Integration, Team, RepositoryRole, or DeployKey.
	QueueActorType string
	// QueueActor is a login for User, otherwise GitHub's numeric actor id.
	QueueActor string
	OutDir     string
}

// RulesetBypassActor is one entry of a ruleset's bypass list.
type RulesetBypassActor struct {
	ActorID    *int64 `json:"actor_id"`
	ActorType  string `json:"actor_type"`
	BypassMode string `json:"bypass_mode"`
}

// PlanRulesetBypassResult is the plan and the payload files for each stage.
type PlanRulesetBypassResult struct {
	Owner        string `json:"owner"`
	Repo         string `json:"repo"`
	RulesetID    int64  `json:"rulesetId"`
	RulesetName  string `json:"rulesetName"`
	TargetBranch string `json:"targetBranch,omitempty"`
	// CurrentBypassActors lets a reader tell a stale plan from a current one.
	CurrentBypassActors []RulesetBypassActor `json:"currentBypassActors"`
	// CallerCanBypass is GitHub's answer for the reading token.
	CallerCanBypass      string `json:"callerCanBypass,omitempty"`
	QueueActorType       string `json:"queueActorType"`
	QueueActor           string `json:"queueActor"`
	QueueActorID         int64  `json:"queueActorId"`
	QueueActorPushAccess string `json:"queueActorPushAccess,omitempty"`
	// Stage1File grants the queue identity bypass alongside today's actors.
	Stage1File string `json:"stage1File"`
	// Stage2File demotes every other always-bypass actor to pull_request.
	Stage2File string `json:"stage2File"`
	// RollbackFile restores the bypass list exactly as it is today.
	RollbackFile string `json:"rollbackFile"`
}

// PlanRulesetBypassDependencies are the GitHub HTTP and gh token seams.
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

type planRulesetBypassInputs struct {
	owner, repo, targetBranch, actorType, actor, outDir string
	rulesetID                                           int64
}

var rulesetBypassActorTypes = []string{"User", "Integration", "Team", "RepositoryRole", "OrganizationAdmin", "DeployKey"}

func resolvePlanRulesetBypassInputs(ctx core.Context, params PlanRulesetBypassParams) (planRulesetBypassInputs, error) {
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

// PlanRulesetBypass verifies the preconditions and writes the two-stage edit
// and rollback that leave one non-human identity as the only always-bypass actor.
func PlanRulesetBypass(ctx core.Context, params PlanRulesetBypassParams, deps PlanRulesetBypassDependencies) (PlanRulesetBypassResult, error) {
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

	ctx.Trace(fmt.Sprintf("github: GET %srepos/%s/%s/rulesets/%d", registry.GithubAPIBaseURL, inputs.owner, inputs.repo, inputs.rulesetID))
	if inputs.actorType == "User" {
		ctx.Trace(fmt.Sprintf("github: GET %susers/%s to resolve the queue identity's actor id", registry.GithubAPIBaseURL, inputs.actor))
	}
	ctx.Trace(fmt.Sprintf("github: GET %srepos/%s/%s/collaborators/{queueActor}/permission to prove the queue identity can already push",
		registry.GithubAPIBaseURL, inputs.owner, inputs.repo))
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

func finishPlanRulesetBypass(ctx core.Context, client *http.Client, token string, inputs planRulesetBypassInputs, result PlanRulesetBypassResult) (PlanRulesetBypassResult, error) {
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

// githubRuleset keeps rules and conditions raw: the update endpoint replaces,
// so they must round-trip byte for byte.
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

type githubRulesetUpdate struct {
	Name         string               `json:"name"`
	Target       string               `json:"target"`
	Enforcement  string               `json:"enforcement"`
	Conditions   json.RawMessage      `json:"conditions,omitempty"`
	Rules        json.RawMessage      `json:"rules,omitempty"`
	BypassActors []RulesetBypassActor `json:"bypass_actors"`
}

func getGitHubRuleset(ctx context.Context, client *http.Client, token, owner, repo string, id int64) (githubRuleset, error) {
	requestURL := fmt.Sprintf("%srepos/%s/%s/rulesets/%d", registry.GithubAPIBaseURL, owner, repo, id)
	var ruleset githubRuleset
	if err := getGitHubJSON(ctx, client, token, requestURL, &ruleset); err != nil {
		return githubRuleset{}, err
	}
	return ruleset, nil
}

func checkRulesetGovernsBranch(ctx core.Context, ruleset githubRuleset, branch string) error {
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

// resolveQueueActorID resolves a login through GitHub so a typo cannot become a
// grant.
func resolveQueueActorID(ctx core.Context, client *http.Client, token string, inputs planRulesetBypassInputs) (int64, error) {
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
	if err := getGitHubJSON(context.Background(), client, token, registry.GithubAPIBaseURL+"users/"+inputs.actor, &user); err != nil {
		return 0, fmt.Errorf("resolve queue identity %q on github: %w", inputs.actor, err)
	}
	ctx.Trace(fmt.Sprintf("plan-ruleset-bypass: queue identity %s is user id %d (%s)", user.Login, user.ID, user.Type))
	return user.ID, nil
}

// resolveQueueActorPushAccess refuses an identity that cannot push, which would
// otherwise become the only bypass actor.
func resolveQueueActorPushAccess(ctx core.Context, client *http.Client, token string, inputs planRulesetBypassInputs) (string, error) {
	if inputs.actorType != "User" {
		ctx.Trace("plan-ruleset-bypass: push access for a " + inputs.actorType +
			" actor is not a collaborator permission; verify it with a real `git push --dry-run` as that identity before stage 2")
		return "", nil
	}
	var permission struct {
		Permission string `json:"permission"`
	}
	requestURL := fmt.Sprintf("%srepos/%s/%s/collaborators/%s/permission", registry.GithubAPIBaseURL, inputs.owner, inputs.repo, inputs.actor)
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

func rulesetWithQueueActorGranted(ruleset githubRuleset, actorID int64, actorType string) githubRulesetUpdate {
	actors := make([]RulesetBypassActor, 0, len(ruleset.BypassActors)+1)
	actors = append(actors, ruleset.BypassActors...)
	if !hasBypassActor(actors, actorID, actorType) {
		actors = append(actors, newRulesetBypassActor(actorID, actorType, RulesetBypassAlways))
	}
	return rulesetUpdatePayload(ruleset, actors)
}

// Exempt actors are demoted too: the bypass ledger cannot see them.
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

func writeRulesetPlanFile(ctx core.Context, path string, payload githubRulesetUpdate) error {
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

func canonicalRulesetBypassActorType(requested string) (string, bool) {
	for _, actorType := range rulesetBypassActorTypes {
		if strings.EqualFold(actorType, requested) {
			return actorType, true
		}
	}
	return "", false
}
