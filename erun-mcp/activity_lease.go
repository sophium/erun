package erunmcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

// The lease tools are the MCP half of the activity lease. An orchestrator that
// detaches a long job in this pod has no request to bump for the job's duration,
// so without a lease the environment it is driving hard reads as untouched.

// ActivityLeaseTakeInput names the work the lease is being held for.
type ActivityLeaseTakeInput struct {
	Tenant      string `json:"tenant,omitempty" jsonschema:"tenant whose environment the lease is held on; defaults to the server tenant context, and must match it: this server only acts on its own environment"`
	Environment string `json:"environment,omitempty" jsonschema:"environment to hold; defaults to the server environment context, and must match it: this server only acts on its own environment"`
	Name        string `json:"name" jsonschema:"what the lease is holding the environment for; shown to the operator as the reason it reads as busy"`
	ID          string `json:"id,omitempty" jsonschema:"lease id to take or renew; defaults to the name, so re-taking the same name renews rather than stacking"`
	PID         int    `json:"pid,omitempty" jsonschema:"process id of the detached job; the lease is reclaimed once that process exits, so an abandoned lease cannot pin the environment awake"`
	TTLSeconds  int64  `json:"ttlSeconds,omitempty" jsonschema:"seconds the lease holds without a renewal; defaults to 900, or 300 when exclusive is set"`
	// Exclusive, Scope, and Orchestrator request the exclusive-claim mode
	// added for erun#1245: at most one exclusive holder per scope, so a
	// second agent job or orchestrator working the same worktree is refused
	// and told who holds it, while a second job in a different scope (a
	// separate clone in the same pod) is unaffected.
	Exclusive    bool   `json:"exclusive,omitempty" jsonschema:"take an exclusive claim instead of plain presence: a second exclusive take in the same scope is refused and told who holds it, rather than silently coexisting. Take this before any mutating work in a target environment."`
	Scope        string `json:"scope,omitempty" jsonschema:"the resource this exclusive claim protects; defaults to 'worktree'. Only meaningful with exclusive=true - exclusivity is scoped, never environment-wide, so two jobs in two separate clones of the same repo in one pod can each hold their own claim"`
	Orchestrator string `json:"orchestrator,omitempty" jsonschema:"the calling orchestrator's own id (its $ERUN_ORCHESTRATOR_ID), recorded on the lease so a refusal can name who to go ask"`
}

// ActivityLeaseResult reports the lease state after the call.
type ActivityLeaseResult struct {
	Tenant      string                                `json:"tenant"`
	Environment string                                `json:"environment"`
	Lease       *eruncommon.EnvironmentActivityLease  `json:"lease,omitempty"`
	Held        []eruncommon.EnvironmentActivityLease `json:"held"`
	// Released and Note are set only by activity_lease_release. Released is
	// true only when a matching lease was actually removed — before erun#2115
	// a no-match and a real release both reported identical success. Note
	// explains a no-match when the id is actually held under a different
	// shape (plain vs exclusive, or a different scope) than the caller asked
	// to release.
	Released bool   `json:"released,omitempty"`
	Note     string `json:"note,omitempty"`
}

func activityLeaseTakeTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ActivityLeaseTakeInput) (*mcp.CallToolResult, ActivityLeaseResult, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input ActivityLeaseTakeInput) (*mcp.CallToolResult, ActivityLeaseResult, error) {
		tenant, environment, err := resolveLocalTarget(runtime, input.Tenant, input.Environment)
		if err != nil {
			return nil, ActivityLeaseResult{}, err
		}
		identity := authIdentityFrom(ctx)
		id := strings.TrimSpace(input.ID)
		if id == "" {
			id = strings.TrimSpace(input.Name)
		}
		params := eruncommon.TakeEnvironmentActivityLeaseParams{
			Tenant:      tenant,
			Environment: environment,
			Name:        input.Name,
			ID:          id,
			PID:         input.PID,
			TTL:         time.Duration(input.TTLSeconds) * time.Second,
			Scope:       input.Scope,
			Exclusive:   input.Exclusive,
			// Tenant and User come from the resolved auth identity, never
			// from caller input, so a lease cannot be taken out in someone
			// else's name.
			Holder: eruncommon.EnvironmentActivityLeaseHolder{
				Orchestrator: input.Orchestrator,
				Tenant:       identity.Tenant,
				User:         identity.User,
			},
		}
		if input.Exclusive && !exclusiveEnvironmentActivityLeaseRenewsOwnClaim(tenant, environment, params) {
			// The operator never takes a lease, so an operator's own
			// interactive session leaves no claim to renew here - only a
			// fresh claim is gated on their presence. Renewing a claim this
			// same caller already holds is not re-litigated: by the time it
			// is running detached work, that work's own resident process is
			// indistinguishable from an operator's, and gating renewal on it
			// would make the job refuse itself.
			status, statusErr := eruncommon.ResolveStoredEnvironmentIdleStatus(runtime.Store, tenant, environment, time.Now())
			if statusErr == nil {
				if reason, present := eruncommon.EnvironmentOperatorPresenceReason(status); present {
					return nil, ActivityLeaseResult{}, &eruncommon.EnvironmentOperatorPresentError{Reason: reason}
				}
			}
		}
		lease, err := eruncommon.TakeEnvironmentActivityLease(params)
		if err != nil {
			return nil, ActivityLeaseResult{}, err
		}
		return activityLeaseResult(tenant, environment, &lease)
	}
}

// exclusiveEnvironmentActivityLeaseRenewsOwnClaim reports whether this take
// would renew a claim the same holder id already has, in which case the
// operator-presence gate does not apply. A best-effort read: if it cannot
// tell, the take proceeds to the gate rather than skipping it.
//
// params.ID is still the caller's raw, unsanitized input at this point — the
// store only sanitizes it inside TakeEnvironmentActivityLease, which has not
// run yet — so it is resolved through the same normalisation the store uses
// before comparing against the stored, already-sanitized lease.ID. Comparing
// the raw value directly is how erun#1652 happened: a lease id needing
// sanitisation (a space, a slash, a colon) never matched its stored form, so
// every renewal misread as a fresh claim.
func exclusiveEnvironmentActivityLeaseRenewsOwnClaim(tenant, environment string, params eruncommon.TakeEnvironmentActivityLeaseParams) bool {
	id, err := eruncommon.ResolveEnvironmentActivityLeaseID(params.ID, params.Name)
	if err != nil {
		return false
	}
	scope := eruncommon.NormalizeExclusiveEnvironmentActivityLeaseScope(params.Scope)
	held, err := eruncommon.LoadEnvironmentActivityLeases(tenant, environment, time.Now())
	if err != nil {
		return false
	}
	for _, lease := range held {
		if lease.Exclusive && lease.Scope == scope && lease.ID == id {
			return true
		}
	}
	return false
}

// ActivityLeaseReleaseInput selects the lease to drop.
type ActivityLeaseReleaseInput struct {
	Tenant      string `json:"tenant,omitempty" jsonschema:"tenant whose environment holds the lease; defaults to the server tenant context, and must match it: this server only acts on its own environment"`
	Environment string `json:"environment,omitempty" jsonschema:"environment to release; defaults to the server environment context, and must match it: this server only acts on its own environment"`
	ID          string `json:"id" jsonschema:"lease id to release; the name passed to activity_lease_take when no explicit id was given"`
	// Exclusive and Scope select the exclusive-claim store; an exclusive
	// claim is keyed by scope, not by id, so releasing one needs to know
	// which scope it was taken on.
	Exclusive bool   `json:"exclusive,omitempty" jsonschema:"release an exclusive claim rather than a plain lease; must match how it was taken"`
	Scope     string `json:"scope,omitempty" jsonschema:"the scope the exclusive claim was taken on; defaults to 'worktree'. Only meaningful with exclusive=true"`
}

func activityLeaseReleaseTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ActivityLeaseReleaseInput) (*mcp.CallToolResult, ActivityLeaseResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ActivityLeaseReleaseInput) (*mcp.CallToolResult, ActivityLeaseResult, error) {
		tenant, environment, err := resolveLocalTarget(runtime, input.Tenant, input.Environment)
		if err != nil {
			return nil, ActivityLeaseResult{}, err
		}
		if strings.TrimSpace(input.ID) == "" {
			return nil, ActivityLeaseResult{}, fmt.Errorf("lease id is required")
		}
		var result eruncommon.ReleaseEnvironmentActivityLeaseResult
		if input.Exclusive {
			result, err = eruncommon.ReleaseExclusiveEnvironmentActivityLease(tenant, environment, input.Scope, input.ID)
		} else {
			result, err = eruncommon.ReleaseEnvironmentActivityLease(tenant, environment, input.ID)
		}
		if err != nil {
			return nil, ActivityLeaseResult{}, err
		}
		return activityLeaseReleaseResult(tenant, environment, result)
	}
}

// activityLeaseResult always returns what is still held, so a caller sees the
// environment's whole claim set rather than only the lease it just moved.
func activityLeaseResult(tenant, environment string, lease *eruncommon.EnvironmentActivityLease) (*mcp.CallToolResult, ActivityLeaseResult, error) {
	held, err := eruncommon.LoadEnvironmentActivityLeases(tenant, environment, time.Now())
	if err != nil {
		return nil, ActivityLeaseResult{}, err
	}
	if held == nil {
		held = []eruncommon.EnvironmentActivityLease{}
	}
	return nil, ActivityLeaseResult{Tenant: tenant, Environment: environment, Lease: lease, Held: held}, nil
}

// activityLeaseReleaseResult is activityLeaseResult's release-only sibling: it
// folds the release outcome (Released/Note) into the same whole-claim-set
// response, so a caller sees both "did my release do anything" and "what is
// still held" from a single call.
func activityLeaseReleaseResult(tenant, environment string, release eruncommon.ReleaseEnvironmentActivityLeaseResult) (*mcp.CallToolResult, ActivityLeaseResult, error) {
	_, result, err := activityLeaseResult(tenant, environment, nil)
	if err != nil {
		return nil, ActivityLeaseResult{}, err
	}
	result.Released = release.Released
	result.Note = release.Note
	return nil, result, nil
}

// ActivityLeaseListInput selects the environment to read.
type ActivityLeaseListInput struct {
	Tenant      string `json:"tenant,omitempty" jsonschema:"tenant whose environment to read; defaults to the server tenant context, and must match it: this server only acts on its own environment"`
	Environment string `json:"environment,omitempty" jsonschema:"environment to read; defaults to the server environment context, and must match it: this server only acts on its own environment"`
}

func activityLeaseListTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ActivityLeaseListInput) (*mcp.CallToolResult, ActivityLeaseResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ActivityLeaseListInput) (*mcp.CallToolResult, ActivityLeaseResult, error) {
		tenant, environment, err := resolveLocalTarget(runtime, input.Tenant, input.Environment)
		if err != nil {
			return nil, ActivityLeaseResult{}, err
		}
		return activityLeaseResult(tenant, environment, nil)
	}
}
