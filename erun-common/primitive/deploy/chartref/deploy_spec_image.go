package chartref

import (
	"slices"
	"strings"

	"github.com/sophium/erun/erun-common/base/model"
)

func DeployContextOwnsRuntimeChart(deployContext model.KubernetesDeployContext, tenant string) bool {
	name := strings.TrimSpace(deployContext.ComponentName)
	if name == "" {
		return false
	}
	// Match either runtime alias, as erun open does (runtimeComponentNames).
	return slices.Contains(RuntimeComponentNames(tenant), name)
}
