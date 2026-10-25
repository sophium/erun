package managedcloud

import (
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
)

func ManagedCloudEnvironment(store configpkg.CloudReadStore, env configpkg.EnvConfig) (bool, error) {
	if env.ManagedCloud {
		return true, nil
	}
	if !env.RemoteWorktree() {
		return false, nil
	}
	status, ok, err := configpkg.FindCloudContextForKubernetesContext(store, env.KubernetesContext)
	if err != nil || !ok {
		return false, err
	}
	if alias := strings.TrimSpace(env.CloudProviderAlias); alias != "" && strings.TrimSpace(status.CloudProviderAlias) != alias {
		return false, nil
	}
	return true, nil
}
