package shared

import (
	"github.com/sophium/erun/erun-common/infra/activity"
)

func RecordRuntimeActivity(runtime RuntimeConfig, kind string, seen bool) {
	_ = activity.RecordEnvironmentActivity(activity.EnvironmentActivityParams{
		Tenant:      runtime.Context.Tenant,
		Environment: runtime.Context.Environment,
		Kind:        kind,
		Seen:        seen,
	})
}
