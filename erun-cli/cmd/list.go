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

	if _, err := fmt.Fprintln(ctx.Stdout, "Tenants:"); err != nil {
		return err
	}
	if len(result.Tenants) == 0 {
		_, err := fmt.Fprintln(ctx.Stdout, "  none")
		return err
	}

	for _, tenant := range result.Tenants {
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
	if err := writeLabeledValue(ctx, "effective target", current.Effective.Tenant+"/"+current.Effective.Environment); err != nil {
		return err
	}
	if err := writeLabeledValue(ctx, "kubernetes context", current.Effective.KubernetesContext); err != nil {
		return err
	}
	if err := writeLabeledValue(ctx, "snapshot", enabledDisabledLabel(current.Effective.Snapshot)); err != nil {
		return err
	}
	return writeLabeledValue(ctx, "repo path", current.Effective.RepoPath)
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
	if err := writeIndentedValue(ctx, 4, "project root", tenant.ProjectRoot); err != nil {
		return err
	}
	if err := writeIndentedValue(ctx, 4, "default environment", tenant.DefaultEnvironment); err != nil {
		return err
	}
	if err := writeIndentedValue(ctx, 4, "snapshot", enabledDisabledLabel(tenant.Snapshot)); err != nil {
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
		envLine += " repo=" + quotedValueOrNone(env.RepoPath)
		if _, err := fmt.Fprintln(ctx.Stdout, envLine); err != nil {
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
