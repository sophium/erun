package activity

import (
	"context"

	"github.com/sophium/erun/cmd/shared/envcall"
	"github.com/sophium/erun/cmd/shared/envscope"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/activity"
)

func takeLeaseInEnvironment(ctx context.Context, commandCtx core.Context, resolveOpen envscope.OpenResolver, params activity.TakeEnvironmentActivityLeaseParams) (model.EnvironmentActivityLease, bool, error) {
	arguments := map[string]any{}
	envcall.PutEnvironmentToolArgument(arguments, "name", params.Name)
	envcall.PutEnvironmentToolArgument(arguments, "id", params.ID)
	envcall.PutEnvironmentToolArgument(arguments, "pid", params.PID)
	envcall.PutEnvironmentToolArgument(arguments, "ttlSeconds", envcall.LeaseTTLSeconds(params.TTL))
	if params.Exclusive {
		arguments["exclusive"] = true
	}
	envcall.PutEnvironmentToolArgument(arguments, "scope", params.Scope)
	envcall.PutEnvironmentToolArgument(arguments, "orchestrator", params.Holder.Orchestrator)
	result, resolved, err := envcall.CallEnvironmentTool[envcall.EnvironmentActivityLeaseResult](ctx, commandCtx, resolveOpen, params.Tenant, params.Environment, "activity_lease_take", arguments, false)
	if err != nil {
		return model.EnvironmentActivityLease{}, resolved, activity.DescribeExclusiveActivityLeaseVersionSkew(params.Tenant, params.Environment, params.Exclusive, err)
	}
	if !resolved {
		return model.EnvironmentActivityLease{}, resolved, nil
	}
	if result.Lease == nil {
		return model.EnvironmentActivityLease{}, resolved, nil
	}
	return *result.Lease, resolved, nil
}

func releaseLeaseInEnvironment(ctx context.Context, commandCtx core.Context, resolveOpen envscope.OpenResolver, tenant, environment, id, scope string, exclusive bool) (model.EnvironmentActivityLeaseReleaseOutcome, bool, error) {
	arguments := map[string]any{}
	envcall.PutEnvironmentToolArgument(arguments, "id", id)
	if exclusive {
		arguments["exclusive"] = true
	}
	envcall.PutEnvironmentToolArgument(arguments, "scope", scope)
	result, resolved, err := envcall.CallEnvironmentTool[envcall.EnvironmentActivityLeaseResult](ctx, commandCtx, resolveOpen, tenant, environment, "activity_lease_release", arguments, false)
	if err != nil || !resolved {
		return model.EnvironmentActivityLeaseNotHeld, resolved, err
	}
	if result.Released != nil && *result.Released {
		return model.EnvironmentActivityLeaseReleased, resolved, nil
	}
	return model.EnvironmentActivityLeaseNotHeld, resolved, nil
}
