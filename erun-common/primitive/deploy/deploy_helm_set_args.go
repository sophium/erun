package deploy

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
)

func formatHelmBool(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func formatHelmPort(value, fallback int) string {
	if value <= 0 {
		value = fallback
	}
	return fmt.Sprintf("%d", value)
}

func formatHelmInt64(value int64) string {
	return fmt.Sprintf("%d", value)
}

func sortedStringMapKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func helmIdleTimeout(config configpkg.EnvironmentIdleConfig) string {
	policy, err := config.Resolve()
	if err != nil {
		return configpkg.DefaultEnvironmentIdleTimeout.String()
	}
	return policy.Timeout.String()
}

func helmIdleWorkingHours(config configpkg.EnvironmentIdleConfig) string {
	policy, err := config.Resolve()
	if err != nil {
		return configpkg.DefaultEnvironmentWorkingHours
	}
	return policy.WorkingHours
}

func helmIdleTrafficBytes(config configpkg.EnvironmentIdleConfig) int64 {
	policy, err := config.Resolve()
	if err != nil {
		return configpkg.DefaultEnvironmentIdleTrafficBytes
	}
	return policy.IdleTrafficBytes
}

func helmIdleTimezone(config configpkg.EnvironmentIdleConfig) string {
	policy, err := config.Resolve()
	if err != nil {
		return ""
	}
	return policy.Timezone
}

// helmCloudContextSetArgs omits an empty region: the chart turns it into
// AWS_REGION, and an empty AWS_REGION overrides the pod's own AWS profile.
func helmCloudContextSetArgs(d HelmDeploySpec) []string {
	args := []string{
		"--set-string", "cloudContext.name=" + d.CloudContextName,
		"--set-string", "cloudContext.provider=" + d.CloudProvider,
		"--set-string", "cloudContext.providerAlias=" + d.CloudProviderAlias,
	}
	if region := strings.TrimSpace(d.CloudRegion); region != "" {
		args = append(args, "--set-string", "cloudContext.region="+region)
	}
	return append(args,
		"--set-string", "cloudContext.instanceId="+d.CloudInstanceID,
		"--set", "cloudContext.useHostCredentials="+formatHelmBool(d.UseHostCredentials),
	)
}

// helmRegistrySetArgs guards each value on presence so older charts are
// unaffected.
func helmRegistrySetArgs(d HelmDeploySpec) []string {
	var args []string
	if registry := strings.TrimSpace(d.ContainerRegistry); registry != "" {
		args = append(args, "--set-string", "containerRegistry="+registry)
	}
	if registry := strings.TrimSpace(d.RuntimeRegistry); registry != "" {
		args = append(args, "--set-string", "runtimeRegistry="+registry)
	}
	if registry := strings.TrimSpace(d.InsecureRegistry); registry != "" {
		args = append(args, "--set-string", "clusterRegistryInsecure="+registry)
	}
	if len(d.ContainerRegistries) > 0 {
		if encoded, marshalErr := json.Marshal(d.ContainerRegistries); marshalErr == nil {
			args = append(args, "--set-json", "containerRegistries="+string(encoded))
		}
	}
	return args
}

// helmPlatformAccountSetArgs binds the env's SA to cluster-admin when set; the
// chart default prunes the binding when absent.
func helmPlatformAccountSetArgs(platformAccount bool) []string {
	if !platformAccount {
		return nil
	}
	return []string{"--set", "platformAccount=true"}
}

// helmStoppedSetArgs renders stopped=true only; absent renders replicas: 1.
func helmStoppedSetArgs(stopped bool) []string {
	if !stopped {
		return nil
	}
	return []string{"--set", "stopped=true"}
}

// helmPlatformSetArgs is read only by the platform singletons.
func helmPlatformSetArgs(p configpkg.PlatformConfig) []string {
	if p.IsZero() {
		return nil
	}
	args := []string{
		"--set-string", "platform.baseDomain=" + escapeHelmSetValue(p.BaseDomain),
		"--set-string", "platform.env=" + escapeHelmSetValue(p.Env),
		"--set-string", "platform.servicesZone=" + escapeHelmSetValue(p.ServicesZone),
		"--set-string", "platform.authoritativeIP=" + escapeHelmSetValue(p.AuthoritativeIP),
		"--set-string", "platform.authHost=" + escapeHelmSetValue(p.AuthHost),
		"--set-string", "platform.caaIssuer=" + escapeHelmSetValue(p.CAAIssuer),
		"--set-string", "platform.apiUrl=" + escapeHelmSetValue(p.APIURL),
		"--set-string", "platform.consoleUrl=" + escapeHelmSetValue(p.ConsoleURL),
		"--set-string", "platform.brand=" + escapeHelmSetValue(p.Brand),
		"--set-string", "platform.docsUrl=" + escapeHelmSetValue(p.DocsURL),
		"--set-string", "platform.tagline=" + escapeHelmSetValue(p.Tagline),
		"--set-string", "platform.logoUrl=" + escapeHelmSetValue(p.LogoURL),
	}
	if len(p.Nameservers) > 0 {
		if encoded, marshalErr := json.Marshal(p.Nameservers); marshalErr == nil {
			args = append(args, "--set-json", "platform.nameservers="+string(encoded))
		}
	}
	return args
}

func helmImagePullSecretSetArgs(secrets []string) []string {
	args := make([]string, 0, len(secrets)*2)
	idx := 0
	for _, name := range secrets {
		if name = strings.TrimSpace(name); name == "" {
			continue
		}
		args = append(args, "--set-string", fmt.Sprintf("imagePullSecrets[%d].name=%s", idx, name))
		idx++
	}
	return args
}

func helmRegistryCredentialSecretSetArgs(name string) []string {
	if name = strings.TrimSpace(name); name == "" {
		return nil
	}
	return []string{"--set-string", "registryCredentialSecretName=" + name}
}

func helmPlatformAliasSecretSetArgs(name string) []string {
	if name = strings.TrimSpace(name); name == "" {
		return nil
	}
	return []string{"--set-string", "platformAliasSecretName=" + name}
}

func helmClaudeSetArgs(config configpkg.EnvironmentClaudeConfig, gateway *configpkg.OpenRouterConfig) []string {
	args := make([]string, 0, 12)
	// Claude Code reads CLAUDE_CODE_USE_BEDROCK/_MANTLE ahead of ANTHROPIC_BASE_URL,
	// so a gateway must turn them off.
	gatewayOn := configpkg.EffectiveGateway(config, gateway).Configured()
	useMantle := !gatewayOn && resolveClaudeBool(config.UseMantle, configpkg.DefaultClaudeUseMantle)
	useBedrock := !gatewayOn && resolveClaudeBool(config.UseBedrock, configpkg.DefaultClaudeUseBedrock)
	args = append(args, "--set-string", "claude.useMantle="+claudeFlagValue(useMantle))
	args = append(args, "--set-string", "claude.useBedrock="+claudeFlagValue(useBedrock))
	if models := configpkg.FormatClaudeModels(config.Models); models != "" {
		args = append(args, "--set-string", "claude.availableModels="+escapeHelmSetValue(models))
	}
	if config.MaxOutputTokens != nil {
		args = append(args, "--set-string", "claude.maxOutputTokens="+strconv.Itoa(*config.MaxOutputTokens))
	}
	// The gateway credential never passes through helm; applyGatewayCredentialsSecret
	// delivers it.
	if gateway := configpkg.EffectiveGateway(config, gateway); gateway.Configured() {
		args = append(args, "--set-string", "claude.openRouterBaseURL="+escapeHelmSetValue(strings.TrimSpace(gateway.BaseURL)))
		if ids := configpkg.FormatClaudeModels(gateway.ModelIDs()); ids != "" {
			args = append(args, "--set-string", "claude.openRouterAvailableModels="+escapeHelmSetValue(ids))
		}
		// Pod-wide default so Claude outside the AI tab uses a model the gateway serves.
		if model := gateway.ResolveDefaultModel(); model != "" {
			args = append(args, "--set-string", "claude.openRouterModel="+escapeHelmSetValue(model))
			if context := gateway.ContextFor(model); context > 0 {
				args = append(args, "--set-string", "claude.openRouterModelContext="+strconv.Itoa(context))
			}
		}
		args = append(args, "--set-string", "claude.openRouterAuthTokenSecret="+configpkg.GatewaySecretName)
		args = append(args, "--set-string", "claude.openRouterAuthTokenKey="+configpkg.GatewaySecretKey)
	}
	return args
}

func resolveClaudeBool(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

// claudeFlagValue returns the "1"/"0" form the chart and entrypoint expect.
func claudeFlagValue(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

// escapeHelmSetValue escapes backslashes and commas, which helm --set treats as
// structural.
func escapeHelmSetValue(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `,`, `\,`)
	return replacer.Replace(value)
}
