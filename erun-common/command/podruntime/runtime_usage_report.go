package podruntime

import (
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/primitive/deploy"
)

// The live reading is fed to RecommendRuntimeSizing with the retained history, so warnings
// and the recommendation come from one verdict rather than two engines.

// RuntimeUsageReport is a live reading plus the sizing recommendation `erun list` reports.
type RuntimeUsageReport struct {
	config.RuntimeUsage
	// Sizing is nil when nothing was observed or the history could not be read.
	Sizing *deploy.RuntimeSizingRecommendation `json:"sizing,omitempty"`
}

// ResolveRuntimeUsageReport treats a history read failure as silence: the report is advisory.
func ResolveRuntimeUsageReport(tenant string, env config.EnvConfig, usage config.RuntimeUsage) RuntimeUsageReport {
	report := RuntimeUsageReport{RuntimeUsage: usage}
	history, err := deploy.LoadRuntimeUsageHistory(tenant, env.Name)
	if err != nil {
		return report
	}
	recommendation, ok := deploy.RecommendRuntimeSizing(deploy.RuntimeSizingParams{
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
