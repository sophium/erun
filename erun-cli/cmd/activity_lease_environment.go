package cmd

import (
	"context"

	common "github.com/sophium/erun/erun-common"
)

// A lease is a claim on one environment's idle-stop, so it only means anything
// in that environment's own store. Taken from the operator's machine it would
// hold the laptop busy and leave the environment reading as untouched — the
// exact outcome the lease exists to prevent.

type environmentActivityLeaseResult struct {
	Tenant      string                            `json:"tenant"`
	Environment string                            `json:"environment"`
	Lease       *common.EnvironmentActivityLease  `json:"lease,omitempty"`
	Held        []common.EnvironmentActivityLease `json:"held"`
}

func takeLeaseInEnvironment(ctx context.Context, commandCtx common.Context, resolveOpen OpenResolver, params common.TakeEnvironmentActivityLeaseParams) (common.EnvironmentActivityLease, bool, error) {
	arguments := map[string]any{}
	putEnvironmentToolArgument(arguments, "name", params.Name)
	putEnvironmentToolArgument(arguments, "id", params.ID)
	putEnvironmentToolArgument(arguments, "pid", params.PID)
	putEnvironmentToolArgument(arguments, "ttlSeconds", leaseTTLSeconds(params.TTL))
	result, resolved, err := callEnvironmentTool[environmentActivityLeaseResult](ctx, commandCtx, resolveOpen, params.Tenant, params.Environment, "activity_lease_take", arguments)
	if err != nil || !resolved {
		return common.EnvironmentActivityLease{}, resolved, err
	}
	if result.Lease == nil {
		return common.EnvironmentActivityLease{}, resolved, nil
	}
	return *result.Lease, resolved, nil
}

func releaseLeaseInEnvironment(ctx context.Context, commandCtx common.Context, resolveOpen OpenResolver, tenant, environment, id string) (bool, error) {
	arguments := map[string]any{}
	putEnvironmentToolArgument(arguments, "id", id)
	_, resolved, err := callEnvironmentTool[environmentActivityLeaseResult](ctx, commandCtx, resolveOpen, tenant, environment, "activity_lease_release", arguments)
	return resolved, err
}

func listLeasesInEnvironment(ctx context.Context, commandCtx common.Context, resolveOpen OpenResolver, tenant, environment string) ([]common.EnvironmentActivityLease, bool, error) {
	result, resolved, err := callEnvironmentTool[environmentActivityLeaseResult](ctx, commandCtx, resolveOpen, tenant, environment, "activity_lease_list", nil)
	return result.Held, resolved, err
}
