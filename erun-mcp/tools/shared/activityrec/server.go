package activityrec

import (
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/activity/records"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimecfg"
)

func RecordRuntimeActivity(runtime runtimecfg.RuntimeConfig, kind string, seen bool) {
	_ = records.RecordEnvironmentActivity(model.EnvironmentActivityParams{
		Tenant:      runtime.Context.Tenant,
		Environment: runtime.Context.Environment,
		Kind:        kind,
		Seen:        seen,
	})
}
