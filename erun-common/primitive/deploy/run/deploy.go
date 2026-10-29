package run

import (
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/seam/helmdeploy"
)

// RunDeploySpecs owns the step-timing root for `erun deploy` and runs every spec; `build --deploy`
// calls RunDeploySpec under the build root instead.
func RunDeploySpecs(ctx core.Context, executions []helmdeploy.DeploySpec, deploy helmdeploy.HelmChartDeployerFunc) error {
	if len(executions) == 0 {
		return nil
	}
	if !ctx.DryRun {
		ctx.Timing = core.NewStepTiming("deploy", nil)
	}
	err := runDeploySpecsPlan(ctx, executions, deploy)
	if root := ctx.Timing; root != nil {
		root.Finish(err)
		core.ReportStepTiming(ctx, "deploy", root)
	}
	return err
}
