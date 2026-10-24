package cmd

import (
	"context"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/activity"
)

// A lease is a claim on one environment's idle-stop, so it only means anything
// in that environment's own store. Taken from the operator's machine it would
// hold the laptop busy and leave the environment reading as untouched — the
// exact outcome the lease exists to prevent.

type environmentActivityLeaseResult struct {
	Tenant      string                              `json:"tenant"`
	Environment string                              `json:"environment"`
	Lease       *activity.EnvironmentActivityLease  `json:"lease,omitempty"`
	Held        []activity.EnvironmentActivityLease `json:"held"`
	// Released mirrors erun-mcp's ActivityLeaseResult.Released: set only by a
	// release call, and only true when a held claim actually existed and was
	// removed.
	Released *bool `json:"released,omitempty"`
}

func takeLeaseInEnvironment(ctx context.Context, commandCtx core.Context, resolveOpen OpenResolver, params activity.TakeEnvironmentActivityLeaseParams) (activity.EnvironmentActivityLease, bool, error) {
	arguments := map[string]any{}
	putEnvironmentToolArgument(arguments, "name", params.Name)
	putEnvironmentToolArgument(arguments, "id", params.ID)
	putEnvironmentToolArgument(arguments, "pid", params.PID)
	putEnvironmentToolArgument(arguments, "ttlSeconds", leaseTTLSeconds(params.TTL))
	if params.Exclusive {
		arguments["exclusive"] = true
	}
	putEnvironmentToolArgument(arguments, "scope", params.Scope)
	putEnvironmentToolArgument(arguments, "orchestrator", params.Holder.Orchestrator)
	result, resolved, err := callEnvironmentTool[environmentActivityLeaseResult](ctx, commandCtx, resolveOpen, params.Tenant, params.Environment, "activity_lease_take", arguments, false)
	if err != nil {
		return activity.EnvironmentActivityLease{}, resolved, activity.DescribeExclusiveActivityLeaseVersionSkew(params.Tenant, params.Environment, params.Exclusive, err)
	}
	if !resolved {
		return activity.EnvironmentActivityLease{}, resolved, nil
	}
	if result.Lease == nil {
		return activity.EnvironmentActivityLease{}, resolved, nil
	}
	return *result.Lease, resolved, nil
}

func releaseLeaseInEnvironment(ctx context.Context, commandCtx core.Context, resolveOpen OpenResolver, tenant, environment, id, scope string, exclusive bool) (activity.EnvironmentActivityLeaseReleaseOutcome, bool, error) {
	arguments := map[string]any{}
	putEnvironmentToolArgument(arguments, "id", id)
	if exclusive {
		arguments["exclusive"] = true
	}
	putEnvironmentToolArgument(arguments, "scope", scope)
	result, resolved, err := callEnvironmentTool[environmentActivityLeaseResult](ctx, commandCtx, resolveOpen, tenant, environment, "activity_lease_release", arguments, false)
	if err != nil || !resolved {
		return activity.EnvironmentActivityLeaseNotHeld, resolved, err
	}
	if result.Released != nil && *result.Released {
		return activity.EnvironmentActivityLeaseReleased, resolved, nil
	}
	return activity.EnvironmentActivityLeaseNotHeld, resolved, nil
}

func listLeasesInEnvironment(ctx context.Context, commandCtx core.Context, resolveOpen OpenResolver, tenant, environment string) ([]activity.EnvironmentActivityLease, bool, error) {
	result, resolved, err := callEnvironmentTool[environmentActivityLeaseResult](ctx, commandCtx, resolveOpen, tenant, environment, "activity_lease_list", nil, true)
	return result.Held, resolved, err
}
