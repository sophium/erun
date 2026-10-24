package envcall

import (
	"context"

	"github.com/sophium/erun/cmd/shared/envscope"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
)

// A lease only means anything in the environment's own store, never the operator's machine.

type EnvironmentActivityLeaseResult struct {
	Tenant      string                           `json:"tenant"`
	Environment string                           `json:"environment"`
	Lease       *model.EnvironmentActivityLease  `json:"lease,omitempty"`
	Held        []model.EnvironmentActivityLease `json:"held"`
	// Released is set only by a release call, and is true only when a held claim was removed.
	Released *bool `json:"released,omitempty"`
}

func ListLeasesInEnvironment(ctx context.Context, commandCtx core.Context, resolveOpen envscope.OpenResolver, tenant, environment string) ([]model.EnvironmentActivityLease, bool, error) {
	result, resolved, err := CallEnvironmentTool[EnvironmentActivityLeaseResult](ctx, commandCtx, resolveOpen, tenant, environment, "activity_lease_list", nil, true)
	return result.Held, resolved, err
}
