package list

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/cmd/shared/clicmd"
	"github.com/sophium/erun/cmd/shared/textfmt"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/lifecycle"
	"github.com/sophium/erun/erun-common/primitive/shared/projectseam"
	"github.com/spf13/cobra"
)

func NewListCmd(store lifecycle.ListStore, findProjectRoot projectseam.ProjectFinderFunc) *cobra.Command {
	var versionDriftTenant string
	var gateEnvironment string
	var controlPlanes bool
	var failOnDrift bool
	var erunAlias string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List configured tenants and environments",
		Long: "List every configured tenant and environment, including each environment's erun version, and flag any erun ssh alias ~/.ssh/config declares that no configured environment claims any more -- a stale block whose local port was reissued resolves into whichever environment inherited it, so `ssh <alias>` reaches an environment you did not name.\n\n" +
			"Pass --tenant to instead report erun-version drift within one tenant: every environment's version, and the newest version observed among them. When an environment's version is not recorded locally, its deployed release is read live to tell a confirmed absence (\"none\") apart from a version that could not be determined at all (\"undetermined\", excluded from the max/behind computation with the reason stated); --dry-run traces that check instead of running it. Add --gate-environment to name the environment driving that tenant's merge-queue gate, and flag whether it is running an older erun version than any environment it gates -- a gate older than the code it gates can pass a change that would fail on current code.\n\n" +
			"Pass --control-planes to instead report every configured erun-hosted control plane's deployed version (GET /v1/platform, unauthenticated) against the newest version erun's own registry has actually published -- deployed-vs-published, not deployed-vs-main. A route or feature can merge, close its issue, and still be unreachable for months because the plane serving it was simply never rolled onto an already-published release; --tenant's drift has no registry baseline to catch that. Each reachable plane's own GET /v1/platform also names the URL of its console and of its documentation site, so both are checked the same way (GET /version.json, unauthenticated) against the same published baseline and reported nested under the plane -- each is published by its own deploy, so any of the three can drift from the others, and neither surface has a version report of its own to notice that without this. A surface the plane advertises no URL for is reported as absent, never as up to date. A plane whose own discovery document advertises an apiUrl resolving to a genuinely different address is flagged distinctly, since that is not a benign alias. Add --erun-alias to narrow the check to one configured erun-hosted alias instead of probing every configured one. Requires network access to each configured plane and the surfaces it advertises, and to erun's registry; --dry-run traces what would be checked instead.\n\n" +
			"Like the rest of `list`, both reports always exit 0 on their own -- this is a reporting command, not a gate. Add --fail-on-drift with --tenant or --control-planes to make that one invocation exit non-zero when the report finds drift, so it can be wired into a script or a schedule.",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runListCommand(clicmd.CommandContext(cmd), store, findProjectRoot, versionDriftTenant, gateEnvironment, erunAlias, controlPlanes, failOnDrift)
		},
	}
	cmd.Flags().StringVar(&versionDriftTenant, "tenant", "", "Report erun-version drift across this tenant's environments instead of the full listing")
	cmd.Flags().StringVar(&gateEnvironment, "gate-environment", "", "With --tenant, name the environment driving that tenant's merge-queue gate and flag whether it is behind any environment it gates")
	cmd.Flags().BoolVar(&controlPlanes, "control-planes", false, "Report every configured erun-hosted control plane's deployed version against the newest version erun's own registry has published, instead of the full listing")
	cmd.Flags().BoolVar(&failOnDrift, "fail-on-drift", false, "With --tenant or --control-planes, exit non-zero when the report finds drift instead of always exiting 0")
	cmd.Flags().StringVar(&erunAlias, "erun-alias", "", "With --control-planes, narrow the check to this one configured erun-hosted alias instead of every configured one (defaults to checking all)")
	clicmd.AddDryRunFlag(cmd)
	cmd.Example = "  erun list\n  erun list --tenant erun\n  erun list --tenant erun --gate-environment build\n  erun list --tenant erun --gate-environment build --output json\n  erun list --tenant erun --fail-on-drift\n  erun list --control-planes\n  erun list --control-planes --dry-run\n  erun list --control-planes --fail-on-drift\n  erun list --control-planes --erun-alias erun+api.acme.services.erunpaas.com@erun"
	return cmd
}

func validateListFlags(controlPlanes, failOnDrift bool, versionDriftTenant, gateEnvironment, erunAlias string) error {
	if gateEnvironment != "" && versionDriftTenant == "" {
		return fmt.Errorf("--gate-environment requires --tenant")
	}
	if controlPlanes && (versionDriftTenant != "" || gateEnvironment != "") {
		return fmt.Errorf("--control-planes cannot be combined with --tenant/--gate-environment")
	}
	if failOnDrift && versionDriftTenant == "" && !controlPlanes {
		return fmt.Errorf("--fail-on-drift requires --tenant or --control-planes")
	}
	return validateListControlPlaneAliasFlag(controlPlanes, erunAlias)
}

func validateListControlPlaneAliasFlag(controlPlanes bool, erunAlias string) error {
	if erunAlias != "" && !controlPlanes {
		return fmt.Errorf("--erun-alias requires --control-planes")
	}
	return nil
}

func runListCommand(ctx core.Context, store lifecycle.ListStore, findProjectRoot projectseam.ProjectFinderFunc, versionDriftTenant, gateEnvironment, erunAlias string, controlPlanes, failOnDrift bool) error {
	ctx.TraceCommand("", "erun", "list")
	versionDriftTenant = strings.TrimSpace(versionDriftTenant)
	gateEnvironment = strings.TrimSpace(gateEnvironment)
	erunAlias = strings.TrimSpace(erunAlias)
	if err := validateListFlags(controlPlanes, failOnDrift, versionDriftTenant, gateEnvironment, erunAlias); err != nil {
		return err
	}

	result, err := lifecycle.ResolveListResult(store, findProjectRoot, model.OpenParams{
		UseDefaultTenant:      true,
		UseDefaultEnvironment: true,
	})
	if err != nil {
		return err
	}

	if controlPlanes {
		return runListControlPlanes(ctx, result, erunAlias, failOnDrift)
	}

	if versionDriftTenant != "" {
		return runListVersionDrift(ctx, result, versionDriftTenant, gateEnvironment, failOnDrift)
	}

	return WriteListResult(ctx, result)
}

func WriteListResult(ctx core.Context, result lifecycle.ListResult) error {
	if err := ctx.WriteResult(result); err != nil {
		return err
	}
	if ctx.Output == model.OutputJSON {
		return nil
	}
	if err := writeListHeaderSections(ctx, result); err != nil {
		return err
	}
	if err := writeCloudProviders(ctx, result.CloudProviders); err != nil {
		return err
	}
	if err := writeListTenants(ctx, result.Tenants); err != nil {
		return err
	}
	if err := writeOrphanedSSHAliases(ctx, result.OrphanedSSHAliases); err != nil {
		return err
	}
	return writeOrchestrators(ctx, result.Orchestrators)
}

// writeOrphanedSSHAliases reports unclaimed erun-named ssh Host blocks. Removal stays
// manual: erun cannot tell its own blocks from hand-maintained ones.
func writeOrphanedSSHAliases(ctx core.Context, orphans []model.SSHOrphanedAlias) error {
	if len(orphans) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(ctx.Stdout, "SSH config ("+sshConfigLocationLabel()+"):"); err != nil {
		return err
	}
	for _, orphan := range orphans {
		if _, err := fmt.Fprintln(ctx.Stdout, "  "+orphanedSSHAliasLine(orphan)); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(ctx.Stdout, "  remove each block above by hand; erun cannot tell its own blocks from hand-maintained ones")
	return err
}

func sshConfigLocationLabel() string {
	return "~/.ssh/config"
}

func orphanedSSHAliasLine(orphan model.SSHOrphanedAlias) string {
	line := orphan.Alias + ": no environment claims it"
	if orphan.ReachesEnvironment != "" {
		target := orphan.ReachesTenant + "/" + orphan.ReachesEnvironment
		return fmt.Sprintf("%s, and its port %d now belongs to %s — `ssh %s` reaches %s, not the environment the alias names",
			line, orphan.Port, target, orphan.Alias, target)
	}
	if orphan.Port > 0 {
		return fmt.Sprintf("%s, and its port %d belongs to no environment", line, orphan.Port)
	}
	return line + ", and it names no port"
}

func writeListHeaderSections(ctx core.Context, result lifecycle.ListResult) error {
	if err := writeListConfigurationSection(ctx, result.ConfigDirectory); err != nil {
		return err
	}
	if err := writeListDefaultsSection(ctx, result.Defaults); err != nil {
		return err
	}
	return writeListCurrentDirectorySection(ctx, result.CurrentDirectory)
}

func writeListConfigurationSection(ctx core.Context, configDirectory string) error {
	if _, err := fmt.Fprintln(ctx.Stdout, "Configuration:"); err != nil {
		return err
	}
	return textfmt.WriteLabeledValue(ctx, "directory", textfmt.ValueOrNone(configDirectory))
}

func writeListDefaultsSection(ctx core.Context, defaults model.ListDefaultsResult) error {
	if _, err := fmt.Fprintln(ctx.Stdout, "Defaults:"); err != nil {
		return err
	}
	if err := textfmt.WriteLabeledValue(ctx, "tenant", textfmt.ValueOrNone(defaults.Tenant)); err != nil {
		return err
	}
	return textfmt.WriteLabeledValue(ctx, "environment", textfmt.ValueOrNone(defaults.Environment))
}

func writeListCurrentDirectorySection(ctx core.Context, current lifecycle.ListCurrentDirectoryResult) error {
	if _, err := fmt.Fprintln(ctx.Stdout, "Current Directory:"); err != nil {
		return err
	}
	if err := textfmt.WriteLabeledValue(ctx, "path", textfmt.ValueOrNone(current.Path)); err != nil {
		return err
	}
	if err := textfmt.WriteLabeledValue(ctx, "repo", textfmt.ValueOrNone(current.Repo)); err != nil {
		return err
	}
	if err := textfmt.WriteLabeledValue(ctx, "configured tenant", configuredCurrentTenantOrNone(current.ConfiguredTenant)); err != nil {
		return err
	}
	return writeEffectiveOpen(ctx, current)
}

func writeListTenants(ctx core.Context, tenants []lifecycle.ListTenantResult) error {
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
