package activity

import (
	"context"

	"github.com/sophium/erun/cmd/shared"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/activity"
)

func takeLeaseInEnvironment(ctx context.Context, commandCtx core.Context, resolveOpen shared.OpenResolver, params activity.TakeEnvironmentActivityLeaseParams) (activity.EnvironmentActivityLease, bool, error) {
	arguments := map[string]any{}
	shared.PutEnvironmentToolArgument(arguments, "name", params.Name)
	shared.PutEnvironmentToolArgument(arguments, "id", params.ID)
	shared.PutEnvironmentToolArgument(arguments, "pid", params.PID)
	shared.PutEnvironmentToolArgument(arguments, "ttlSeconds", shared.LeaseTTLSeconds(params.TTL))
	if params.Exclusive {
		arguments["exclusive"] = true
	}
	shared.PutEnvironmentToolArgument(arguments, "scope", params.Scope)
	shared.PutEnvironmentToolArgument(arguments, "orchestrator", params.Holder.Orchestrator)
	result, resolved, err := shared.CallEnvironmentTool[shared.EnvironmentActivityLeaseResult](ctx, commandCtx, resolveOpen, params.Tenant, params.Environment, "activity_lease_take", arguments, false)
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

func releaseLeaseInEnvironment(ctx context.Context, commandCtx core.Context, resolveOpen shared.OpenResolver, tenant, environment, id, scope string, exclusive bool) (activity.EnvironmentActivityLeaseReleaseOutcome, bool, error) {
	arguments := map[string]any{}
	shared.PutEnvironmentToolArgument(arguments, "id", id)
	if exclusive {
		arguments["exclusive"] = true
	}
	shared.PutEnvironmentToolArgument(arguments, "scope", scope)
	result, resolved, err := shared.CallEnvironmentTool[shared.EnvironmentActivityLeaseResult](ctx, commandCtx, resolveOpen, tenant, environment, "activity_lease_release", arguments, false)
	if err != nil || !resolved {
		return activity.EnvironmentActivityLeaseNotHeld, resolved, err
	}
	if result.Released != nil && *result.Released {
		return activity.EnvironmentActivityLeaseReleased, resolved, nil
	}
	return activity.EnvironmentActivityLeaseNotHeld, resolved, nil
}
