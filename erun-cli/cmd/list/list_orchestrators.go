package list

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/cmd/shared"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/command/lifecycle"
)

func writeOrchestrators(ctx core.Context, orchestrators []lifecycle.ListOrchestratorResult) error {
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

func writeOrchestratorEntry(ctx core.Context, orchestrator lifecycle.ListOrchestratorResult) error {
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

// writeOrchestratorAlias prints the erun platform alias this orchestrator
// declares as its own, and nothing at all when it declares none: the default is
// this machine's own alias, so an empty line would report a setting the
// operator never made. Same rule as writeOrchestratorDirectories below.
func writeOrchestratorAlias(ctx core.Context, alias string) error {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return nil
	}
	_, err := fmt.Fprintf(ctx.Stdout, "    platform alias: %s\n", alias)
	return err
}

// writeOrchestratorLaunchSettings prints the gateway, model and effort the
// orchestrator records, each only when set, by the same rule as
// writeOrchestratorAlias: unset is the default launch, not a setting.
func writeOrchestratorLaunchSettings(ctx core.Context, orchestrator lifecycle.ListOrchestratorResult) error {
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

// writeOrchestratorDirectories prints the orchestrator's own directories, and
// nothing at all when it has none: an orchestrator pointed at directories and
// linking no environment must not read as having no scope, and one with only
// environments should not grow an empty section.
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

// orchestratorEnvRoleLabel renders "undeclared" for an empty role rather than
// guessing a default -- unset means the operator has not stated a purpose for
// this link, distinct from either known role.
func orchestratorEnvRoleLabel(role config.OrchestratorEnvRole) string {
	if role == "" {
		return "undeclared"
	}
	return string(role)
}

func writeOrchestratorEnvEntry(ctx core.Context, env lifecycle.ListOrchestratorEnvResult) error {
	line := "      - " + env.Tenant + "/" + env.Environment
	line += " role=" + orchestratorEnvRoleLabel(env.Role)
	if strings.TrimSpace(env.Directory) != "" {
		line += " directory=" + env.Directory
	}
	_, err := fmt.Fprintln(ctx.Stdout, line)
	return err
}

func writeEffectiveOpen(ctx core.Context, current lifecycle.ListCurrentDirectoryResult) error {
	if current.Effective == nil {
		if strings.TrimSpace(current.EffectiveError) != "" {
			return shared.WriteLabeledValue(ctx, "effective target", "unavailable ("+current.EffectiveError+")")
		}
		return shared.WriteLabeledValue(ctx, "effective target", "none")
	}
	if err := writeEffectiveOpenBase(ctx, *current.Effective); err != nil {
		return err
	}
	if current.Effective.SSH.Enabled {
		if err := writeEffectiveOpenSSH(ctx, *current.Effective); err != nil {
			return err
		}
	}
	return shared.WriteLabeledValue(ctx, "repo path", current.Effective.RepoPath)
}

func writeEffectiveOpenBase(ctx core.Context, effective lifecycle.ListEffectiveTargetResult) error {
	if err := writeEffectiveTargetIdentity(ctx, effective); err != nil {
		return err
	}
	if err := writeEffectiveTargetType(ctx, effective); err != nil {
		return err
	}
	return writeEffectiveTargetPorts(ctx, effective)
}

func writeEffectiveTargetIdentity(ctx core.Context, effective lifecycle.ListEffectiveTargetResult) error {
	if err := shared.WriteLabeledValue(ctx, "effective target", effective.Tenant+"/"+effective.Environment); err != nil {
		return err
	}
	if err := shared.WriteLabeledValue(ctx, "kubernetes context", effective.KubernetesContext); err != nil {
		return err
	}
	if strings.TrimSpace(effective.CloudProviderAlias) != "" {
		if err := shared.WriteLabeledValue(ctx, "cloud provider", effective.CloudProviderAlias); err != nil {
			return err
		}
	}
	return nil
}

func writeEffectiveTargetType(ctx core.Context, effective lifecycle.ListEffectiveTargetResult) error {
	return shared.WriteLabeledValue(ctx, "type", shared.ValueOrNone(string(effective.Type)))
}

func writeEffectiveTargetPorts(ctx core.Context, effective lifecycle.ListEffectiveTargetResult) error {
	if err := shared.WriteLabeledValue(ctx, "assigned local port range", portRangeLabel(effective.LocalPorts)); err != nil {
		return err
	}
	if err := shared.WriteLabeledValue(ctx, "assigned mcp local port", fmt.Sprintf("%d (when MCP is running or forwarded)", effective.LocalPorts.MCP)); err != nil {
		return err
	}
	if err := shared.WriteLabeledValue(ctx, "assigned api local port", fmt.Sprintf("%d (when API is running or forwarded)", effective.LocalPorts.API)); err != nil {
		return err
	}
	if err := shared.WriteLabeledValue(ctx, "api url", effective.APIURL); err != nil {
		return err
	}
	if err := shared.WriteLabeledValue(ctx, "assigned ssh local port", fmt.Sprintf("%d (when SSH port-forward is active)", effective.LocalPorts.SSH)); err != nil {
		return err
	}
	return shared.WriteLabeledValue(ctx, "assigned contribute-app local port", fmt.Sprintf("%d (when contribute mode is active and `erun app --headless` is running)", effective.LocalPorts.ContributeApp))
}

func writeEffectiveOpenSSH(ctx core.Context, effective lifecycle.ListEffectiveTargetResult) error {
	ssh := effective.SSH
	if err := shared.WriteLabeledValue(ctx, "sshd", "on"); err != nil {
		return err
	}
	if err := shared.WriteLabeledValue(ctx, "ssh host", sshHostAliasLabel(effective.Tenant, effective.Environment, ssh.HostAlias)); err != nil {
		return err
	}
	if err := shared.WriteLabeledValue(ctx, "ssh user", ssh.User); err != nil {
		return err
	}
	return shared.WriteLabeledValue(ctx, "ssh workspace", ssh.WorkspacePath)
}
