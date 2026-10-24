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

// helmCloudContextSetArgs projects the env's resolved cloud context. Every value
// but the region is always set, so a projection reconciles back to empty when an
// env loses its cloud context. The region is the exception: the chart turns it
// into AWS_REGION, and an explicitly empty AWS_REGION overrides — rather than
// falls back to — the region the pod's own AWS profile carries, so "no region
// resolved" has to mean "no value threaded at all".
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

// helmRegistrySetArgs returns the registry-projection helm --set args, each
// guarded on presence so an env that carries none renders nothing and an old
// chart with no .Values for them is unaffected.
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

// helmPlatformAccountSetArgs appends the platform-account switch only when the
// env opted in, so every other deploy plan stays byte-for-byte unchanged. When
// set, the runtime chart binds the env's SA to cluster-admin (a
// <release>-platform ClusterRoleBinding) so in-pod platform terraform and
// component installs can manage cluster-scoped resources. Absent on a later
// deploy, the chart default (false) prunes the binding on upgrade, so a flip in
// either direction reconciles.
func helmPlatformAccountSetArgs(platformAccount bool) []string {
	if !platformAccount {
		return nil
	}
	return []string{"--set", "platformAccount=true"}
}

// helmStoppedSetArgs renders the operator's stop so the upgrade reconciles it
// instead of quietly restarting the pod. Emitted only when the env is stopped,
// so every running env's plan is byte-for-byte unchanged — and an absent value
// renders replicas: 1, so a wake reconciles in the other direction just as
// declaratively.
func helmStoppedSetArgs(stopped bool) []string {
	if !stopped {
		return nil
	}
	return []string{"--set", "stopped=true"}
}

// helmPlatformSetArgs returns the per-instance platform.* helm --set args,
// guarded on presence so non-platform deploys (every existing env) render none.
// Threaded to every chart; only the platform singletons read them — PowerDNS to
// bootstrap its services zone, Zitadel to resolve its auth host, erun-backend-api
// to serve them at the unauthenticated GET /v1/platform discovery endpoint.
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

// helmImagePullSecretSetArgs renders the runtime pod's imagePullSecrets as
// indexed helm --set keys, skipping blank names. Empty input yields no args, so
// a public-image env passes nothing and its deploy is byte-for-byte unchanged.
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

// helmRegistryCredentialSecretSetArgs renders registryCredentialSecretName as
// a single helm --set key, empty input yielding no args so an env init never
// provisioned a credential for is byte-for-byte unchanged.
func helmRegistryCredentialSecretSetArgs(name string) []string {
	if name = strings.TrimSpace(name); name == "" {
		return nil
	}
	return []string{"--set-string", "registryCredentialSecretName=" + name}
}

// helmPlatformAliasSecretSetArgs renders platformAliasSecretName as a single
// helm --set key, empty input yielding no args so an env init found no host
// alias for is byte-for-byte unchanged.
func helmPlatformAliasSecretSetArgs(name string) []string {
	if name = strings.TrimSpace(name); name == "" {
		return nil
	}
	return []string{"--set-string", "platformAliasSecretName=" + name}
}

func helmClaudeSetArgs(config configpkg.EnvironmentClaudeConfig, gateway *configpkg.OpenRouterConfig) []string {
	args := make([]string, 0, 12)
	// A gateway replaces the model provider, so it renders the provider flags
	// off: Claude Code reads CLAUDE_CODE_USE_BEDROCK / _MANTLE ahead of
	// ANTHROPIC_BASE_URL, and leaving either on would route the session past the
	// gateway the environment selected.
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
	// The gateway is erun-level, so these render for every environment that
	// selects from the catalog; one that opted out renders none of them. Only
	// the Secret's name travels here, and that name is a fixed constant rather
	// than an operator entry — the credential is one erun-level value, delivered
	// into this namespace by applyGatewayCredentialsSecret, so there is nothing
	// per-environment to name. The value itself never passes through helm.
	if gateway := configpkg.EffectiveGateway(config, gateway); gateway.Configured() {
		args = append(args, "--set-string", "claude.openRouterBaseURL="+escapeHelmSetValue(strings.TrimSpace(gateway.BaseURL)))
		if ids := configpkg.FormatClaudeModels(gateway.ModelIDs()); ids != "" {
			args = append(args, "--set-string", "claude.openRouterAvailableModels="+escapeHelmSetValue(ids))
		}
		// The pod-wide default, so Claude invoked outside the AI tab (an exec
		// agent job) does not fall back to a model the gateway may not serve.
		if model := gateway.ResolveDefaultModel(); model != "" {
			args = append(args, "--set-string", "claude.openRouterModel="+escapeHelmSetValue(model))
			if context := gateway.ContextFor(model); context > 0 {
				args = append(args, "--set-string", "claude.openRouterModelContext="+strconv.Itoa(context))
			}
		}
		// Both names are erun's own constants, not operator entries: erun creates
		// this Secret from the erun-level credential, so there is nothing here
		// to supply and nothing to get out of step with what deploy creates.
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

// claudeFlagValue returns the "1"/"0" form expected by the chart template and
// the entrypoint script, distinct from formatHelmBool's "true"/"false" form.
func claudeFlagValue(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

// escapeHelmSetValue escapes characters that Helm's --set/--set-string parser
// treats as structural so the input is preserved as a literal scalar value.
// Helm splits values on commas, so a comma inside the value must be backslash-escaped.
func escapeHelmSetValue(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `,`, `\,`)
	return replacer.Replace(value)
}
