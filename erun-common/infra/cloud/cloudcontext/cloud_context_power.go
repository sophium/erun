package cloudcontext

import (
	"strings"

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud/providerconfig"
)

// IsInClusterCloudContext reports whether a context names this process's own cluster, which is always running.
func IsInClusterCloudContext(config model.CloudContextConfig) bool {
	return strings.TrimSpace(config.Name) == providerconfig.InClusterKubernetesContext ||
		strings.TrimSpace(config.KubernetesContext) == providerconfig.InClusterKubernetesContext
}
