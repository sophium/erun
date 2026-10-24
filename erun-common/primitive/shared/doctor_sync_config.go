package shared

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud"
)

// InjectedRuntimeConfig is the config the chart-injected ERUN_* vars describe.
type InjectedRuntimeConfig struct {
	Tenant      string
	Environment string
	Env         configpkg.EnvConfig
	Providers   []model.CloudProviderConfig
	Contexts    []model.CloudContextConfig
}

// ResolveInjectedRuntimeConfig reports ok=false without a tenant and environment.
func ResolveInjectedRuntimeConfig(env func(string) string) (InjectedRuntimeConfig, bool) {
	if env == nil {
		env = os.Getenv
	}
	get := func(key string) string { return strings.TrimSpace(env(key)) }

	tenant := get("ERUN_TENANT")
	environment := get("ERUN_ENVIRONMENT")
	if tenant == "" || environment == "" {
		return InjectedRuntimeConfig{}, false
	}

	envType := configpkg.EnvironmentType(get("ERUN_ENV_TYPE"))
	if !envType.IsValid() {
		envType = configpkg.LegacyEnvTypeFromRemoteSnapshot(strings.EqualFold(get("ERUN_REPO_REMOTE"), "true"), nil)
	}
	kubernetesContext := get("ERUN_KUBERNETES_CONTEXT")
	if kubernetesContext == "" {
		kubernetesContext = cloud.InClusterKubernetesContext
	}
	provider := get("ERUN_CLOUD_PROVIDER")
	alias := get("ERUN_CLOUD_PROVIDER_ALIAS")
	region := get("ERUN_CLOUD_REGION")
	instanceID := get("ERUN_CLOUD_INSTANCE_ID")
	contextName := get("ERUN_CLOUD_CONTEXT_NAME")

	injected := InjectedRuntimeConfig{
		Tenant:      tenant,
		Environment: environment,
		Env: configpkg.EnvConfig{
			Name:               environment,
			Type:               envType,
			KubernetesContext:  kubernetesContext,
			CloudProviderAlias: alias,
			ManagedCloud:       injectedManagedCloud(get, provider, alias, region),
			RuntimeRegistry:    get("ERUN_RUNTIME_REGISTRY"),
			DisableBuildScript: strings.EqualFold(get("ERUN_DISABLE_BUILD_SCRIPT"), "true"),
			Idle:               injectedIdleConfig(get),
		},
	}
	if registries := parseInjectedContainerRegistries(env("ERUN_CONTAINER_REGISTRIES")); len(registries) > 0 {
		injected.Env.ContainerRegistries = registries
	}
	injected.Providers, injected.Contexts = injectedCloudConfig(provider, alias, region, instanceID, contextName, kubernetesContext)
	return injected, true
}

func injectedCloudConfig(provider, alias, region, instanceID, contextName, kubernetesContext string) ([]model.CloudProviderConfig, []model.CloudContextConfig) {
	if provider == "" || alias == "" {
		return nil, nil
	}
	username, accountID, _, _ := configpkg.ParseCloudProviderAlias(alias)
	providers := []model.CloudProviderConfig{{Alias: alias, Provider: provider, Username: username, AccountID: accountID}}
	if region == "" {
		return providers, nil
	}
	// Normalize like every writer does, or the injected "" never matches the
	// on-disk default and drift never clears.
	contexts := []model.CloudContextConfig{configpkg.NormalizeCloudContextConfig(model.CloudContextConfig{
		Name:               contextName,
		Provider:           provider,
		CloudProviderAlias: alias,
		Region:             region,
		InstanceID:         instanceID,
		KubernetesContext:  kubernetesContext,
	})}
	return providers, contexts
}

func injectedManagedCloud(get func(string) string, provider, alias, region string) bool {
	if strings.EqualFold(get("ERUN_CLOUD_ENVIRONMENT"), "true") {
		return true
	}
	if !strings.EqualFold(get("ERUN_REPO_REMOTE"), "true") {
		return false
	}
	return provider != "" && alias != "" && region != ""
}

func injectedIdleConfig(get func(string) string) configpkg.EnvironmentIdleConfig {
	idle := configpkg.EnvironmentIdleConfig{
		Timeout:      get("ERUN_IDLE_TIMEOUT"),
		WorkingHours: get("ERUN_IDLE_WORKING_HOURS"),
		Timezone:     get("ERUN_IDLE_TIMEZONE"),
	}
	if idle.Timeout == "" {
		idle.Timeout = "5m0s"
	}
	if idle.WorkingHours == "" {
		idle.WorkingHours = "08:00-20:00"
	}
	if bytes := strings.TrimSpace(get("ERUN_IDLE_TRAFFIC_BYTES")); bytes != "" && bytes != "0" {
		var parsed int64
		if _, err := fmt.Sscan(bytes, &parsed); err == nil {
			idle.IdleTrafficBytes = parsed
		}
	}
	return idle
}

func parseInjectedContainerRegistries(value string) configpkg.ContainerRegistries {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	var registries configpkg.ContainerRegistries
	if err := json.Unmarshal([]byte(value), &registries); err != nil {
		return nil
	}
	return registries
}

func OverlayInjectedEnvConfig(onDisk, injected configpkg.EnvConfig) configpkg.EnvConfig {
	onDisk.Name = injected.Name
	onDisk.Type = injected.Type
	onDisk.KubernetesContext = injected.KubernetesContext
	onDisk.CloudProviderAlias = injected.CloudProviderAlias
	onDisk.ManagedCloud = injected.ManagedCloud
	onDisk.Idle = injected.Idle
	onDisk.RuntimeRegistry = injected.RuntimeRegistry
	onDisk.ContainerRegistries = injected.ContainerRegistries
	onDisk.DisableBuildScript = injected.DisableBuildScript
	return onDisk
}
