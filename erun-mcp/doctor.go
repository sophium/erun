package erunmcp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

type DoctorInput struct {
	Tenant                     string                   `json:"tenant,omitempty" jsonschema:"optional explicit tenant override"`
	Environment                string                   `json:"environment,omitempty" jsonschema:"optional explicit environment override"`
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
}

// DoctorRepairAliasInput is the MCP equivalent of the interactive
// "Re-initialize cloud provider alias?" prompt, shaped so a caller can
// stitch it from the structured RootConfigInspection of a previous run.
type DoctorRepairAliasInput struct {
	Alias         string `json:"alias" jsonschema:"orphaned cloud provider alias (username+account@provider) to recreate"`
	SSOStartURL   string `json:"ssoStartUrl" jsonschema:"AWS IAM Identity Center start URL"`
	SSORegion     string `json:"ssoRegion" jsonschema:"AWS IAM Identity Center region"`
	RoleName      string `json:"roleName,omitempty" jsonschema:"AWS permission set (IAM Identity Center) used during SSO login"`
	Region        string `json:"region,omitempty" jsonschema:"default AWS region for the recreated provider (derived from referenced cloud context when blank)"`
	OIDCIssuerURL string `json:"oidcIssuerUrl,omitempty" jsonschema:"override for the OIDC issuer URL; usually inferred from AWS web identity token"`
	SkipLogin     bool   `json:"skipLogin,omitempty" jsonschema:"when true, skip aws sso login and rely on existing credentials"`
}

// DoctorRootConfigReport pairs the inspection with the call's repair
// outcomes so an LLM agent can decide whether to retry with full
// per-alias init params or escalate to the user.
type DoctorRootConfigReport struct {
	Inspection                  eruncommon.RootConfigInspection `json:"inspection"`
	RestoredFromBackup          string                          `json:"restoredFromBackup,omitempty"`
	RestoredEnvConfigFromBackup string                          `json:"restoredEnvConfigFromBackup,omitempty"`
	RepairedAliases             []string                        `json:"repairedAliases,omitempty"`
	UnresolvedAliases           []string                        `json:"unresolvedAliases,omitempty"`
}

func doctorTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, DoctorInput) (*mcp.CallToolResult, CommandOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input DoctorInput) (*mcp.CallToolResult, CommandOutput, error) {
		var report *DoctorRootConfigReport
		output, err := runRuntimeCommand(runtime, input.Preview, input.Verbosity, func(runCtx eruncommon.Context, _ string) error {
			r, runErr := runDoctorToolCommand(runtime, input, runCtx)
			report = r
			return runErr
		})
		if report != nil {
			output.RootConfig = report
		}
		return nil, output, err
	}
}

func runDoctorToolCommand(runtime RuntimeConfig, input DoctorInput, runCtx eruncommon.Context) (*DoctorRootConfigReport, error) {
	if input.ClearPendingHelm && input.Rollback {
		return nil, errors.New("clearPendingHelm and rollback are alternative recoveries; request only one")
	}
	// Config drift is more fundamental than anything below it: a wrong projection
	// mis-drives every later resolution, so reconcile first and return.
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
	if selector := strings.TrimSpace(input.RestoreEnvConfigFromBackup); selector != "" {
		if err := restoreDoctorEnvConfigFromBackup(runCtx, input, selector, report); err != nil {
			return report, err
		}
		if onlyEnvConfigRestoreInput(input) {
			return report, nil
		}
	}
	if err := runDoctorTenantEnvActions(runtime, input, runCtx); err != nil {
		return report, err
	}
	return report, nil
}

// restoreDoctorEnvConfigFromBackup recovers a changed or corrupted env config
// from a backup before any tenant/env work runs against it.
func restoreDoctorEnvConfigFromBackup(runCtx eruncommon.Context, input DoctorInput, selector string, report *DoctorRootConfigReport) error {
	tenant := strings.TrimSpace(input.Tenant)
	environment := strings.TrimSpace(input.Environment)
	if tenant == "" || environment == "" {
		return errors.New("restoreEnvConfigFromBackup requires explicit tenant and environment")
	}
	livePath, err := eruncommon.EnvConfigPath(tenant, environment)
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
		if err := eruncommon.RestoreEnvConfigFromBackup(backup.Path, tenant, environment); err != nil {
			return err
		}
	}
	runCtx.TraceCommand("", "cp", backup.Path, livePath)
	report.RestoredEnvConfigFromBackup = backup.Path
	return nil
}

func availableEnvBackupDates(tenant, environment string) string {
	backups, err := eruncommon.ListEnvConfigBackups(tenant, environment)
	if err != nil || len(backups) == 0 {
		return ""
	}
	dates := make([]string, 0, len(backups))
	for _, backup := range backups {
		dates = append(dates, backup.Date.Format("2006-01-02"))
	}
	return strings.Join(dates, ", ")
}

func resolveDoctorEnvBackupSelector(tenant, environment, selector string) (eruncommon.ConfigBackup, bool, error) {
	if strings.ContainsAny(selector, "/\\") {
		return eruncommon.ConfigBackup{Path: selector}, true, nil
	}
	return eruncommon.FindEnvConfigBackupByDate(tenant, environment, selector)
}

func onlyEnvConfigRestoreInput(input DoctorInput) bool {
	return strings.TrimSpace(input.RestoreEnvConfigFromBackup) != "" &&
		len(input.RepairOrphanedAliases) == 0 &&
		strings.TrimSpace(input.RestoreConfigFromBackup) == "" &&
		!input.PruneImages && !input.PruneBuildCache && !input.PruneContainers &&
		!input.ClearPendingHelm && !input.Rollback
}

func runDoctorTenantEnvActions(runtime RuntimeConfig, input DoctorInput, runCtx eruncommon.Context) error {
	target, err := resolveDoctorOpenResult(runtime, input)
	if err != nil {
		return err
	}
	req := eruncommon.ShellLaunchParamsFromResult(target)
	if err := writeDoctorDeployDiagnosis(runCtx, req); err != nil {
		return err
	}
	if err := runDoctorRecoveryToolActions(runCtx, input, req); err != nil {
		return err
	}
	if err := writeDoctorInspection(runCtx, target, req); err != nil {
		return err
	}
	return runDoctorToolActions(runCtx, input, req)
}

// onlyRootConfigDoctorInput mirrors the CLI's doctorOnlyRepairConfig: when the
// run is scoped to root-config repair, skip the tenant/env fall-through, since
// resolveDoctorOpenResult would fail on the very dangling alias the caller
// asked us to fix.
func onlyRootConfigDoctorInput(input DoctorInput) bool {
	if len(input.RepairOrphanedAliases) == 0 && strings.TrimSpace(input.RestoreConfigFromBackup) == "" {
		return false
	}
	return !input.PruneImages && !input.PruneBuildCache && !input.PruneContainers
}

// runDoctorRootConfigToolFlow returns fatal=true when the root config is in a
// state that must stop the flow before tenant/env work even when more was
// requested — for example, the file is corrupted and no repair input was supplied.
func runDoctorRootConfigToolFlow(runtime RuntimeConfig, input DoctorInput, runCtx eruncommon.Context) (*DoctorRootConfigReport, bool, error) {
	inspection, err := eruncommon.InspectRootConfig(runtime.Store)
	if err != nil {
		return nil, false, err
	}
	report := &DoctorRootConfigReport{Inspection: inspection}

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

func restoreDoctorRootConfigFromBackup(runtime RuntimeConfig, runCtx eruncommon.Context, inspection eruncommon.RootConfigInspection, selector string, report *DoctorRootConfigReport) (eruncommon.RootConfigInspection, error) {
	backup, ok, err := resolveDoctorBackupSelector(inspection, selector)
	if err != nil {
		return inspection, err
	}
	if !ok {
		return inspection, fmt.Errorf("no root config backup matches %q", selector)
	}
	if !runCtx.DryRun {
		if err := eruncommon.RestoreRootConfigFromBackup(backup.Path, inspection.ConfigPath); err != nil {
			return inspection, err
		}
	}
	runCtx.TraceCommand("", "cp", backup.Path, inspection.ConfigPath)
	report.RestoredFromBackup = backup.Path
	return refreshDoctorInspection(runtime, report)
}

func refreshDoctorInspection(runtime RuntimeConfig, report *DoctorRootConfigReport) (eruncommon.RootConfigInspection, error) {
	refreshed, err := eruncommon.InspectRootConfig(runtime.Store)
	if err != nil {
		return eruncommon.RootConfigInspection{}, err
	}
	report.Inspection = refreshed
	return refreshed, nil
}

func doctorRootConfigBlocks(inspection eruncommon.RootConfigInspection, input DoctorInput) bool {
	if inspection.ConfigStatus != eruncommon.RootConfigStatusOK {
		return true
	}
	return len(inspection.OrphanedAliases) > 0 && onlyRootConfigDoctorInput(input)
}

func runDoctorAliasRepairs(runtime RuntimeConfig, input DoctorInput, runCtx eruncommon.Context, inspection eruncommon.RootConfigInspection, report *DoctorRootConfigReport) error {
	byAlias := make(map[string]eruncommon.OrphanedAlias, len(inspection.OrphanedAliases))
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
		params := eruncommon.RepairOrphanedAliasParams{
			Orphan:        orphan,
			SSOStartURL:   strings.TrimSpace(entry.SSOStartURL),
			SSORegion:     strings.TrimSpace(entry.SSORegion),
			RoleName:      strings.TrimSpace(entry.RoleName),
			Region:        firstNonBlank(entry.Region, preferredRegionForOrphanAlias(orphan)),
			OIDCIssuerURL: strings.TrimSpace(entry.OIDCIssuerURL),
			SkipLogin:     entry.SkipLogin,
		}
		if _, err := eruncommon.RepairOrphanedAlias(runCtx, runtime.Store, params, eruncommon.CloudDependencies{}); err != nil {
			return fmt.Errorf("repair alias %s: %w", alias, err)
		}
		report.RepairedAliases = append(report.RepairedAliases, alias)
	}
	return nil
}

func resolveDoctorBackupSelector(inspection eruncommon.RootConfigInspection, selector string) (eruncommon.ConfigBackup, bool, error) {
	if strings.ContainsAny(selector, "/\\") {
		return eruncommon.ConfigBackup{Path: selector}, true, nil
	}
	backup, ok, err := eruncommon.FindRootConfigBackupByDate(inspection.ConfigPath, selector)
	if err != nil {
		return eruncommon.ConfigBackup{}, false, err
	}
	return backup, ok, nil
}

func preferredRegionForOrphanAlias(orphan eruncommon.OrphanedAlias) string {
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

// writeDoctorDeployDiagnosis reports helm release status and runtime pods so an
// agent can see why a deploy failed before any cleanup runs. Read-only.
func writeDoctorDeployDiagnosis(runCtx eruncommon.Context, req eruncommon.ShellLaunchParams) error {
	diagnosis := eruncommon.RunDeployDiagnosis(runCtx, req)
	if runCtx.DryRun {
		return nil
	}
	if status := strings.TrimSpace(diagnosis.HelmStatus); status != "" {
		if _, err := fmt.Fprintf(runCtx.Stdout, "== Helm release status ==\n%s\n\n", status); err != nil {
			return err
		}
	}
	if pods := strings.TrimSpace(diagnosis.Pods); pods != "" {
		if _, err := fmt.Fprintf(runCtx.Stdout, "== Pods ==\n%s\n\n", pods); err != nil {
			return err
		}
	}
	return nil
}

func writeDoctorInspection(runCtx eruncommon.Context, target eruncommon.OpenResult, req eruncommon.ShellLaunchParams) error {
	inspection, err := eruncommon.RunDoctorInspection(runCtx, nil, req)
	if err != nil || runCtx.DryRun {
		return err
	}
	if _, err := fmt.Fprintf(runCtx.Stdout, "Target: %s/%s\n", target.Tenant, target.Environment); err != nil {
		return err
	}
	return writeDoctorOutput(runCtx, inspection.Stdout, inspection.Stderr)
}

// runDoctorRecoveryToolActions runs the caller-requested deploy-recovery actions
// (clear pending helm / rollback). Mutates the live release.
func runDoctorRecoveryToolActions(runCtx eruncommon.Context, input DoctorInput, req eruncommon.ShellLaunchParams) error {
	for _, action := range deployRecoveryActionsFromInput(input) {
		if !runCtx.DryRun {
			if _, err := fmt.Fprintf(runCtx.Stdout, "Running: %s\n", eruncommon.DeployRecoveryActionDescription(action)); err != nil {
				return err
			}
		}
		output, err := eruncommon.RunDeployRecovery(runCtx, req, action)
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

func deployRecoveryActionsFromInput(input DoctorInput) []eruncommon.DeployRecoveryAction {
	actions := make([]eruncommon.DeployRecoveryAction, 0, 2)
	if input.ClearPendingHelm {
		actions = append(actions, eruncommon.DeployRecoveryClearPendingHelm)
	}
	if input.Rollback {
		actions = append(actions, eruncommon.DeployRecoveryRollback)
	}
	return actions
}

func runDoctorToolActions(runCtx eruncommon.Context, input DoctorInput, req eruncommon.ShellLaunchParams) error {
	for _, action := range doctorActionsFromInput(input) {
		if err := writeDoctorAction(runCtx, action); err != nil {
			return err
		}
		output, err := eruncommon.RunDoctorAction(runCtx, nil, req, action)
		if err != nil {
			return err
		}
		if !runCtx.DryRun {
			if err := writeDoctorOutput(runCtx, output.Stdout, output.Stderr); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeDoctorAction(runCtx eruncommon.Context, action eruncommon.DoctorAction) error {
	if runCtx.DryRun {
		return nil
	}
	_, err := fmt.Fprintf(runCtx.Stdout, "Running: %s\n", eruncommon.DoctorActionDescription(action))
	return err
}

func writeDoctorOutput(runCtx eruncommon.Context, stdout, stderr string) error {
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

func resolveDoctorOpenResult(runtime RuntimeConfig, input DoctorInput) (eruncommon.OpenResult, error) {
	tenant := strings.TrimSpace(input.Tenant)
	environment := strings.TrimSpace(input.Environment)
	switch {
	case tenant != "" && environment != "":
		return eruncommon.ResolveDoctorTarget(runtime.Store, eruncommon.OpenParams{
			Tenant:      tenant,
			Environment: environment,
		})
	case tenant != "":
		return eruncommon.ResolveDoctorTarget(runtime.Store, eruncommon.OpenParams{
			Tenant:                tenant,
			UseDefaultEnvironment: true,
		})
	case environment != "":
		return eruncommon.ResolveDoctorTarget(runtime.Store, eruncommon.OpenParams{
			Environment:      environment,
			UseDefaultTenant: true,
		})
	}

	runtimeTenant := strings.TrimSpace(runtime.Context.Tenant)
	runtimeEnvironment := strings.TrimSpace(runtime.Context.Environment)
	if runtimeTenant != "" && runtimeEnvironment != "" {
		return eruncommon.ResolveDoctorTarget(runtime.Store, eruncommon.OpenParams{
			Tenant:      runtimeTenant,
			Environment: runtimeEnvironment,
		})
	}

	return eruncommon.ResolveDoctorTarget(runtime.Store, eruncommon.OpenParams{
		UseDefaultTenant:      true,
		UseDefaultEnvironment: true,
	})
}

func doctorActionsFromInput(input DoctorInput) []eruncommon.DoctorAction {
	actions := make([]eruncommon.DoctorAction, 0, 3)
	if input.PruneImages {
		actions = append(actions, eruncommon.DoctorActionPruneImages)
	}
	if input.PruneBuildCache {
		actions = append(actions, eruncommon.DoctorActionPruneBuildCache)
	}
	if input.PruneContainers {
		actions = append(actions, eruncommon.DoctorActionPruneContainers)
	}
	return actions
}

// runDoctorConfigSync reconciles the in-pod config from the injected env. The
// request is itself the confirmation, so it applies without a further prompt.
func runDoctorConfigSync(runCtx eruncommon.Context) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	inspection, err := eruncommon.InspectRuntimeConfigSync(eruncommon.ResolveRuntimeConfigHome(homeDir), os.Getenv)
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
	return eruncommon.RunRuntimeConfigSync(runCtx, inspection)
}
