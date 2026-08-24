package cmd

import (
	"fmt"
	"strings"
	"time"

	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

func newListCmd(store common.ListStore, findProjectRoot common.ProjectFinderFunc) *cobra.Command {
	return &cobra.Command{
		Use:           "list",
		Short:         "List configured tenants and environments",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runListCommand(commandContext(cmd), store, findProjectRoot)
		},
	}
}

func runListCommand(ctx common.Context, store common.ListStore, findProjectRoot common.ProjectFinderFunc) error {
	ctx.TraceCommand("", "erun", "list")
	result, err := common.ResolveListResult(store, findProjectRoot, common.OpenParams{
		UseDefaultTenant:      true,
		UseDefaultEnvironment: true,
	})
	if err != nil {
		return err
	}
	return writeListResult(ctx, result)
}

func writeListResult(ctx common.Context, result common.ListResult) error {
	if err := writeListHeaderSections(ctx, result); err != nil {
		return err
	}
	if err := writeCloudProviders(ctx, result.CloudProviders); err != nil {
		return err
	}
	return writeListTenants(ctx, result.Tenants)
}

func writeListHeaderSections(ctx common.Context, result common.ListResult) error {
	if err := writeListConfigurationSection(ctx, result.ConfigDirectory); err != nil {
		return err
	}
	if err := writeListDefaultsSection(ctx, result.Defaults); err != nil {
		return err
	}
	return writeListCurrentDirectorySection(ctx, result.CurrentDirectory)
}

func writeListConfigurationSection(ctx common.Context, configDirectory string) error {
	if _, err := fmt.Fprintln(ctx.Stdout, "Configuration:"); err != nil {
		return err
	}
	return writeLabeledValue(ctx, "directory", valueOrNone(configDirectory))
}

func writeListDefaultsSection(ctx common.Context, defaults common.ListDefaultsResult) error {
	if _, err := fmt.Fprintln(ctx.Stdout, "Defaults:"); err != nil {
		return err
	}
	if err := writeLabeledValue(ctx, "tenant", valueOrNone(defaults.Tenant)); err != nil {
		return err
	}
	return writeLabeledValue(ctx, "environment", valueOrNone(defaults.Environment))
}

func writeListCurrentDirectorySection(ctx common.Context, current common.ListCurrentDirectoryResult) error {
	if _, err := fmt.Fprintln(ctx.Stdout, "Current Directory:"); err != nil {
		return err
	}
	if err := writeLabeledValue(ctx, "path", valueOrNone(current.Path)); err != nil {
		return err
	}
	if err := writeLabeledValue(ctx, "repo", valueOrNone(current.Repo)); err != nil {
		return err
	}
	if err := writeLabeledValue(ctx, "configured tenant", configuredCurrentTenantOrNone(current.ConfiguredTenant)); err != nil {
		return err
	}
	return writeEffectiveOpen(ctx, current)
}

func writeListTenants(ctx common.Context, tenants []common.ListTenantResult) error {
	if _, err := fmt.Fprintln(ctx.Stdout, "Tenants:"); err != nil {
		return err
	}
	if len(tenants) == 0 {
		_, err := fmt.Fprintln(ctx.Stdout, "  none")
		return err
	}

	for _, tenant := range tenants {
		if err := writeTenantEntry(ctx, tenant); err != nil {
			return err
		}
	}
	return nil
}

func writeEffectiveOpen(ctx common.Context, current common.ListCurrentDirectoryResult) error {
	if current.Effective == nil {
		if strings.TrimSpace(current.EffectiveError) != "" {
			return writeLabeledValue(ctx, "effective target", "unavailable ("+current.EffectiveError+")")
		}
		return writeLabeledValue(ctx, "effective target", "none")
	}
	if err := writeEffectiveOpenBase(ctx, *current.Effective); err != nil {
		return err
	}
	if current.Effective.SSH.Enabled {
		if err := writeEffectiveOpenSSH(ctx, current.Effective.SSH); err != nil {
			return err
		}
	}
	return writeLabeledValue(ctx, "repo path", current.Effective.RepoPath)
}

func writeEffectiveOpenBase(ctx common.Context, effective common.ListEffectiveTargetResult) error {
	if err := writeEffectiveTargetIdentity(ctx, effective); err != nil {
		return err
	}
	if err := writeEffectiveTargetType(ctx, effective); err != nil {
		return err
	}
	return writeEffectiveTargetPorts(ctx, effective)
}

func writeEffectiveTargetIdentity(ctx common.Context, effective common.ListEffectiveTargetResult) error {
	if err := writeLabeledValue(ctx, "effective target", effective.Tenant+"/"+effective.Environment); err != nil {
		return err
	}
	if err := writeLabeledValue(ctx, "kubernetes context", effective.KubernetesContext); err != nil {
		return err
	}
	if strings.TrimSpace(effective.CloudProviderAlias) != "" {
		if err := writeLabeledValue(ctx, "cloud provider", effective.CloudProviderAlias); err != nil {
			return err
		}
	}
	return nil
}

func writeEffectiveTargetType(ctx common.Context, effective common.ListEffectiveTargetResult) error {
	return writeLabeledValue(ctx, "type", valueOrNone(string(effective.Type)))
}

func writeEffectiveTargetPorts(ctx common.Context, effective common.ListEffectiveTargetResult) error {
	if err := writeLabeledValue(ctx, "assigned local port range", portRangeLabel(effective.LocalPorts)); err != nil {
		return err
	}
	if err := writeLabeledValue(ctx, "assigned mcp local port", fmt.Sprintf("%d (when MCP is running or forwarded)", effective.LocalPorts.MCP)); err != nil {
		return err
	}
	if err := writeLabeledValue(ctx, "assigned api local port", fmt.Sprintf("%d (when API is running or forwarded)", effective.LocalPorts.API)); err != nil {
		return err
	}
	if err := writeLabeledValue(ctx, "api url", effective.APIURL); err != nil {
		return err
	}
	if err := writeLabeledValue(ctx, "assigned ssh local port", fmt.Sprintf("%d (when SSH port-forward is active)", effective.LocalPorts.SSH)); err != nil {
		return err
	}
	return writeLabeledValue(ctx, "assigned contribute-app local port", fmt.Sprintf("%d (when contribute mode is active and `erun app --headless` is running)", effective.LocalPorts.ContributeApp))
}

func writeEffectiveOpenSSH(ctx common.Context, ssh common.ListSSHResult) error {
	if err := writeLabeledValue(ctx, "sshd", "on"); err != nil {
		return err
	}
	if err := writeLabeledValue(ctx, "ssh host", ssh.HostAlias); err != nil {
		return err
	}
	if err := writeLabeledValue(ctx, "ssh user", ssh.User); err != nil {
		return err
	}
	return writeLabeledValue(ctx, "ssh workspace", ssh.WorkspacePath)
}

func writeTenantEntry(ctx common.Context, tenant common.ListTenantResult) error {
	if _, err := fmt.Fprintln(ctx.Stdout, tenantHeaderLine(tenant)); err != nil {
		return err
	}
	if err := writeIndentedValue(ctx, 4, "default environment", tenant.DefaultEnvironment); err != nil {
		return err
	}
	if strings.TrimSpace(tenant.APIURL) != "" {
		if err := writeIndentedValue(ctx, 4, "api url", tenant.APIURL); err != nil {
			return err
		}
	}

	if len(tenant.Environments) == 0 {
		_, err := fmt.Fprintln(ctx.Stdout, "    environments: none")
		return err
	}
	if _, err := fmt.Fprintln(ctx.Stdout, "    environments:"); err != nil {
		return err
	}
	for _, env := range tenant.Environments {
		if err := writeEnvironmentEntry(ctx, env); err != nil {
			return err
		}
	}
	return nil
}

func writeEnvironmentEntry(ctx common.Context, env common.ListEnvironmentResult) error {
	if _, err := fmt.Fprintln(ctx.Stdout, environmentHeaderLine(env)); err != nil {
		return err
	}
	for _, line := range environmentDetailLines(env) {
		if _, err := fmt.Fprintln(ctx.Stdout, line); err != nil {
			return err
		}
	}
	return nil
}

func tenantHeaderLine(tenant common.ListTenantResult) string {
	header := "  " + tenant.Name
	if markers := statusMarkers(tenant.IsDefault, tenant.IsEffective); len(markers) > 0 {
		header += " [" + strings.Join(markers, ", ") + "]"
	}
	return header
}

func environmentHeaderLine(env common.ListEnvironmentResult) string {
	envLine := "      - " + env.Name
	if markers := statusMarkers(env.IsDefault, env.IsEffective); len(markers) > 0 {
		envLine += " [" + strings.Join(markers, ", ") + "]"
	}
	envLine += " context=" + quotedValueOrNone(env.KubernetesContext)
	if strings.TrimSpace(env.CloudProviderAlias) != "" {
		envLine += " cloud=" + quotedValueOrNone(env.CloudProviderAlias)
	}
	return envLine
}

func environmentDetailLines(env common.ListEnvironmentResult) []string {
	const indent = "          "
	lines := []string{
		indent + "type: " + valueOrNone(string(env.Type)),
		indent + "repo: " + valueOrNone(env.RepoPath),
		indent + "ports: " + portRangeLabel(env.LocalPorts),
		indent + "mcp-port: " + fmt.Sprintf("%d", env.LocalPorts.MCP),
		indent + "api-port: " + fmt.Sprintf("%d", env.LocalPorts.API),
		indent + "api-url: " + valueOrNone(env.APIURL),
		indent + "ssh-port: " + fmt.Sprintf("%d", env.LocalPorts.SSH),
		indent + "contribute-app-port: " + fmt.Sprintf("%d", env.LocalPorts.ContributeApp),
		indent + "container-registries: " + containerRegistriesLabel(env.ContainerRegistries),
		indent + "runtime-version: " + valueOrNone(env.RuntimeVersion),
		indent + "runtime-pod: " + runtimePodLabel(env.RuntimePod),
	}
	lines = append(lines, runtimeSizingLines(env.Sizing, indent)...)
	lines = append(lines, []string{
		indent + "managed-cloud: " + enabledDisabledLabel(env.ManagedCloud),
		indent + "ai-tool: " + valueOrNone(env.AITool),
		indent + "claude: " + claudeLabel(env.Claude),
		indent + "idle: " + idleLabel(env.Idle),
	}...)
	if env.DisableBuildScript {
		lines = append(lines, indent+"disable-build-script: enabled")
	}
	if env.PlatformAccount {
		lines = append(lines, indent+"platform-account: enabled")
	}
	if env.SSH.Enabled {
		lines = append(lines, environmentSSHDetailLines(env.SSH, indent)...)
	} else {
		lines = append(lines, indent+"sshd: off")
	}
	return lines
}

func environmentSSHDetailLines(ssh common.ListSSHResult, indent string) []string {
	return []string{
		indent + "sshd: on",
		indent + "ssh-host: " + valueOrNone(ssh.HostAlias),
		indent + "ssh-user: " + valueOrNone(ssh.User),
		indent + "ssh-local-port: " + fmt.Sprintf("%d", ssh.LocalPort),
		indent + "ssh-workspace: " + valueOrNone(ssh.WorkspacePath),
		indent + "ssh-public-key-path: " + valueOrNone(ssh.PublicKeyPath),
		indent + "ssh-workspace-sync: " + enabledDisabledLabel(ssh.WorkspaceSyncEnabled),
		indent + "ssh-workspace-sync-local-path: " + valueOrNone(ssh.WorkspaceSyncLocalPath),
	}
}

// runtimeSizingLines render the standing recommendation under `runtime-pod:`,
// which is the setting an operator would change to act on it. Two lines: the
// verdicts, then the evidence they rest on. The evidence line is not optional
// detail — a recommendation whose window and counters are invisible cannot be
// argued with, and this one has to be argued with before anyone shrinks an
// environment.
func runtimeSizingLines(sizing *common.RuntimeSizingRecommendation, indent string) []string {
	if sizing == nil {
		return nil
	}
	verdicts := make([]string, 0, len(sizing.Verdicts))
	for _, verdict := range sizing.Verdicts {
		verdicts = append(verdicts, runtimeSizingVerdictLabel(verdict))
	}
	evidence := sizing.Evidence
	return []string{
		indent + "sizing: " + strings.Join(verdicts, "; "),
		indent + fmt.Sprintf("sizing-evidence: %s observed, %d samples, %d restarts, knob=%s, from %s (not loadavg)",
			common.FormatObservedWindow(time.Duration(evidence.ObservedSeconds)*time.Second),
			evidence.Samples, evidence.Restarts, sizing.Knob, strings.Join(evidence.Signals, ", ")),
	}
}

func runtimeSizingVerdictLabel(verdict common.RuntimeSizingVerdict) string {
	label := verdict.Resource + " " + string(verdict.Action)
	if suggested := strings.TrimSpace(verdict.Suggested); suggested != "" {
		label += " to " + suggested
	}
	if current := strings.TrimSpace(verdict.Current); current != "" {
		label += " from " + current
	}
	label += " (" + verdict.Reason
	if verdict.Confidence != "" {
		label += ", " + string(verdict.Confidence) + " confidence"
	}
	return label + ")"
}

func runtimePodLabel(pod common.RuntimePodResources) string {
	cpu := strings.TrimSpace(pod.CPU)
	memory := strings.TrimSpace(pod.Memory)
	if cpu == "" && memory == "" {
		return "none"
	}
	return fmt.Sprintf("cpu=%s memory=%s", valueOrNone(cpu), valueOrNone(memory))
}

func claudeLabel(c common.EnvironmentClaudeConfig) string {
	if c.IsZero() {
		return "none"
	}
	parts := make([]string, 0, 4)
	parts = append(parts, "use-mantle="+optionalBoolLabel(c.UseMantle))
	parts = append(parts, "use-bedrock="+optionalBoolLabel(c.UseBedrock))
	models := "none"
	if len(c.Models) > 0 {
		models = strings.Join(c.Models, ",")
	}
	parts = append(parts, "models="+models)
	tokens := "unset"
	if c.MaxOutputTokens != nil {
		tokens = fmt.Sprintf("%d", *c.MaxOutputTokens)
	}
	parts = append(parts, "max-output-tokens="+tokens)
	return strings.Join(parts, " ")
}

func idleLabel(idle common.EnvironmentIdleConfig) string {
	timeout := strings.TrimSpace(idle.Timeout)
	hours := strings.TrimSpace(idle.WorkingHours)
	tz := strings.TrimSpace(idle.Timezone)
	if timeout == "" && hours == "" && tz == "" && idle.IdleTrafficBytes == 0 {
		return "none"
	}
	return fmt.Sprintf("timeout=%s working-hours=%s timezone=%s idle-traffic-bytes=%d",
		valueOrNone(timeout), valueOrNone(hours), valueOrNone(tz), idle.IdleTrafficBytes)
}

func optionalBoolLabel(b *bool) string {
	if b == nil {
		return "unset"
	}
	if *b {
		return "true"
	}
	return "false"
}

func statusMarkers(isDefault, isEffective bool) []string {
	markers := make([]string, 0, 2)
	if isDefault {
		markers = append(markers, "default")
	}
	if isEffective {
		markers = append(markers, "effective")
	}
	return markers
}

func writeCloudProviders(ctx common.Context, providers []common.CloudProviderStatus) error {
	if _, err := fmt.Fprintln(ctx.Stdout, "Cloud Providers:"); err != nil {
		return err
	}
	if len(providers) == 0 {
		_, err := fmt.Fprintln(ctx.Stdout, "  none")
		return err
	}
	for _, provider := range providers {
		line := "  - " + provider.Alias
		line += " provider=" + quotedValueOrNone(provider.Provider)
		line += " account=" + quotedValueOrNone(provider.AccountID)
		line += " status=" + quotedValueOrNone(provider.Status)
		if strings.TrimSpace(provider.Message) != "" {
			line += " message=" + quotedValueOrNone(provider.Message)
		}
		if _, err := fmt.Fprintln(ctx.Stdout, line); err != nil {
			return err
		}
	}
	return nil
}

func writeLabeledValue(ctx common.Context, label, value string) error {
	return writeIndentedValue(ctx, 2, label, value)
}

func writeIndentedValue(ctx common.Context, indent int, label, value string) error {
	if strings.TrimSpace(value) == "" {
		value = "none"
	}
	_, err := fmt.Fprintf(ctx.Stdout, "%s%s: %s\n", strings.Repeat(" ", indent), label, value)
	return err
}

func valueOrNone(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "none"
	}
	return value
}

// containerRegistriesLabel renders the marked registry list as
// "<registry>[role,role] <registry>[role]", or "none" when unset.
func containerRegistriesLabel(registries common.ContainerRegistries) string {
	if registries.IsZero() {
		return "none"
	}
	entries := make([]string, 0, len(registries))
	for _, entry := range registries {
		roles := make([]string, 0, len(entry.Roles))
		for _, role := range entry.Roles {
			roles = append(roles, string(role))
		}
		entries = append(entries, entry.Registry+"["+strings.Join(roles, ",")+"]")
	}
	return strings.Join(entries, " ")
}

func quotedValueOrNone(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "none"
	}
	return value
}

func configuredCurrentTenantOrNone(tenant string) string {
	if strings.TrimSpace(tenant) == "" {
		return "none"
	}
	return tenant
}

func enabledDisabledLabel(enabled bool) string {
	if enabled {
		return "on"
	}
	return "off"
}

func portRangeLabel(ports common.EnvironmentLocalPorts) string {
	if ports.RangeStart <= 0 || ports.RangeEnd <= 0 {
		return "none"
	}
	return fmt.Sprintf("%d-%d", ports.RangeStart, ports.RangeEnd)
}
