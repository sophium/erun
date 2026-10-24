package shared

import (
	"context"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/activity"
)

// A lease is a claim on one environment's idle-stop, so it only means anything
// in that environment's own store. Taken from the operator's machine it would
// hold the laptop busy and leave the environment reading as untouched — the
// exact outcome the lease exists to prevent.

type EnvironmentActivityLeaseResult struct {
	Tenant      string                              `json:"tenant"`
	Environment string                              `json:"environment"`
	Lease       *activity.EnvironmentActivityLease  `json:"lease,omitempty"`
	Held        []activity.EnvironmentActivityLease `json:"held"`
	// Released mirrors erun-mcp's ActivityLeaseResult.Released: set only by a
	// release call, and only true when a held claim actually existed and was
	// removed.
	Released *bool `json:"released,omitempty"`
}

func ListLeasesInEnvironment(ctx context.Context, commandCtx core.Context, resolveOpen OpenResolver, tenant, environment string) ([]activity.EnvironmentActivityLease, bool, error) {
	result, resolved, err := CallEnvironmentTool[EnvironmentActivityLeaseResult](ctx, commandCtx, resolveOpen, tenant, environment, "activity_lease_list", nil, true)
	return result.Held, resolved, err
}
