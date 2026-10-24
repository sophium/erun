package shared

import (
	"context"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/activity"
)

// A lease only means anything in the environment's own store, never the operator's machine.

type EnvironmentActivityLeaseResult struct {
	Tenant      string                              `json:"tenant"`
	Environment string                              `json:"environment"`
	Lease       *activity.EnvironmentActivityLease  `json:"lease,omitempty"`
	Held        []activity.EnvironmentActivityLease `json:"held"`
	// Released is set only by a release call, and is true only when a held claim was removed.
	Released *bool `json:"released,omitempty"`
}

func ListLeasesInEnvironment(ctx context.Context, commandCtx core.Context, resolveOpen OpenResolver, tenant, environment string) ([]activity.EnvironmentActivityLease, bool, error) {
	result, resolved, err := CallEnvironmentTool[EnvironmentActivityLeaseResult](ctx, commandCtx, resolveOpen, tenant, environment, "activity_lease_list", nil, true)
	return result.Held, resolved, err
}
