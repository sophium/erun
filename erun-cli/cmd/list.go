package cmd

import (
	"fmt"
	"strings"

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
	if err := writeLabeledValue(ctx, "snapshot", enabledDisabledLabel(effective.Snapshot)); err != nil {
		return err
	}
	if err := writeLabeledValue(ctx, "assigned local port range", portRangeLabel(effective.LocalPorts)); err != nil {
		return err
	}
	if err := writeLabeledValue(ctx, "assigned mcp local port", fmt.Sprintf("%d (when MCP is running or forwarded)", effective.LocalPorts.MCP)); err != nil {
		return err
	}
	return writeLabeledValue(ctx, "assigned ssh local port", fmt.Sprintf("%d (when SSH port-forward is active)", effective.LocalPorts.SSH))
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

	if len(tenant.Environments) == 0 {
		_, err := fmt.Fprintln(ctx.Stdout, "    environments: none")
		return err
	}
	if _, err := fmt.Fprintln(ctx.Stdout, "    environments:"); err != nil {
		return err
	}
	for _, env := range tenant.Environments {
		if _, err := fmt.Fprintln(ctx.Stdout, environmentLine(env)); err != nil {
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

func environmentLine(env common.ListEnvironmentResult) string {
	envLine := "      - " + env.Name
	if markers := statusMarkers(env.IsDefault, env.IsEffective); len(markers) > 0 {
		envLine += " [" + strings.Join(markers, ", ") + "]"
	}
	envLine += " context=" + quotedValueOrNone(env.KubernetesContext)
	if strings.TrimSpace(env.CloudProviderAlias) != "" {
		envLine += " cloud=" + quotedValueOrNone(env.CloudProviderAlias)
	}
	envLine += environmentBaseFields(env)
	if env.SSH.Enabled {
		envLine += environmentSSHFields(env.SSH)
	}
	return envLine
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

func environmentBaseFields(env common.ListEnvironmentResult) string {
	line := " snapshot=" + enabledDisabledLabel(env.Snapshot)
	line += " repo=" + quotedValueOrNone(env.RepoPath)
	line += " ports=" + portRangeLabel(env.LocalPorts)
	line += " mcp-port=" + fmt.Sprintf("%d", env.LocalPorts.MCP)
	line += " ssh-port=" + fmt.Sprintf("%d", env.LocalPorts.SSH)
	return line
}

func environmentSSHFields(ssh common.ListSSHResult) string {
	line := " ssh=on"
	line += " host=" + quotedValueOrNone(ssh.HostAlias)
	line += " user=" + quotedValueOrNone(ssh.User)
	line += " local-port=" + fmt.Sprintf("%d", ssh.LocalPort)
	line += " workspace=" + quotedValueOrNone(ssh.WorkspacePath)
	return line
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
