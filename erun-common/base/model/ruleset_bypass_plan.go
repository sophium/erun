package model

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
