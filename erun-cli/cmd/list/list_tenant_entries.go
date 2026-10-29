package list

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/cmd/shared/textfmt"
	"github.com/sophium/erun/cmd/shared/usageview"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/config/runtimetarget"
	"github.com/sophium/erun/erun-common/base/config/sshconfig"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/lifecycle/list"
)

func writeTenantEntry(ctx core.Context, tenant list.ListTenantResult) error {
	if _, err := fmt.Fprintln(ctx.Stdout, tenantHeaderLine(tenant)); err != nil {
		return err
	}
	if err := textfmt.WriteIndentedValue(ctx, 4, "default environment", tenant.DefaultEnvironment); err != nil {
		return err
	}
	if strings.TrimSpace(tenant.APIURL) != "" {
		if err := textfmt.WriteIndentedValue(ctx, 4, "api url", tenant.APIURL); err != nil {
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
		if err := writeEnvironmentEntry(ctx, tenant.Name, env); err != nil {
			return err
		}
	}
	return nil
}

func writeEnvironmentEntry(ctx core.Context, tenantName string, env list.ListEnvironmentResult) error {
	if _, err := fmt.Fprintln(ctx.Stdout, environmentHeaderLine(env)); err != nil {
		return err
	}
	for _, line := range environmentDetailLines(tenantName, env) {
		if _, err := fmt.Fprintln(ctx.Stdout, line); err != nil {
			return err
		}
	}
	return nil
}

func tenantHeaderLine(tenant list.ListTenantResult) string {
	header := "  " + tenant.Name
	if markers := statusMarkers(tenant.IsDefault, tenant.IsEffective); len(markers) > 0 {
		header += " [" + strings.Join(markers, ", ") + "]"
	}
	return header
}

func environmentHeaderLine(env list.ListEnvironmentResult) string {
	envLine := "      - " + env.Name
	if markers := statusMarkers(env.IsDefault, env.IsEffective); len(markers) > 0 {
		envLine += " [" + strings.Join(markers, ", ") + "]"
	}
	envLine += " context=" + textfmt.QuotedValueOrNone(env.KubernetesContext)
	if strings.TrimSpace(env.CloudProviderAlias) != "" {
		envLine += " cloud=" + textfmt.QuotedValueOrNone(env.CloudProviderAlias)
	}
	return envLine
}

func environmentDetailLines(tenantName string, env list.ListEnvironmentResult) []string {
	const indent = "          "
	lines := []string{
		indent + "type: " + textfmt.ValueOrNone(string(env.Type)),
		indent + "repo: " + textfmt.ValueOrNone(env.RepoPath),
		indent + "ports: " + portRangeLabel(env.LocalPorts),
		indent + "mcp-port: " + fmt.Sprintf("%d", env.LocalPorts.MCP),
		indent + "api-port: " + fmt.Sprintf("%d", env.LocalPorts.API),
		indent + "api-url: " + textfmt.ValueOrNone(env.APIURL),
		indent + "ssh-port: " + fmt.Sprintf("%d", env.LocalPorts.SSH),
		indent + "contribute-app-port: " + fmt.Sprintf("%d", env.LocalPorts.ContributeApp),
		indent + "container-registries: " + containerRegistriesLabel(env.ContainerRegistries),
		indent + "runtime-version: " + runtimeVersionLabel(tenantName, env),
		indent + "runtime-pod: " + runtimePodLabel(env.RuntimePod),
	}
	lines = append(lines, usageview.RuntimeSizingLines(env.Sizing, indent)...)
	lines = append(lines, []string{
		indent + "managed-cloud: " + textfmt.EnabledDisabledLabel(env.ManagedCloud),
		indent + "hosted: " + hostedEnvironmentLabel(env.Hosted),
		indent + "ai-tool: " + textfmt.ValueOrNone(env.AITool),
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
		lines = append(lines, environmentSSHDetailLines(tenantName, env.Name, env.SSH, indent)...)
	} else {
		lines = append(lines, indent+"sshd: off")
	}
	return lines
}

func environmentSSHDetailLines(tenantName, environmentName string, ssh model.ListSSHResult, indent string) []string {
	return []string{
		indent + "sshd: on",
		indent + "ssh-host: " + sshHostAliasLabel(tenantName, environmentName, ssh.HostAlias),
		indent + "ssh-user: " + textfmt.ValueOrNone(ssh.User),
		indent + "ssh-local-port: " + fmt.Sprintf("%d", ssh.LocalPort),
		indent + "ssh-workspace: " + textfmt.ValueOrNone(ssh.WorkspacePath),
		indent + "ssh-public-key-path: " + textfmt.ValueOrNone(ssh.PublicKeyPath),
		indent + "ssh-workspace-sync: " + textfmt.EnabledDisabledLabel(ssh.WorkspaceSyncEnabled),
		indent + "ssh-workspace-sync-local-path: " + textfmt.ValueOrNone(ssh.WorkspaceSyncLocalPath),
	}
}

// sshHostAliasLabel adds the fix when ~/.ssh/config has no matching Host block,
// since the alias is derived by convention alone.
func sshHostAliasLabel(tenantName, environmentName, alias string) string {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return textfmt.ValueOrNone(alias)
	}
	configured, err := sshconfig.DefaultSSHConfigHasAlias(alias)
	if err != nil || configured {
		return alias
	}
	return fmt.Sprintf("%s (not in ~/.ssh/config — run `erun sshd init %s %s` to fix)", alias, tenantName, environmentName)
}

// runtimeVersionLabel names the release line beside the version, since envs in one
// tenant can ride different lines.
func runtimeVersionLabel(tenantName string, env list.ListEnvironmentResult) string {
	version := textfmt.ValueOrNone(env.RuntimeVersion)
	if env.RuntimeVersionLine == nil {
		return version
	}
	line := *env.RuntimeVersionLine
	if line.Undetermined {
		return version + " (line undetermined — no resolved runtime image recorded; redeploy to record it)"
	}
	detail := line.Line + " line, " + line.Image
	if line.Disagrees {
		detail += " — release name " + runtimetarget.RuntimeReleaseName(tenantName) + " disagrees with the image"
	}
	return version + " (" + detail + ")"
}

func runtimePodLabel(pod model.RuntimePodResources) string {
	cpu := strings.TrimSpace(pod.CPU)
	memory := strings.TrimSpace(pod.Memory)
	if cpu == "" && memory == "" {
		return "none"
	}
	return fmt.Sprintf("cpu=%s memory=%s", textfmt.ValueOrNone(cpu), textfmt.ValueOrNone(memory))
}

func claudeLabel(c config.EnvironmentClaudeConfig) string {
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

func idleLabel(idle config.EnvironmentIdleConfig) string {
	timeout := strings.TrimSpace(idle.Timeout)
	hours := strings.TrimSpace(idle.WorkingHours)
	tz := strings.TrimSpace(idle.Timezone)
	if timeout == "" && hours == "" && tz == "" && idle.IdleTrafficBytes == 0 {
		return "none"
	}
	return fmt.Sprintf("timeout=%s working-hours=%s timezone=%s idle-traffic-bytes=%d",
		textfmt.ValueOrNone(timeout), textfmt.ValueOrNone(hours), textfmt.ValueOrNone(tz), idle.IdleTrafficBytes)
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

func writeCloudProviders(ctx core.Context, providers []model.CloudProviderStatus) error {
	if _, err := fmt.Fprintln(ctx.Stdout, "Cloud Providers:"); err != nil {
		return err
	}
	if len(providers) == 0 {
		_, err := fmt.Fprintln(ctx.Stdout, "  none")
		return err
	}
	for _, provider := range providers {
		line := "  - " + provider.Alias
		line += " provider=" + textfmt.QuotedValueOrNone(provider.Provider)
		line += " account=" + textfmt.QuotedValueOrNone(provider.AccountID)
		line += " status=" + textfmt.QuotedValueOrNone(provider.Status)
		if strings.TrimSpace(provider.Message) != "" {
			line += " message=" + textfmt.QuotedValueOrNone(provider.Message)
		}
		if _, err := fmt.Fprintln(ctx.Stdout, line); err != nil {
			return err
		}
	}
	return nil
}

func containerRegistriesLabel(registries config.ContainerRegistries) string {
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

func configuredCurrentTenantOrNone(tenant string) string {
	if strings.TrimSpace(tenant) == "" {
		return "none"
	}
	return tenant
}

// hostedEnvironmentLabel shows hosted (definition) beside managed-cloud (lifecycle).
func hostedEnvironmentLabel(hosted *config.HostedEnvironment) string {
	if hosted == nil {
		return "no"
	}
	return hosted.Describe()
}

func portRangeLabel(ports model.EnvironmentLocalPorts) string {
	if ports.RangeStart <= 0 || ports.RangeEnd <= 0 {
		return "none"
	}
	return fmt.Sprintf("%d-%d", ports.RangeStart, ports.RangeEnd)
}
