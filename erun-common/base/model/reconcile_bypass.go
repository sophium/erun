package model

// The queue pushes raw commits, so a pull_request-rule bypass is unavoidable;
// this checks each bypassed push in GitHub's rule-suites ledger against the
// platform's successful builds (erun-backend-api/AGENTS.md).

// ReconcileBypassParams is the `erun exec reconcile-bypass` input.
type ReconcileBypassParams struct {
	RemoteURL string
	// RulesetID is required so another ruleset's bypasses are never folded in.
	RulesetID    int64
	TargetBranch string
	// Since is a rule-suites time_period (hour, day, week, month); empty keeps
	// GitHub's default.
	Since string
	// ExpectedActors may hold the bypass grant; empty accepts any actor.
	ExpectedActors []string
}

// BypassedPush is one push that bypassed a rule RulesetID enforces.
type BypassedPush struct {
	RuleSuiteID int64  `json:"ruleSuiteId"`
	Actor       string `json:"actor"`
	Commit      string `json:"commit"`
	// BeforeCommit bounds the pushed range with Commit; a batched merge or
	// release carries more than one commit.
	BeforeCommit string `json:"beforeCommit"`
	PushedAt     string `json:"pushedAt"`
	BypassedRule string `json:"bypassedRule"`
	Verdict      string `json:"verdict"`
	Reason       string `json:"reason,omitempty"`
	BuildID      string `json:"buildId,omitempty"`
	ReleaseTag   string `json:"releaseTag,omitempty"`
}

// ReconcileBypassResult is the full reconciliation report.
type ReconcileBypassResult struct {
	Owner        string         `json:"owner"`
	Repo         string         `json:"repo"`
	RulesetID    int64          `json:"rulesetId"`
	TargetBranch string         `json:"targetBranch"`
	Pushes       []BypassedPush `json:"pushes"`
	Unreconciled int            `json:"unreconciled"`
	// UnexpectedActors is separate from Unreconciled: wrong identity, not
	// unbuilt content.
	UnexpectedActors int `json:"unexpectedActors"`
}
