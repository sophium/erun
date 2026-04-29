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
	if _, err := fmt.Fprintln(ctx.Stdout, "Configuration:"); err != nil {
		return err
	}
	if err := writeLabeledValue(ctx, "directory", valueOrNone(result.ConfigDirectory)); err != nil {
		return err
	}

	if _, err := fmt.Fprintln(ctx.Stdout, "Defaults:"); err != nil {
		return err
	}
	if err := writeLabeledValue(ctx, "tenant", valueOrNone(result.Defaults.Tenant)); err != nil {
		return err
	}
	if err := writeLabeledValue(ctx, "environment", valueOrNone(result.Defaults.Environment)); err != nil {
		return err
	}

	if _, err := fmt.Fprintln(ctx.Stdout, "Current Directory:"); err != nil {
		return err
	}
	if err := writeLabeledValue(ctx, "path", valueOrNone(result.CurrentDirectory.Path)); err != nil {
		return err
	}
	if err := writeLabeledValue(ctx, "repo", valueOrNone(result.CurrentDirectory.Repo)); err != nil {
		return err
	}
	if err := writeLabeledValue(ctx, "configured tenant", configuredCurrentTenantOrNone(result.CurrentDirectory.ConfiguredTenant)); err != nil {
		return err
	}
	if err := writeEffectiveOpen(ctx, result.CurrentDirectory); err != nil {
		return err
	}
	return nil
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
	header := "  " + tenant.Name
	tenantMarkers := make([]string, 0, 2)
	if tenant.IsDefault {
		tenantMarkers = append(tenantMarkers, "default")
	}
	if tenant.IsEffective {
		tenantMarkers = append(tenantMarkers, "effective")
	}
	if len(tenantMarkers) > 0 {
		header += " [" + strings.Join(tenantMarkers, ", ") + "]"
	}
	if _, err := fmt.Fprintln(ctx.Stdout, header); err != nil {
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
		envLine := "      - " + env.Name
		envMarkers := make([]string, 0, 2)
		if env.IsDefault {
			envMarkers = append(envMarkers, "default")
		}
		if env.IsEffective {
			envMarkers = append(envMarkers, "effective")
		}
		if len(envMarkers) > 0 {
			envLine += " [" + strings.Join(envMarkers, ", ") + "]"
		}
		envLine += " context=" + quotedValueOrNone(env.KubernetesContext)
		if strings.TrimSpace(env.CloudProviderAlias) != "" {
			envLine += " cloud=" + quotedValueOrNone(env.CloudProviderAlias)
		}
		envLine += " snapshot=" + enabledDisabledLabel(env.Snapshot)
		envLine += " repo=" + quotedValueOrNone(env.RepoPath)
		envLine += " ports=" + portRangeLabel(env.LocalPorts)
		envLine += " mcp-port=" + fmt.Sprintf("%d", env.LocalPorts.MCP)
		envLine += " ssh-port=" + fmt.Sprintf("%d", env.LocalPorts.SSH)
		if env.SSH.Enabled {
			envLine += " ssh=on"
			envLine += " host=" + quotedValueOrNone(env.SSH.HostAlias)
			envLine += " user=" + quotedValueOrNone(env.SSH.User)
			envLine += " local-port=" + fmt.Sprintf("%d", env.SSH.LocalPort)
			envLine += " workspace=" + quotedValueOrNone(env.SSH.WorkspacePath)
		}
		if _, err := fmt.Fprintln(ctx.Stdout, envLine); err != nil {
			return err
		}
	}
	return nil
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
