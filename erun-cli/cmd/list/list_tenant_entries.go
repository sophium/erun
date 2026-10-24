package list

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/cmd/shared"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/command/lifecycle"
	"github.com/sophium/erun/erun-common/infra/cloud"
)

func writeTenantEntry(ctx core.Context, tenant lifecycle.ListTenantResult) error {
	if _, err := fmt.Fprintln(ctx.Stdout, tenantHeaderLine(tenant)); err != nil {
		return err
	}
	if err := shared.WriteIndentedValue(ctx, 4, "default environment", tenant.DefaultEnvironment); err != nil {
		return err
	}
	if strings.TrimSpace(tenant.APIURL) != "" {
		if err := shared.WriteIndentedValue(ctx, 4, "api url", tenant.APIURL); err != nil {
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

func writeEnvironmentEntry(ctx core.Context, tenantName string, env lifecycle.ListEnvironmentResult) error {
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

func tenantHeaderLine(tenant lifecycle.ListTenantResult) string {
	header := "  " + tenant.Name
	if markers := statusMarkers(tenant.IsDefault, tenant.IsEffective); len(markers) > 0 {
		header += " [" + strings.Join(markers, ", ") + "]"
	}
	return header
}

func environmentHeaderLine(env lifecycle.ListEnvironmentResult) string {
	envLine := "      - " + env.Name
	if markers := statusMarkers(env.IsDefault, env.IsEffective); len(markers) > 0 {
		envLine += " [" + strings.Join(markers, ", ") + "]"
	}
	envLine += " context=" + shared.QuotedValueOrNone(env.KubernetesContext)
	if strings.TrimSpace(env.CloudProviderAlias) != "" {
		envLine += " cloud=" + shared.QuotedValueOrNone(env.CloudProviderAlias)
	}
	return envLine
}

func environmentDetailLines(tenantName string, env lifecycle.ListEnvironmentResult) []string {
	const indent = "          "
	lines := []string{
		indent + "type: " + shared.ValueOrNone(string(env.Type)),
		indent + "repo: " + shared.ValueOrNone(env.RepoPath),
		indent + "ports: " + portRangeLabel(env.LocalPorts),
		indent + "mcp-port: " + fmt.Sprintf("%d", env.LocalPorts.MCP),
		indent + "api-port: " + fmt.Sprintf("%d", env.LocalPorts.API),
		indent + "api-url: " + shared.ValueOrNone(env.APIURL),
		indent + "ssh-port: " + fmt.Sprintf("%d", env.LocalPorts.SSH),
		indent + "contribute-app-port: " + fmt.Sprintf("%d", env.LocalPorts.ContributeApp),
		indent + "container-registries: " + containerRegistriesLabel(env.ContainerRegistries),
		indent + "runtime-version: " + runtimeVersionLabel(tenantName, env),
		indent + "runtime-pod: " + runtimePodLabel(env.RuntimePod),
	}
	lines = append(lines, shared.RuntimeSizingLines(env.Sizing, indent)...)
	lines = append(lines, []string{
		indent + "managed-cloud: " + shared.EnabledDisabledLabel(env.ManagedCloud),
		indent + "hosted: " + hostedEnvironmentLabel(env.Hosted),
		indent + "ai-tool: " + shared.ValueOrNone(env.AITool),
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

func environmentSSHDetailLines(tenantName, environmentName string, ssh lifecycle.ListSSHResult, indent string) []string {
	return []string{
		indent + "sshd: on",
		indent + "ssh-host: " + sshHostAliasLabel(tenantName, environmentName, ssh.HostAlias),
		indent + "ssh-user: " + shared.ValueOrNone(ssh.User),
		indent + "ssh-local-port: " + fmt.Sprintf("%d", ssh.LocalPort),
		indent + "ssh-workspace: " + shared.ValueOrNone(ssh.WorkspacePath),
		indent + "ssh-public-key-path: " + shared.ValueOrNone(ssh.PublicKeyPath),
		indent + "ssh-workspace-sync: " + shared.EnabledDisabledLabel(ssh.WorkspaceSyncEnabled),
		indent + "ssh-workspace-sync-local-path: " + shared.ValueOrNone(ssh.WorkspaceSyncLocalPath),
	}
}

// sshHostAliasLabel reports the alias `erun list` derives for an env, plus the
// fix when the alias is only a naming convention: SSHHostAlias is computed
// from tenant/environment alone, so it prints the same whether or not
// ~/.ssh/config actually has a matching Host block. Reporting it bare reads as
// "this alias works" when nothing wrote the block or authorized a key for it.
// A check that cannot run (home dir unresolvable) reports the alias bare
// rather than a false alarm.
func sshHostAliasLabel(tenantName, environmentName, alias string) string {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return shared.ValueOrNone(alias)
	}
	configured, err := config.DefaultSSHConfigHasAlias(alias)
	if err != nil || configured {
		return alias
	}
	return fmt.Sprintf("%s (not in ~/.ssh/config — run `erun sshd init %s %s` to fix)", alias, tenantName, environmentName)
}

// runtimeVersionLabel names the release line beside the bare runtime-version
// number: a number alone reads as an erun version even when it rides a tenant's
// own devops line, and two environments in the same tenant can ride different
// lines from each other. An environment that has never deployed still reads
// "none", unchanged; one that has deployed but recorded no resolved image reads
// distinctly as "line undetermined" rather than guessing a line from the tenant
// name.
func runtimeVersionLabel(tenantName string, env lifecycle.ListEnvironmentResult) string {
	version := shared.ValueOrNone(env.RuntimeVersion)
	if env.RuntimeVersionLine == nil {
		return version
	}
	line := *env.RuntimeVersionLine
	if line.Undetermined {
		return version + " (line undetermined — no resolved runtime image recorded; redeploy to record it)"
	}
	detail := line.Line + " line, " + line.Image
	if line.Disagrees {
		detail += " — release name " + config.RuntimeReleaseName(tenantName) + " disagrees with the image"
	}
	return version + " (" + detail + ")"
}

func runtimePodLabel(pod config.RuntimePodResources) string {
	cpu := strings.TrimSpace(pod.CPU)
	memory := strings.TrimSpace(pod.Memory)
	if cpu == "" && memory == "" {
		return "none"
	}
	return fmt.Sprintf("cpu=%s memory=%s", shared.ValueOrNone(cpu), shared.ValueOrNone(memory))
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
		shared.ValueOrNone(timeout), shared.ValueOrNone(hours), shared.ValueOrNone(tz), idle.IdleTrafficBytes)
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

func writeCloudProviders(ctx core.Context, providers []cloud.CloudProviderStatus) error {
	if _, err := fmt.Fprintln(ctx.Stdout, "Cloud Providers:"); err != nil {
		return err
	}
	if len(providers) == 0 {
		_, err := fmt.Fprintln(ctx.Stdout, "  none")
		return err
	}
	for _, provider := range providers {
		line := "  - " + provider.Alias
		line += " provider=" + shared.QuotedValueOrNone(provider.Provider)
		line += " account=" + shared.QuotedValueOrNone(provider.AccountID)
		line += " status=" + shared.QuotedValueOrNone(provider.Status)
		if strings.TrimSpace(provider.Message) != "" {
			line += " message=" + shared.QuotedValueOrNone(provider.Message)
		}
		if _, err := fmt.Fprintln(ctx.Stdout, line); err != nil {
			return err
		}
	}
	return nil
}

// containerRegistriesLabel renders the marked registry list as
// "<registry>[role,role] <registry>[role]", or "none" when unset.
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

// hostedEnvironmentLabel renders the hosted marker beside managed-cloud, which
// is where an operator already looks to learn whether the platform has any
// relationship with this environment. The two answer different questions --
// managed-cloud is lifecycle, hosted is definition -- so both are shown rather
// than one standing in for the other.
func hostedEnvironmentLabel(hosted *config.HostedEnvironment) string {
	if hosted == nil {
		return "no"
	}
	return hosted.Describe()
}

func portRangeLabel(ports config.EnvironmentLocalPorts) string {
	if ports.RangeStart <= 0 || ports.RangeEnd <= 0 {
		return "none"
	}
	return fmt.Sprintf("%d-%d", ports.RangeStart, ports.RangeEnd)
}
