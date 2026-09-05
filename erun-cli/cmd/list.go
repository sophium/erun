package cmd

import (
	"fmt"
	"strings"

	common "github.com/sophium/erun/erun-common"
	sshconfig "github.com/sophium/erun/internal/sshconfig"
	"github.com/spf13/cobra"
)

func newListCmd(store common.ListStore, findProjectRoot common.ProjectFinderFunc) *cobra.Command {
	var versionDriftTenant string
	var gateEnvironment string
	var controlPlanes bool
	var failOnDrift bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List configured tenants and environments",
		Long: "List every configured tenant and environment, including each environment's erun version.\n\n" +
			"Pass --tenant to instead report erun-version drift within one tenant: every environment's version, and the newest version observed among them. Add --gate-environment to name the environment driving that tenant's merge-queue gate, and flag whether it is running an older erun version than any environment it gates -- a gate older than the code it gates can pass a change that would fail on current code. When an environment's version cannot be read from config alone -- a tenant that ships its own runtime image under its own tag scheme has no version erun can parse from it -- this falls back to a live probe of that environment's own local MCP edge, which always knows the version of the binary actually running there; an environment nobody has opened still reports unresolved rather than making the probe hang.\n\n" +
			"Pass --control-planes to instead report every configured erun-hosted control plane's deployed version (GET /v1/platform, unauthenticated) against the newest version erun's own registry has actually published -- deployed-vs-published, not deployed-vs-main. A route or feature can merge, close its issue, and still be unreachable for months because the plane serving it was simply never rolled onto an already-published release; --tenant's drift has no registry baseline to catch that. Each reachable plane's own GET /v1/platform also names its console's URL, so its console is checked the same way (GET /version.json, unauthenticated) against the same published baseline and reported nested under the plane -- a plane and its console can drift from each other, and a console has no version surface of its own to notice that without this. Requires network access to each configured plane and console, and to erun's registry; --dry-run traces what would be checked instead.\n\n" +
			"Like the rest of `list`, both reports always exit 0 on their own -- this is a reporting command, not a gate. Add --fail-on-drift with --tenant or --control-planes to make that one invocation exit non-zero when the report finds drift, so it can be wired into a script or a schedule.",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runListCommand(commandContext(cmd), store, findProjectRoot, versionDriftTenant, gateEnvironment, controlPlanes, failOnDrift)
		},
	}
	cmd.Flags().StringVar(&versionDriftTenant, "tenant", "", "Report erun-version drift across this tenant's environments instead of the full listing")
	cmd.Flags().StringVar(&gateEnvironment, "gate-environment", "", "With --tenant, name the environment driving that tenant's merge-queue gate and flag whether it is behind any environment it gates")
	cmd.Flags().BoolVar(&controlPlanes, "control-planes", false, "Report every configured erun-hosted control plane's deployed version against the newest version erun's own registry has published, instead of the full listing")
	cmd.Flags().BoolVar(&failOnDrift, "fail-on-drift", false, "With --tenant or --control-planes, exit non-zero when the report finds drift instead of always exiting 0")
	addDryRunFlag(cmd)
	cmd.Example = "  erun list\n  erun list --tenant erun\n  erun list --tenant erun --gate-environment build\n  erun list --tenant erun --gate-environment build --output json\n  erun list --tenant erun --fail-on-drift\n  erun list --control-planes\n  erun list --control-planes --dry-run\n  erun list --control-planes --fail-on-drift"
	return cmd
}

func validateListFlags(controlPlanes, failOnDrift bool, versionDriftTenant, gateEnvironment string) error {
	if gateEnvironment != "" && versionDriftTenant == "" {
		return fmt.Errorf("--gate-environment requires --tenant")
	}
	if controlPlanes && (versionDriftTenant != "" || gateEnvironment != "") {
		return fmt.Errorf("--control-planes cannot be combined with --tenant/--gate-environment")
	}
	if failOnDrift && versionDriftTenant == "" && !controlPlanes {
		return fmt.Errorf("--fail-on-drift requires --tenant or --control-planes")
	}
	return nil
}

func runListCommand(ctx common.Context, store common.ListStore, findProjectRoot common.ProjectFinderFunc, versionDriftTenant, gateEnvironment string, controlPlanes, failOnDrift bool) error {
	ctx.TraceCommand("", "erun", "list")
	versionDriftTenant = strings.TrimSpace(versionDriftTenant)
	gateEnvironment = strings.TrimSpace(gateEnvironment)
	if err := validateListFlags(controlPlanes, failOnDrift, versionDriftTenant, gateEnvironment); err != nil {
		return err
	}

	result, err := common.ResolveListResult(store, findProjectRoot, common.OpenParams{
		UseDefaultTenant:      true,
		UseDefaultEnvironment: true,
	})
	if err != nil {
		return err
	}

	if controlPlanes {
		return runListControlPlanes(ctx, result, failOnDrift)
	}

	if versionDriftTenant != "" {
		return runListVersionDrift(ctx, result, versionDriftTenant, gateEnvironment, failOnDrift)
	}

	return writeListResult(ctx, result)
}

func runListVersionDrift(ctx common.Context, result common.ListResult, versionDriftTenant, gateEnvironment string, failOnDrift bool) error {
	drift, err := common.ResolveTenantVersionDrift(ctx, result, versionDriftTenant, gateEnvironment, common.DefaultEnvironmentVersionProbe(currentBuildInfo().Version))
	if err != nil {
		return err
	}
	if ctx.Output == common.OutputJSON {
		if err := ctx.WriteResult(drift); err != nil {
			return err
		}
	} else if err := writeVersionDriftReport(ctx, drift); err != nil {
		return err
	}
	if !failOnDrift {
		return nil
	}
	return versionDriftExitError(drift)
}

func runListControlPlanes(ctx common.Context, result common.ListResult, failOnDrift bool) error {
	drift := common.ResolveControlPlaneVersionDrift(ctx, result, common.DefaultCloudDependencies(), common.ResolveDefaultRuntimeRegistryVersions)
	if ctx.Output == common.OutputJSON {
		if err := ctx.WriteResult(drift); err != nil {
			return err
		}
	} else if err := writeControlPlaneVersionReport(ctx, drift); err != nil {
		return err
	}
	if ctx.DryRun || !failOnDrift {
		return nil
	}
	return controlPlaneVersionDriftExitError(drift)
}

// versionDriftExitError makes tenant version drift a non-zero exit when
// --fail-on-drift asks for it, after the full report has already printed:
// any environment behind the tenant's own max, or a gate environment whose
// own behind verdict is unresolved or true -- see erun-cli/AGENTS.md §
// "Exit-Code Contract: Reporting Commands Vs Gating Checks" for why this is
// opt-in rather than the command's default.
func versionDriftExitError(drift common.TenantVersionDrift) error {
	var problems []string
	var behind, unresolved []string
	for _, env := range drift.Environments {
		if env.BehindMax {
			behind = append(behind, env.Environment)
		}
		if env.VersionUnresolved {
			unresolved = append(unresolved, env.Environment)
		}
	}
	if len(behind) > 0 {
		problems = append(problems, fmt.Sprintf("%d environment(s) behind the tenant's max version: %s", len(behind), strings.Join(behind, ", ")))
	}
	if len(unresolved) > 0 {
		problems = append(problems, fmt.Sprintf("%d environment(s) are up and answering but their erun version could not be determined: %s", len(unresolved), strings.Join(unresolved, ", ")))
	}
	if drift.GateEnvironment != "" {
		switch {
		case drift.GateVersionUnresolved:
			problems = append(problems, fmt.Sprintf("gate environment %s's own erun version could not be resolved", drift.GateEnvironment))
		case drift.GateBehind:
			problems = append(problems, fmt.Sprintf("gate environment %s is outdated relative to %s", drift.GateEnvironment, strings.Join(drift.GateOutdatedBy, ", ")))
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("version drift for tenant %s: %s", drift.Tenant, strings.Join(problems, "; "))
}

// controlPlaneVersionDriftExitError makes control-plane version drift a
// non-zero exit when --fail-on-drift asks for it: any plane behind or ahead
// of the published version, any plane erun could not reach, or a baseline
// erun could not even resolve -- none of those confirm a plane is running
// what erun actually published.
func controlPlaneVersionDriftExitError(drift common.ControlPlaneVersionDrift) error {
	var problems []string
	if drift.PublishedVersionError != "" {
		problems = append(problems, "the published version could not be resolved: "+drift.PublishedVersionError)
	}
	unreachable, behind, ahead := classifyControlPlaneVersionDrift(drift.Planes)
	if len(unreachable) > 0 {
		problems = append(problems, fmt.Sprintf("%d plane(s) unreachable: %s", len(unreachable), strings.Join(unreachable, ", ")))
	}
	if len(behind) > 0 {
		problems = append(problems, fmt.Sprintf("%d plane(s) behind published: %s", len(behind), strings.Join(behind, ", ")))
	}
	if len(ahead) > 0 {
		problems = append(problems, fmt.Sprintf("%d plane(s) ahead of published: %s", len(ahead), strings.Join(ahead, ", ")))
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("control plane version drift: %s", strings.Join(problems, "; "))
}

// classifyControlPlaneVersionDrift buckets every plane, and its linked
// console when one was checked, into unreachable/behind/ahead -- a console's
// own label gets a " (console)" suffix so the two stay distinguishable in the
// --fail-on-drift summary.
func classifyControlPlaneVersionDrift(planes []common.ControlPlaneVersionStatus) (unreachable, behind, ahead []string) {
	for _, plane := range planes {
		unreachable, behind, ahead = appendControlPlaneVersionVerdict(unreachable, behind, ahead, plane.Alias, plane.Reachable, plane.Behind, plane.Ahead)
		if plane.Console == nil {
			continue
		}
		unreachable, behind, ahead = appendControlPlaneVersionVerdict(unreachable, behind, ahead, plane.Alias+" (console)", plane.Console.Reachable, plane.Console.Behind, plane.Console.Ahead)
	}
	return unreachable, behind, ahead
}

func appendControlPlaneVersionVerdict(unreachable, behind, ahead []string, label string, reachable, isBehind, isAhead bool) ([]string, []string, []string) {
	switch {
	case !reachable:
		unreachable = append(unreachable, label)
	case isBehind:
		behind = append(behind, label)
	case isAhead:
		ahead = append(ahead, label)
	}
	return unreachable, behind, ahead
}

func writeVersionDriftReport(ctx common.Context, drift common.TenantVersionDrift) error {
	if _, err := fmt.Fprintf(ctx.Stdout, "Version drift for tenant %s:\n", drift.Tenant); err != nil {
		return err
	}
	if err := writeLabeledValue(ctx, "max version", valueOrNone(drift.MaxVersion)); err != nil {
		return err
	}
	if err := writeVersionDriftEnvironments(ctx, drift.Environments); err != nil {
		return err
	}
	if drift.GateEnvironment == "" {
		return nil
	}
	return writeVersionDriftGate(ctx, drift)
}

func writeVersionDriftEnvironments(ctx common.Context, environments []common.EnvironmentVersionStatus) error {
	if _, err := fmt.Fprintln(ctx.Stdout, "  environments:"); err != nil {
		return err
	}
	for _, env := range environments {
		line := "    - " + env.Environment + " version=" + quotedValueOrNone(env.Version)
		if env.BehindMax {
			line += " [behind max]"
		}
		if env.VersionUnresolved {
			line += " [up but version unresolved]"
		}
		if _, err := fmt.Fprintln(ctx.Stdout, line); err != nil {
			return err
		}
	}
	return nil
}

func writeVersionDriftGate(ctx common.Context, drift common.TenantVersionDrift) error {
	if _, err := fmt.Fprintln(ctx.Stdout, "  gate:"); err != nil {
		return err
	}
	if err := writeIndentedValue(ctx, 4, "environment", drift.GateEnvironment); err != nil {
		return err
	}
	if err := writeIndentedValue(ctx, 4, "version", valueOrNone(drift.GateVersion)); err != nil {
		return err
	}
	switch {
	case drift.GateVersionUnresolved:
		_, err := fmt.Fprintln(ctx.Stdout, "    behind: unknown (gate's own erun version could not be resolved from config)")
		return err
	case drift.GateBehind:
		_, err := fmt.Fprintf(ctx.Stdout, "    behind: yes -- outdated relative to %s\n", strings.Join(drift.GateOutdatedBy, ", "))
		return err
	default:
		_, err := fmt.Fprintln(ctx.Stdout, "    behind: no")
		return err
	}
}

func writeControlPlaneVersionReport(ctx common.Context, drift common.ControlPlaneVersionDrift) error {
	if ctx.DryRun {
		_, err := fmt.Fprintln(ctx.Stdout, "Dry run: control plane version check planned; see trace for the planes and registry lookup that would be probed.")
		return err
	}
	if drift.PublishedVersionError != "" {
		if err := writeLabeledValue(ctx, "published version", "unresolved ("+drift.PublishedVersionError+")"); err != nil {
			return err
		}
	} else {
		if err := writeLabeledValue(ctx, "published version", valueOrNone(drift.PublishedVersion)); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(ctx.Stdout, "Control planes:"); err != nil {
		return err
	}
	if len(drift.Planes) == 0 {
		_, err := fmt.Fprintln(ctx.Stdout, "  none")
		return err
	}
	for _, plane := range drift.Planes {
		if err := writeControlPlaneVersionEntry(ctx, plane); err != nil {
			return err
		}
	}
	return nil
}

func writeControlPlaneVersionEntry(ctx common.Context, plane common.ControlPlaneVersionStatus) error {
	line := "  - " + plane.Alias + " api-url=" + quotedValueOrNone(plane.APIURL)
	if !plane.Reachable {
		line += " reachable=no reason=" + quotedValueOrNone(plane.UnreachableReason)
		_, err := fmt.Fprintln(ctx.Stdout, line)
		return err
	}
	line += " reachable=yes version=" + quotedValueOrNone(plane.Version)
	switch {
	case plane.Behind:
		line += " [behind published -- roll it]"
	case plane.Ahead:
		line += " [ahead of published -- running an unpublished version]"
	}
	if _, err := fmt.Fprintln(ctx.Stdout, line); err != nil {
		return err
	}
	if plane.Console == nil {
		return nil
	}
	return writeControlPlaneConsoleEntry(ctx, *plane.Console)
}

func writeControlPlaneConsoleEntry(ctx common.Context, console common.ConsoleVersionStatus) error {
	line := "    console: url=" + quotedValueOrNone(console.URL)
	if !console.Reachable {
		line += " reachable=no reason=" + quotedValueOrNone(console.UnreachableReason)
		_, err := fmt.Fprintln(ctx.Stdout, line)
		return err
	}
	line += " reachable=yes version=" + quotedValueOrNone(console.Version)
	switch {
	case console.Behind:
		line += " [behind published -- roll it]"
	case console.Ahead:
		line += " [ahead of published -- running an unpublished version]"
	}
	_, err := fmt.Fprintln(ctx.Stdout, line)
	return err
}

func writeListResult(ctx common.Context, result common.ListResult) error {
	if err := writeListHeaderSections(ctx, result); err != nil {
		return err
	}
	if err := writeCloudProviders(ctx, result.CloudProviders); err != nil {
		return err
	}
	if err := writeListTenants(ctx, result.Tenants); err != nil {
		return err
	}
	return writeOrchestrators(ctx, result.Orchestrators)
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

func writeOrchestrators(ctx common.Context, orchestrators []common.ListOrchestratorResult) error {
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

func writeOrchestratorEntry(ctx common.Context, orchestrator common.ListOrchestratorResult) error {
	if _, err := fmt.Fprintf(ctx.Stdout, "  - %s %q\n", orchestrator.ID, orchestrator.Name); err != nil {
		return err
	}
	if len(orchestrator.Environments) == 0 {
		_, err := fmt.Fprintln(ctx.Stdout, "    environments: none")
		return err
	}
	if _, err := fmt.Fprintln(ctx.Stdout, "    environments:"); err != nil {
		return err
	}
	for _, env := range orchestrator.Environments {
		if err := writeOrchestratorEnvEntry(ctx, env); err != nil {
			return err
		}
	}
	return nil
}

// orchestratorEnvRoleLabel renders "undeclared" for an empty role rather than
// guessing a default -- unset means the operator has not stated a purpose for
// this link, distinct from either known role.
func orchestratorEnvRoleLabel(role common.OrchestratorEnvRole) string {
	if role == "" {
		return "undeclared"
	}
	return string(role)
}

func writeOrchestratorEnvEntry(ctx common.Context, env common.ListOrchestratorEnvResult) error {
	line := "      - " + env.Tenant + "/" + env.Environment
	line += " role=" + orchestratorEnvRoleLabel(env.Role)
	if strings.TrimSpace(env.Directory) != "" {
		line += " directory=" + env.Directory
	}
	_, err := fmt.Fprintln(ctx.Stdout, line)
	return err
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
		if err := writeEffectiveOpenSSH(ctx, *current.Effective); err != nil {
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

func writeEffectiveOpenSSH(ctx common.Context, effective common.ListEffectiveTargetResult) error {
	ssh := effective.SSH
	if err := writeLabeledValue(ctx, "sshd", "on"); err != nil {
		return err
	}
	if err := writeLabeledValue(ctx, "ssh host", sshHostAliasLabel(effective.Tenant, effective.Environment, ssh.HostAlias)); err != nil {
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
		if err := writeEnvironmentEntry(ctx, tenant.Name, env); err != nil {
			return err
		}
	}
	return nil
}

func writeEnvironmentEntry(ctx common.Context, tenantName string, env common.ListEnvironmentResult) error {
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

func environmentDetailLines(tenantName string, env common.ListEnvironmentResult) []string {
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
		indent + "runtime-version: " + runtimeVersionLabel(tenantName, env),
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
		lines = append(lines, environmentSSHDetailLines(tenantName, env.Name, env.SSH, indent)...)
	} else {
		lines = append(lines, indent+"sshd: off")
	}
	return lines
}

func environmentSSHDetailLines(tenantName, environmentName string, ssh common.ListSSHResult, indent string) []string {
	return []string{
		indent + "sshd: on",
		indent + "ssh-host: " + sshHostAliasLabel(tenantName, environmentName, ssh.HostAlias),
		indent + "ssh-user: " + valueOrNone(ssh.User),
		indent + "ssh-local-port: " + fmt.Sprintf("%d", ssh.LocalPort),
		indent + "ssh-workspace: " + valueOrNone(ssh.WorkspacePath),
		indent + "ssh-public-key-path: " + valueOrNone(ssh.PublicKeyPath),
		indent + "ssh-workspace-sync: " + enabledDisabledLabel(ssh.WorkspaceSyncEnabled),
		indent + "ssh-workspace-sync-local-path: " + valueOrNone(ssh.WorkspaceSyncLocalPath),
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
		return valueOrNone(alias)
	}
	configured, err := sshconfig.DefaultConfigHasAlias(alias)
	if err != nil || configured {
		return alias
	}
	return fmt.Sprintf("%s (not in ~/.ssh/config — run `erun sshd init %s %s` to fix)", alias, tenantName, environmentName)
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
		verdicts = append(verdicts, common.FormatRuntimeSizingVerdict(verdict))
	}
	return []string{
		indent + "sizing: " + strings.Join(verdicts, "; "),
		indent + "sizing-evidence: " + common.FormatRuntimeSizingEvidence(*sizing),
	}
}

// runtimeVersionLabel names the release line beside the bare runtime-version
// number: a number alone reads as an erun version even when it rides a
// tenant's own devops line, and two environments in the same tenant can ride
// different lines from each other (erun#1746). An environment that has never
// deployed still reads "none", unchanged; one that has deployed but recorded
// no resolved image reads distinctly as "line undetermined" rather than
// guessing a line from the tenant name.
func runtimeVersionLabel(tenantName string, env common.ListEnvironmentResult) string {
	version := valueOrNone(env.RuntimeVersion)
	if env.RuntimeVersionLine == nil {
		return version
	}
	line := *env.RuntimeVersionLine
	if line.Undetermined {
		return version + " (line undetermined — no resolved runtime image recorded; redeploy to record it)"
	}
	detail := line.Line + " line, " + line.Image
	if line.Disagrees {
		detail += " — release name " + common.RuntimeReleaseName(tenantName) + " disagrees with the image"
	}
	return version + " (" + detail + ")"
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
