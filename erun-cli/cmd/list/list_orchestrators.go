package list

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/cmd/shared/textfmt"
	"github.com/sophium/erun/erun-common/base/core"
	modelpkg "github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/lifecycle/list"
)

func writeOrchestrators(ctx core.Context, orchestrators []modelpkg.ListOrchestratorResult) error {
	if _, err := fmt.Fprintln(ctx.Stdout, "Orchestrators:"); err != nil {
		return err
	}
	if len(orchestrators) == 0 {
		_, err := fmt.Fprintln(ctx.Stdout, "  none")
		return err
	}
	for _, orchestrator := range orchestrators {
		if err := writeOrchestratorEntry(ctx, orchestrator); err != nil {
			return err
		}
	}
	return nil
}

func writeOrchestratorEntry(ctx core.Context, orchestrator modelpkg.ListOrchestratorResult) error {
	if _, err := fmt.Fprintf(ctx.Stdout, "  - %s %q\n", orchestrator.ID, orchestrator.Name); err != nil {
		return err
	}
	if err := writeOrchestratorAlias(ctx, orchestrator.Alias); err != nil {
		return err
	}
	if err := writeOrchestratorLaunchSettings(ctx, orchestrator); err != nil {
		return err
	}
	if len(orchestrator.Environments) == 0 {
		if _, err := fmt.Fprintln(ctx.Stdout, "    environments: none"); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintln(ctx.Stdout, "    environments:"); err != nil {
			return err
		}
		for _, env := range orchestrator.Environments {
			if err := writeOrchestratorEnvEntry(ctx, env); err != nil {
				return err
			}
		}
	}
	return writeOrchestratorDirectories(ctx, orchestrator.Directories)
}

// writeOrchestratorAlias prints nothing when unset; the default is not a setting.
func writeOrchestratorAlias(ctx core.Context, alias string) error {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return nil
	}
	_, err := fmt.Fprintf(ctx.Stdout, "    platform alias: %s\n", alias)
	return err
}

func writeOrchestratorLaunchSettings(ctx core.Context, orchestrator modelpkg.ListOrchestratorResult) error {
	var lines []string
	if orchestrator.UseGateway {
		lines = append(lines, "gateway: on")
	}
	if model := strings.TrimSpace(orchestrator.Model); model != "" {
		lines = append(lines, "model: "+model)
	}
	if effort := strings.TrimSpace(orchestrator.Effort); effort != "" {
		lines = append(lines, "effort: "+effort)
	}
	for _, line := range lines {
		if _, err := fmt.Fprintf(ctx.Stdout, "    %s\n", line); err != nil {
			return err
		}
	}
	return nil
}

func writeOrchestratorDirectories(ctx core.Context, directories []string) error {
	if len(directories) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(ctx.Stdout, "    directories:"); err != nil {
		return err
	}
	for _, directory := range directories {
		if _, err := fmt.Fprintf(ctx.Stdout, "      - %s\n", directory); err != nil {
			return err
		}
	}
	return nil
}

// orchestratorEnvRoleLabel renders an empty role as "undeclared", not a default.
func orchestratorEnvRoleLabel(role modelpkg.OrchestratorEnvRole) string {
	if role == "" {
		return "undeclared"
	}
	return string(role)
}

func writeOrchestratorEnvEntry(ctx core.Context, env modelpkg.ListOrchestratorEnvResult) error {
	line := "      - " + env.Tenant + "/" + env.Environment
	line += " role=" + orchestratorEnvRoleLabel(env.Role)
	if strings.TrimSpace(env.Directory) != "" {
		line += " directory=" + env.Directory
	}
	_, err := fmt.Fprintln(ctx.Stdout, line)
	return err
}

func writeEffectiveOpen(ctx core.Context, current list.ListCurrentDirectoryResult) error {
	if current.Effective == nil {
		if strings.TrimSpace(current.EffectiveError) != "" {
			return textfmt.WriteLabeledValue(ctx, "effective target", "unavailable ("+current.EffectiveError+")")
		}
		return textfmt.WriteLabeledValue(ctx, "effective target", "none")
	}
	if err := writeEffectiveOpenBase(ctx, *current.Effective); err != nil {
		return err
	}
	if current.Effective.SSH.Enabled {
		if err := writeEffectiveOpenSSH(ctx, *current.Effective); err != nil {
			return err
		}
	}
	return textfmt.WriteLabeledValue(ctx, "repo path", current.Effective.RepoPath)
}

func writeEffectiveOpenBase(ctx core.Context, effective list.ListEffectiveTargetResult) error {
	if err := writeEffectiveTargetIdentity(ctx, effective); err != nil {
		return err
	}
	if err := writeEffectiveTargetType(ctx, effective); err != nil {
		return err
	}
	return writeEffectiveTargetPorts(ctx, effective)
}

func writeEffectiveTargetIdentity(ctx core.Context, effective list.ListEffectiveTargetResult) error {
	if err := textfmt.WriteLabeledValue(ctx, "effective target", effective.Tenant+"/"+effective.Environment); err != nil {
		return err
	}
	if err := textfmt.WriteLabeledValue(ctx, "kubernetes context", effective.KubernetesContext); err != nil {
		return err
	}
	if strings.TrimSpace(effective.CloudProviderAlias) != "" {
		if err := textfmt.WriteLabeledValue(ctx, "cloud provider", effective.CloudProviderAlias); err != nil {
			return err
		}
	}
	return nil
}

func writeEffectiveTargetType(ctx core.Context, effective list.ListEffectiveTargetResult) error {
	return textfmt.WriteLabeledValue(ctx, "type", textfmt.ValueOrNone(string(effective.Type)))
}

func writeEffectiveTargetPorts(ctx core.Context, effective list.ListEffectiveTargetResult) error {
	if err := textfmt.WriteLabeledValue(ctx, "assigned local port range", portRangeLabel(effective.LocalPorts)); err != nil {
		return err
	}
	if err := textfmt.WriteLabeledValue(ctx, "assigned mcp local port", fmt.Sprintf("%d (when MCP is running or forwarded)", effective.LocalPorts.MCP)); err != nil {
		return err
	}
	if err := textfmt.WriteLabeledValue(ctx, "assigned api local port", fmt.Sprintf("%d (when API is running or forwarded)", effective.LocalPorts.API)); err != nil {
		return err
	}
	if err := textfmt.WriteLabeledValue(ctx, "api url", effective.APIURL); err != nil {
		return err
	}
	if err := textfmt.WriteLabeledValue(ctx, "assigned ssh local port", fmt.Sprintf("%d (when SSH port-forward is active)", effective.LocalPorts.SSH)); err != nil {
		return err
	}
	return textfmt.WriteLabeledValue(ctx, "assigned contribute-app local port", fmt.Sprintf("%d (when contribute mode is active and `erun app --headless` is running)", effective.LocalPorts.ContributeApp))
}

func writeEffectiveOpenSSH(ctx core.Context, effective list.ListEffectiveTargetResult) error {
	ssh := effective.SSH
	if err := textfmt.WriteLabeledValue(ctx, "sshd", "on"); err != nil {
		return err
	}
	if err := textfmt.WriteLabeledValue(ctx, "ssh host", sshHostAliasLabel(effective.Tenant, effective.Environment, ssh.HostAlias)); err != nil {
		return err
	}
	if err := textfmt.WriteLabeledValue(ctx, "ssh user", ssh.User); err != nil {
		return err
	}
	return textfmt.WriteLabeledValue(ctx, "ssh workspace", ssh.WorkspacePath)
}
