package model

// AISessionAttachOutcome is why an attach to a persistent session ended.
type AISessionAttachOutcome string

const (
	// AISessionAttachOutcomeTakenOver: another attach evicted this viewer; the
	// session keeps running.
	AISessionAttachOutcomeTakenOver AISessionAttachOutcome = "taken-over"
	// AISessionAttachOutcomeDeployReattach: reattach once the new pod is ready.
	AISessionAttachOutcomeDeployReattach AISessionAttachOutcome = "deploy-reattach"
	AISessionAttachOutcomeEnded          AISessionAttachOutcome = "ended"
	// AISessionAttachOutcomeUnknown: no exit status; never collapse it into a guess.
	AISessionAttachOutcomeUnknown AISessionAttachOutcome = "unknown"
)

type OpenParams struct {
	Tenant                string
	Environment           string
	UseDefaultTenant      bool
	UseDefaultEnvironment bool
	// Command is the operator-facing invocation named in a resolution failure's recovery;
	// empty falls back to command-free wording.
	Command string
	// CommandScopesTenantByFlag gates whether the recovery may name Command's --tenant flag.
	CommandScopesTenantByFlag bool
}
