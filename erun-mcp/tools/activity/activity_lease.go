package activity

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/activity/activitylease"
	"github.com/sophium/erun/erun-common/infra/activity/idlestatus"
	"github.com/sophium/erun/erun-mcp/tools/shared/callerid"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimecfg"
)

// A detached job makes no requests, so without a lease its environment reads as idle.

// ActivityLeaseTakeInput names the work the lease is being held for.
type ActivityLeaseTakeInput struct {
	Tenant      string `json:"tenant,omitempty" jsonschema:"tenant whose environment the lease is held on; defaults to the server tenant context, and must match it: this server only acts on its own environment"`
	Environment string `json:"environment,omitempty" jsonschema:"environment to hold; defaults to the server environment context, and must match it: this server only acts on its own environment"`
	Name        string `json:"name" jsonschema:"what the lease is holding the environment for; shown to the operator as the reason it reads as busy"`
	ID          string `json:"id,omitempty" jsonschema:"lease id to take or renew; defaults to the name, so re-taking the same name renews rather than stacking"`
	PID         int    `json:"pid,omitempty" jsonschema:"process id of the detached job; the lease is reclaimed once that process exits, so an abandoned lease cannot pin the environment awake"`
	TTLSeconds  int64  `json:"ttlSeconds,omitempty" jsonschema:"seconds the lease holds without a renewal; defaults to 900, or 300 when exclusive is set"`
	// Exclusive claims allow one holder per scope; only an "environment" claim
	// refuses other job starts, and a "worktree" claim blocks gate-merge.
	Exclusive    bool   `json:"exclusive,omitempty" jsonschema:"take an exclusive claim instead of plain presence: a second exclusive take in the same scope is refused and told who holds it, rather than silently coexisting. Take this before any mutating work in a target environment."`
	Scope        string `json:"scope,omitempty" jsonschema:"the resource this exclusive claim protects; defaults to 'worktree'. Only meaningful with exclusive=true - exclusivity is scoped, never environment-wide, so two jobs in two separate clones of the same repo in one pod can each hold their own claim"`
	Orchestrator string `json:"orchestrator,omitempty" jsonschema:"the calling orchestrator's own id (its $ERUN_ORCHESTRATOR_ID), recorded on the lease so a refusal can name who to go ask"`
}

// ActivityLeaseResult reports the lease state after the call.
type ActivityLeaseResult struct {
	Tenant      string                           `json:"tenant"`
	Environment string                           `json:"environment"`
	Lease       *model.EnvironmentActivityLease  `json:"lease,omitempty"`
	Held        []model.EnvironmentActivityLease `json:"held"`
	// Released is set only by release: false means nothing was held (still a success).
	Released *bool `json:"released,omitempty"`
}

func ActivityLeaseTakeTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ActivityLeaseTakeInput) (*mcp.CallToolResult, ActivityLeaseResult, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input ActivityLeaseTakeInput) (*mcp.CallToolResult, ActivityLeaseResult, error) {
		tenant, environment, err := runtimecfg.ResolveLocalTarget(runtime, input.Tenant, input.Environment)
		if err != nil {
			return nil, ActivityLeaseResult{}, err
		}
		identity := callerid.AuthIdentityFrom(ctx)
		id := strings.TrimSpace(input.ID)
		if id == "" {
			id = strings.TrimSpace(input.Name)
		}
		params := activitylease.TakeEnvironmentActivityLeaseParams{
			Tenant:      tenant,
			Environment: environment,
			Name:        input.Name,
			ID:          id,
			PID:         input.PID,
			TTL:         time.Duration(input.TTLSeconds) * time.Second,
			Scope:       input.Scope,
			Exclusive:   input.Exclusive,
			// Tenant and User come from the auth identity, never caller input.
			Holder: model.EnvironmentActivityLeaseHolder{
				Orchestrator: input.Orchestrator,
				Tenant:       identity.Tenant,
				User:         identity.User,
			},
		}
		if input.Exclusive && !exclusiveEnvironmentActivityLeaseRenewsOwnClaim(tenant, environment, params) {
			// Renewing one's own claim skips the operator-presence gate, or a running job
			// would refuse itself.
			status, statusErr := idlestatus.ResolveStoredEnvironmentIdleStatus(runtime.Store, tenant, environment, time.Now())
			if statusErr == nil {
				if reason, present := idlestatus.EnvironmentOperatorPresenceReason(status); present {
					return nil, ActivityLeaseResult{}, &model.EnvironmentOperatorPresentError{Reason: reason}
				}
			}
		}
		lease, err := activitylease.TakeEnvironmentActivityLease(params)
		if err != nil {
			return nil, ActivityLeaseResult{}, err
		}
		return activityLeaseResult(tenant, environment, &lease, nil)
	}
}

// exclusiveEnvironmentActivityLeaseRenewsOwnClaim reports whether this take
// renews the holder's existing claim; when it cannot tell, the presence gate
// still applies. params.ID is still unsanitized, so it is normalized the way the
// store does before comparing.
func exclusiveEnvironmentActivityLeaseRenewsOwnClaim(tenant, environment string, params activitylease.TakeEnvironmentActivityLeaseParams) bool {
	id, err := activitylease.ResolveEnvironmentActivityLeaseID(params.ID, params.Name)
	if err != nil {
		return false
	}
	scope := activitylease.NormalizeExclusiveEnvironmentActivityLeaseScope(params.Scope)
	held, err := activitylease.LoadEnvironmentActivityLeases(tenant, environment, time.Now())
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
	// An exclusive claim is keyed by scope, not id.
	Exclusive bool   `json:"exclusive,omitempty" jsonschema:"release an exclusive claim rather than a plain lease; must match how it was taken"`
	Scope     string `json:"scope,omitempty" jsonschema:"the scope the exclusive claim was taken on; defaults to 'worktree'. Only meaningful with exclusive=true"`
}

func ActivityLeaseReleaseTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ActivityLeaseReleaseInput) (*mcp.CallToolResult, ActivityLeaseResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ActivityLeaseReleaseInput) (*mcp.CallToolResult, ActivityLeaseResult, error) {
		tenant, environment, err := runtimecfg.ResolveLocalTarget(runtime, input.Tenant, input.Environment)
		if err != nil {
			return nil, ActivityLeaseResult{}, err
		}
		if strings.TrimSpace(input.ID) == "" {
			return nil, ActivityLeaseResult{}, fmt.Errorf("lease id is required")
		}
		var outcome model.EnvironmentActivityLeaseReleaseOutcome
		if input.Exclusive {
			outcome, err = activitylease.ReleaseExclusiveEnvironmentActivityLease(tenant, environment, input.Scope, input.ID)
			if err != nil {
				return nil, ActivityLeaseResult{}, err
			}
		} else {
			outcome, err = activitylease.ReleaseEnvironmentActivityLease(tenant, environment, input.ID)
			if err != nil {
				return nil, ActivityLeaseResult{}, err
			}
		}
		released := outcome == model.EnvironmentActivityLeaseReleased
		return activityLeaseResult(tenant, environment, nil, &released)
	}
}

// activityLeaseResult returns every claim still held; released is nil except on release.
func activityLeaseResult(tenant, environment string, lease *model.EnvironmentActivityLease, released *bool) (*mcp.CallToolResult, ActivityLeaseResult, error) {
	held, err := activitylease.LoadEnvironmentActivityLeases(tenant, environment, time.Now())
	if err != nil {
		return nil, ActivityLeaseResult{}, err
	}
	if held == nil {
		held = []model.EnvironmentActivityLease{}
	}
	return nil, ActivityLeaseResult{Tenant: tenant, Environment: environment, Lease: lease, Held: held, Released: released}, nil
}

// ActivityLeaseListInput selects the environment to read.
type ActivityLeaseListInput struct {
	Tenant      string `json:"tenant,omitempty" jsonschema:"tenant whose environment to read; defaults to the server tenant context, and must match it: this server only acts on its own environment"`
	Environment string `json:"environment,omitempty" jsonschema:"environment to read; defaults to the server environment context, and must match it: this server only acts on its own environment"`
}

func ActivityLeaseListTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ActivityLeaseListInput) (*mcp.CallToolResult, ActivityLeaseResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ActivityLeaseListInput) (*mcp.CallToolResult, ActivityLeaseResult, error) {
		tenant, environment, err := runtimecfg.ResolveLocalTarget(runtime, input.Tenant, input.Environment)
		if err != nil {
			return nil, ActivityLeaseResult{}, err
		}
		return activityLeaseResult(tenant, environment, nil, nil)
	}
}
