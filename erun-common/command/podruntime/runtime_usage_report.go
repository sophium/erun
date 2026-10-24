package podruntime

import (
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/deploy/runtimeusage"
)

// ResolveRuntimeUsageReport treats a history read failure as silence: the report is advisory.
func ResolveRuntimeUsageReport(tenant string, env config.EnvConfig, usage model.RuntimeUsage) model.RuntimeUsageReport {
	report := model.RuntimeUsageReport{RuntimeUsage: usage}
	history, err := runtimeusage.LoadRuntimeUsageHistory(tenant, env.Name)
	if err != nil {
		return report
	}
	recommendation, ok := runtimeusage.RecommendRuntimeSizing(runtimeusage.RuntimeSizingParams{
		History: history,
		Ceiling: env.NamespaceQuota,
		Live:    &usage,
	})
	if !ok {
		return report
	}
	report.Sizing = &recommendation
	return report
}
