package doctor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/config/configbackup"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/doctor"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/cloud/shelllaunch"
	diagnosispkg "github.com/sophium/erun/erun-common/orchestration/diagnosis"
	deploydiagnosis "github.com/sophium/erun/erun-common/primitive/deploy/diagnosis"
	"github.com/sophium/erun/erun-mcp/tools/shared/jobenvelope"
	"github.com/sophium/erun/erun-mcp/tools/shared/runcmd"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimecfg"
	"github.com/sophium/erun/erun-mcp/tools/shared/toolio"
)

type DoctorInput struct {
	Tenant                     string                   `json:"tenant,omitempty" jsonschema:"optional explicit tenant override; defaults to the server tenant context, and must match it: this server only acts on its own environment"`
	Environment                string                   `json:"environment,omitempty" jsonschema:"optional explicit environment override; defaults to the server environment context, and must match it: this server only acts on its own environment"`
	PruneImages                bool                     `json:"pruneImages,omitempty" jsonschema:"when true, prune unused Docker images"`
	PruneBuildCache            bool                     `json:"pruneBuildCache,omitempty" jsonschema:"when true, prune unused BuildKit cache"`
	PruneContainers            bool                     `json:"pruneContainers,omitempty" jsonschema:"when true, prune stopped Docker containers"`
	ClearPendingHelm           bool                     `json:"clearPendingHelm,omitempty" jsonschema:"when true, clear a stuck helm pending-install/upgrade lock for the runtime release so the next deploy can proceed"`
	Rollback                   bool                     `json:"rollback,omitempty" jsonschema:"when true, roll the runtime release back to its last successful revision (recovers a bad/non-converging deploy)"`
	Preview                    bool                     `json:"preview,omitempty" jsonschema:"when true, resolve and print the planned actions without executing them"`
	Verbosity                  int                      `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
	RestoreConfigFromBackup    string                   `json:"restoreConfigFromBackup,omitempty" jsonschema:"YYYY-MM-DD or absolute path; when set, restore the root erun config from the matching daily backup before any tenant/env work"`
	RestoreEnvConfigFromBackup string                   `json:"restoreEnvConfigFromBackup,omitempty" jsonschema:"YYYY-MM-DD or absolute path; when set, restore the target environment's config.yaml from the matching daily backup (requires explicit tenant and environment) before any tenant/env work"`
	RepairOrphanedAliases      []DoctorRepairAliasInput `json:"repairOrphanedAliases,omitempty" jsonschema:"per-alias AWS init parameters; when present, doctor re-initializes each listed cloud provider alias before tenant/env work"`
	SyncConfig                 bool                     `json:"syncConfig,omitempty" jsonschema:"when true, reconcile the in-pod erun config with the helm-injected ERUN_* env vars (injected wins). Only meaningful inside a runtime pod, where the projection is rewritten without those values whenever the pod is replaced, which silently changes which registry a build resolves and whether a project build script runs"`
	jobenvelope.JobEnvelopeInput
}

// DoctorRepairAliasInput answers the "Re-initialize cloud provider alias?" prompt.
type DoctorRepairAliasInput struct {
	Alias         string `json:"alias" jsonschema:"orphaned cloud provider alias (username+account@provider) to recreate"`
	SSOStartURL   string `json:"ssoStartUrl" jsonschema:"AWS IAM Identity Center start URL"`
	SSORegion     string `json:"ssoRegion" jsonschema:"AWS IAM Identity Center region"`
	RoleName      string `json:"roleName,omitempty" jsonschema:"AWS permission set (IAM Identity Center) used during SSO login"`
	Region        string `json:"region,omitempty" jsonschema:"default AWS region for the recreated provider (derived from referenced cloud context when blank)"`
	OIDCIssuerURL string `json:"oidcIssuerUrl,omitempty" jsonschema:"override for the OIDC issuer URL; usually inferred from AWS web identity token"`
	SkipLogin     bool   `json:"skipLogin,omitempty" jsonschema:"when true, skip aws sso login and rely on existing credentials"`
}

func DoctorTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, DoctorInput) (*mcp.CallToolResult, jobenvelope.JobEnvelopeOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input DoctorInput) (*mcp.CallToolResult, jobenvelope.JobEnvelopeOutput, error) {
		execute := func(preview bool, log io.Writer) (toolio.CommandOutput, error) {
			var report *toolio.DoctorRootConfigReport
			output, err := runcmd.RunRuntimeCommand(runtime, preview, input.Verbosity, log, func(runCtx core.Context, _ string) error {
				r, runErr := runDoctorToolCommand(runtime, input, runCtx)
				report = r
				return runErr
			})
			if report != nil {
				output.RootConfig = report
			}
			return output, err
		}
		envelope, err := jobenvelope.RunJobEnvelope(runtime, "doctor", input.JobEnvelopeInput, input.Preview, execute)
		return nil, envelope, err
	}
}

func runDoctorToolCommand(runtime runtimecfg.RuntimeConfig, input DoctorInput, runCtx core.Context) (*toolio.DoctorRootConfigReport, error) {
	if input.ClearPendingHelm && input.Rollback {
		return nil, errors.New("clearPendingHelm and rollback are alternative recoveries; request only one")
	}
	// Config drift mis-drives everything below, so reconcile first and return.
	if input.SyncConfig {
		return nil, runDoctorConfigSync(runCtx)
	}
	report, fatal, err := runDoctorRootConfigToolFlow(runtime, input, runCtx)
	if err != nil {
		return report, err
	}
	if fatal || onlyRootConfigDoctorInput(input) {
		return report, nil
	}
	restoreWasTheWholeRequest, err := runDoctorEnvConfigRestore(runtime, input, runCtx, report)
	if err != nil {
		return report, err
	}
	if restoreWasTheWholeRequest {
		return report, nil
	}
	if err := runDoctorTenantEnvActions(runtime, input, runCtx); err != nil {
		return report, err
	}
	return report, nil
}

// runDoctorEnvConfigRestore reports whether the restore was the whole request.
func runDoctorEnvConfigRestore(runtime runtimecfg.RuntimeConfig, input DoctorInput, runCtx core.Context, report *toolio.DoctorRootConfigReport) (bool, error) {
	selector := strings.TrimSpace(input.RestoreEnvConfigFromBackup)
	if selector == "" {
		return false, nil
	}
	if err := restoreDoctorEnvConfigFromBackup(runtime, runCtx, input, selector, report); err != nil {
		return false, err
	}
	return onlyEnvConfigRestoreInput(input), nil
}

// restoreDoctorEnvConfigFromBackup restores the env config before any
// tenant/env work; the target is pinned to this pod's environment.
func restoreDoctorEnvConfigFromBackup(runtime runtimecfg.RuntimeConfig, runCtx core.Context, input DoctorInput, selector string, report *toolio.DoctorRootConfigReport) error {
	tenant, environment, err := runtimecfg.ResolveLocalTarget(runtime, input.Tenant, input.Environment)
	if err != nil {
		return err
	}
	livePath, err := configbackup.EnvConfigPath(tenant, environment)
	if err != nil {
		return err
	}
	backup, ok, err := resolveDoctorEnvBackupSelector(tenant, environment, selector)
	if err != nil {
		return err
	}
	if !ok {
		if dates := availableEnvBackupDates(tenant, environment); dates != "" {
			return fmt.Errorf("no env config backup matches %q for %s/%s; available: %s", selector, tenant, environment, dates)
		}
		return fmt.Errorf("no env config backup matches %q for %s/%s (no backups exist yet)", selector, tenant, environment)
	}
	if !runCtx.DryRun {
		if err := configbackup.RestoreEnvConfigFromBackup(backup.Path, tenant, environment); err != nil {
			return err
		}
	}
	runCtx.TraceCommand("", "cp", backup.Path, livePath)
	report.RestoredEnvConfigFromBackup = backup.Path
	return nil
}

func availableEnvBackupDates(tenant, environment string) string {
	backups, err := configbackup.ListEnvConfigBackups(tenant, environment)
	if err != nil || len(backups) == 0 {
		return ""
	}
	dates := make([]string, 0, len(backups))
	for _, backup := range backups {
		dates = append(dates, backup.Date.Format("2006-01-02"))
	}
	return strings.Join(dates, ", ")
}

func resolveDoctorEnvBackupSelector(tenant, environment, selector string) (model.ConfigBackup, bool, error) {
	if strings.ContainsAny(selector, "/\\") {
		return model.ConfigBackup{Path: selector}, true, nil
	}
	return configbackup.FindEnvConfigBackupByDate(tenant, environment, selector)
}

func onlyEnvConfigRestoreInput(input DoctorInput) bool {
	return strings.TrimSpace(input.RestoreEnvConfigFromBackup) != "" &&
		len(input.RepairOrphanedAliases) == 0 &&
		strings.TrimSpace(input.RestoreConfigFromBackup) == "" &&
		!input.PruneImages && !input.PruneBuildCache && !input.PruneContainers &&
		!input.ClearPendingHelm && !input.Rollback
}

func runDoctorTenantEnvActions(runtime runtimecfg.RuntimeConfig, input DoctorInput, runCtx core.Context) error {
	target, err := resolveDoctorOpenResult(runtime, input)
	if err != nil {
		return err
	}
	req := shelllaunch.ShellLaunchParamsFromResult(target)
	diagnosis, err := writeDoctorDeployDiagnosis(runCtx, req)
	if err != nil {
		return err
	}
	if err := writeDoctorGitPushAccess(runCtx, target, req, diagnosis); err != nil {
		return err
	}
	if err := runDoctorRecoveryToolActions(runCtx, input, req); err != nil {
		return err
	}
	// A host environment's daemon is local, so an unreachable cluster says nothing about it.
	if diagnosis.ClusterUnreachable && doctor.ResolveDoctorDockerDaemon(req).Kind != model.DoctorDockerDaemonHost {
		if err := writeDoctorPodUnreachableSkip(runCtx, "Docker storage"); err != nil {
			return err
		}
		if !anyDoctorActionRequested(input) {
			return nil
		}
		_, err := fmt.Fprintln(runCtx.Stdout, "Skipping the requested prune action(s) for the same reason.")
		return err
	}
	if err := writeDoctorInspection(runCtx, target, req, anyDoctorActionRequested(input)); err != nil {
		return err
	}
	return runDoctorToolActions(runCtx, input, req)
}

func anyDoctorActionRequested(input DoctorInput) bool {
	return input.PruneImages || input.PruneBuildCache || input.PruneContainers
}

// onlyRootConfigDoctorInput skips tenant/env work, which would fail on the
// dangling alias being repaired.
func onlyRootConfigDoctorInput(input DoctorInput) bool {
	if len(input.RepairOrphanedAliases) == 0 && strings.TrimSpace(input.RestoreConfigFromBackup) == "" {
		return false
	}
	return !input.PruneImages && !input.PruneBuildCache && !input.PruneContainers
}

// runDoctorRootConfigToolFlow returns fatal=true when the root config must stop
// the flow, e.g. it is corrupted and no repair input was given.
func runDoctorRootConfigToolFlow(runtime runtimecfg.RuntimeConfig, input DoctorInput, runCtx core.Context) (*toolio.DoctorRootConfigReport, bool, error) {
	inspection, err := configbackup.InspectRootConfig(runtime.Store)
	if err != nil {
		return nil, false, err
	}
	report := &toolio.DoctorRootConfigReport{Inspection: inspection}

	if selector := strings.TrimSpace(input.RestoreConfigFromBackup); selector != "" {
		refreshed, err := restoreDoctorRootConfigFromBackup(runtime, runCtx, inspection, selector, report)
		if err != nil {
			return report, true, err
		}
		inspection = refreshed
	}

	if len(input.RepairOrphanedAliases) > 0 {
		if err := runDoctorAliasRepairs(runtime, input, runCtx, inspection, report); err != nil {
			return report, true, err
		}
		refreshed, err := refreshDoctorInspection(runtime, report)
		if err != nil {
			return report, true, err
		}
		inspection = refreshed
	}

	if doctorRootConfigBlocks(inspection, input) {
		return report, true, nil
	}
	return report, false, nil
}

func restoreDoctorRootConfigFromBackup(runtime runtimecfg.RuntimeConfig, runCtx core.Context, inspection model.RootConfigInspection, selector string, report *toolio.DoctorRootConfigReport) (model.RootConfigInspection, error) {
	backup, ok, err := resolveDoctorBackupSelector(inspection, selector)
	if err != nil {
		return inspection, err
	}
	if !ok {
		return inspection, fmt.Errorf("no root config backup matches %q", selector)
	}
	if !runCtx.DryRun {
		if err := configbackup.RestoreRootConfigFromBackup(backup.Path, inspection.ConfigPath); err != nil {
			return inspection, err
		}
	}
	runCtx.TraceCommand("", "cp", backup.Path, inspection.ConfigPath)
	report.RestoredFromBackup = backup.Path
	return refreshDoctorInspection(runtime, report)
}

func refreshDoctorInspection(runtime runtimecfg.RuntimeConfig, report *toolio.DoctorRootConfigReport) (model.RootConfigInspection, error) {
	refreshed, err := configbackup.InspectRootConfig(runtime.Store)
	if err != nil {
		return model.RootConfigInspection{}, err
	}
	report.Inspection = refreshed
	return refreshed, nil
}

func doctorRootConfigBlocks(inspection model.RootConfigInspection, input DoctorInput) bool {
	if inspection.ConfigStatus != model.RootConfigStatusOK {
		return true
	}
	return len(inspection.OrphanedAliases) > 0 && onlyRootConfigDoctorInput(input)
}

func runDoctorAliasRepairs(runtime runtimecfg.RuntimeConfig, input DoctorInput, runCtx core.Context, inspection model.RootConfigInspection, report *toolio.DoctorRootConfigReport) error {
	byAlias := make(map[string]model.OrphanedAlias, len(inspection.OrphanedAliases))
	for _, orphan := range inspection.OrphanedAliases {
		byAlias[orphan.Alias] = orphan
	}
	for _, entry := range input.RepairOrphanedAliases {
		alias := strings.TrimSpace(entry.Alias)
		if alias == "" {
			return fmt.Errorf("repairOrphanedAliases entries must include alias")
		}
		orphan, ok := byAlias[alias]
		if !ok {
			report.UnresolvedAliases = append(report.UnresolvedAliases, alias)
			continue
		}
		params := model.RepairOrphanedAliasParams{
			Orphan:        orphan,
			SSOStartURL:   strings.TrimSpace(entry.SSOStartURL),
			SSORegion:     strings.TrimSpace(entry.SSORegion),
			RoleName:      strings.TrimSpace(entry.RoleName),
			Region:        firstNonBlank(entry.Region, preferredRegionForOrphanAlias(orphan)),
			OIDCIssuerURL: strings.TrimSpace(entry.OIDCIssuerURL),
			SkipLogin:     entry.SkipLogin,
		}
		if _, err := doctor.RepairOrphanedAlias(runCtx, runtime.Store, params, cloud.CloudDependencies{}); err != nil {
			return fmt.Errorf("repair alias %s: %w", alias, err)
		}
		report.RepairedAliases = append(report.RepairedAliases, alias)
	}
	return nil
}

func resolveDoctorBackupSelector(inspection model.RootConfigInspection, selector string) (model.ConfigBackup, bool, error) {
	if strings.ContainsAny(selector, "/\\") {
		return model.ConfigBackup{Path: selector}, true, nil
	}
	backup, ok, err := configbackup.FindRootConfigBackupByDate(inspection.ConfigPath, selector)
	if err != nil {
		return model.ConfigBackup{}, false, err
	}
	return backup, ok, nil
}

func preferredRegionForOrphanAlias(orphan model.OrphanedAlias) string {
	for _, ref := range orphan.ReferencedByCloudContexts {
		region := strings.TrimSpace(ref.Region)
		if region != "" {
			return region
		}
	}
	return ""
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if v := strings.TrimSpace(value); v != "" {
			return v
		}
	}
	return ""
}

// writeDoctorDeployDiagnosis is read-only, so it runs under preview too; later
// sections reuse its ClusterUnreachable instead of re-probing.
func writeDoctorDeployDiagnosis(runCtx core.Context, req config.ShellLaunchParams) (model.DeployDiagnosisResult, error) {
	diagnosis := deploydiagnosis.RunDeployDiagnosis(runCtx, req)
	if status := strings.TrimSpace(diagnosis.HelmStatus); status != "" {
		if _, err := fmt.Fprintf(runCtx.Stdout, "== Helm release status ==\n%s\n\n", status); err != nil {
			return diagnosis, err
		}
	}
	if diagnosis.ClusterUnreachable {
		return diagnosis, writeDoctorPodUnreachableSkip(runCtx, "Pods")
	}
	if pods := strings.TrimSpace(diagnosis.Pods); pods != "" {
		if _, err := fmt.Fprintf(runCtx.Stdout, "== Pods ==\n%s\n\n", pods); err != nil {
			return diagnosis, err
		}
	}
	return diagnosis, nil
}

// writeDoctorPodUnreachableSkip avoids another kubectl timeout on a cluster
// already known unreachable.
func writeDoctorPodUnreachableSkip(runCtx core.Context, header string) error {
	_, err := fmt.Fprintf(runCtx.Stdout, "== %s ==\nskipped: the runtime pod is not reachable for this check; see the helm release status and pod state reported above for why, then retry once it is running.\n\n", header)
	return err
}

// writeDoctorInspection reports docker storage. No build daemon is a report,
// not a failure, unless a prune was requested against it.
func writeDoctorInspection(runCtx core.Context, target config.OpenResult, req config.ShellLaunchParams, pruneRequested bool) error {
	inspection, err := doctor.RunDoctorInspection(runCtx, nil, req)
	var unavailable model.DoctorDockerDaemonUnavailableError
	daemonUnavailable := errors.As(err, &unavailable)
	if daemonUnavailable && pruneRequested {
		return err
	}
	if err != nil && !daemonUnavailable {
		return err
	}
	if runCtx.DryRun {
		return nil
	}
	if _, err := fmt.Fprintf(runCtx.Stdout, "Target: %s/%s\n", target.Tenant, target.Environment); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(runCtx.Stdout, doctor.DoctorDockerDaemonLine(inspection.Daemon)); err != nil {
		return err
	}
	return writeDoctorOutput(runCtx, inspection.Stdout, inspection.Stderr)
}

// runDoctorRecoveryToolActions mutates the live release.
func runDoctorRecoveryToolActions(runCtx core.Context, input DoctorInput, req config.ShellLaunchParams) error {
	for _, action := range deployRecoveryActionsFromInput(input) {
		if !runCtx.DryRun {
			if _, err := fmt.Fprintf(runCtx.Stdout, "Running: %s\n", doctor.DeployRecoveryActionDescription(action)); err != nil {
				return err
			}
		}
		output, err := doctor.RunDeployRecovery(runCtx, req, action)
		if err != nil {
			return err
		}
		if !runCtx.DryRun {
			if err := writeDoctorOutput(runCtx, output, ""); err != nil {
				return err
			}
		}
	}
	return nil
}

func deployRecoveryActionsFromInput(input DoctorInput) []model.DeployRecoveryAction {
	actions := make([]model.DeployRecoveryAction, 0, 2)
	if input.ClearPendingHelm {
		actions = append(actions, model.DeployRecoveryClearPendingHelm)
	}
	if input.Rollback {
		actions = append(actions, model.DeployRecoveryRollback)
	}
	return actions
}

func runDoctorToolActions(runCtx core.Context, input DoctorInput, req config.ShellLaunchParams) error {
	for _, action := range doctorActionsFromInput(input) {
		if err := writeDoctorAction(runCtx, req, action); err != nil {
			return err
		}
		output, err := doctor.RunDoctorAction(runCtx, nil, req, action)
		if err != nil {
			return err
		}
		if !runCtx.DryRun {
			if err := writeDoctorOutput(runCtx, output.Stdout, output.Stderr); err != nil {
				return err
			}
			if summary := doctor.DoctorPruneSummary(output); summary != "" {
				if _, err := fmt.Fprintln(runCtx.Stdout, summary); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// writeDoctorAction names the daemon too, since acting on the wrong one reclaims nothing.
func writeDoctorAction(runCtx core.Context, req config.ShellLaunchParams, action model.DoctorAction) error {
	if runCtx.DryRun {
		return nil
	}
	_, err := fmt.Fprintf(runCtx.Stdout, "Running: %s\n",
		doctor.DoctorActionDescriptionOn(action, doctor.ResolveDoctorDockerDaemon(req)))
	return err
}

func writeDoctorOutput(runCtx core.Context, stdout, stderr string) error {
	if trimmed := strings.TrimSpace(stdout); trimmed != "" {
		if _, err := fmt.Fprintln(runCtx.Stdout, trimmed); err != nil {
			return err
		}
	}
	if trimmed := strings.TrimSpace(stderr); trimmed != "" {
		if _, err := fmt.Fprintln(runCtx.Stderr, trimmed); err != nil {
			return err
		}
	}
	return nil
}

// resolveDoctorOpenResult pins tenant/environment to this pod's environment.
func resolveDoctorOpenResult(runtime runtimecfg.RuntimeConfig, input DoctorInput) (config.OpenResult, error) {
	tenant, environment, err := runtimecfg.ResolveLocalTarget(runtime, input.Tenant, input.Environment)
	if err != nil {
		return config.OpenResult{}, err
	}
	return diagnosispkg.ResolveDoctorTarget(runtime.Store, model.OpenParams{Tenant: tenant, Environment: environment})
}

func doctorActionsFromInput(input DoctorInput) []model.DoctorAction {
	actions := make([]model.DoctorAction, 0, 3)
	if input.PruneImages {
		actions = append(actions, model.DoctorActionPruneImages)
	}
	if input.PruneBuildCache {
		actions = append(actions, model.DoctorActionPruneBuildCache)
	}
	if input.PruneContainers {
		actions = append(actions, model.DoctorActionPruneContainers)
	}
	return actions
}

// runDoctorConfigSync applies without a prompt; the request is the confirmation.
func runDoctorConfigSync(runCtx core.Context) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	inspection, err := doctor.InspectRuntimeConfigSync(doctor.ResolveRuntimeConfigHome(homeDir), os.Getenv)
	if err != nil {
		return err
	}
	if !inspection.HasInjected {
		return errors.New("cannot reconcile the in-pod config: ERUN_TENANT/ERUN_ENVIRONMENT are unset, so this is not a runtime pod")
	}
	if inspection.InSync() {
		runCtx.Trace("doctor: in-pod config matches the injected env; nothing to reconcile")
		return nil
	}
	for _, field := range inspection.Drift {
		runCtx.Trace(fmt.Sprintf("doctor: in-pod config drift %s %s on-disk=%q injected=%q [%s]",
			field.Scope, field.Key, field.OnDisk, field.Injected, field.Kind))
	}
	return doctor.RunRuntimeConfigSync(runCtx, inspection)
}
