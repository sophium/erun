package erunmcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

type DoctorInput struct {
	Tenant                  string                       `json:"tenant,omitempty" jsonschema:"optional explicit tenant override"`
	Environment             string                       `json:"environment,omitempty" jsonschema:"optional explicit environment override"`
	PruneImages             bool                         `json:"pruneImages,omitempty" jsonschema:"when true, prune unused Docker images"`
	PruneBuildCache         bool                         `json:"pruneBuildCache,omitempty" jsonschema:"when true, prune unused BuildKit cache"`
	PruneContainers         bool                         `json:"pruneContainers,omitempty" jsonschema:"when true, prune stopped Docker containers"`
	Preview                 bool                         `json:"preview,omitempty" jsonschema:"when true, resolve and print the planned actions without executing them"`
	Verbosity               int                          `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
	RestoreConfigFromBackup string                       `json:"restoreConfigFromBackup,omitempty" jsonschema:"YYYY-MM-DD or absolute path; when set, restore the root erun config from the matching daily backup before any tenant/env work"`
	RepairOrphanedAliases   []DoctorRepairAliasInput     `json:"repairOrphanedAliases,omitempty" jsonschema:"per-alias AWS init parameters; when present, doctor re-initializes each listed cloud provider alias before tenant/env work"`
}

// DoctorRepairAliasInput is the MCP equivalent of the interactive
// "Re-initialize cloud provider alias <alias>?" prompt. Required
// fields are alias + SSO start URL + SSO region; the rest mirror
// InitAWSCloudProviderParams so callers can stitch one tool call to
// the structured RootConfigInspection emitted on a previous run.
type DoctorRepairAliasInput struct {
	Alias         string `json:"alias" jsonschema:"orphaned cloud provider alias (username+account@provider) to recreate"`
	SSOStartURL   string `json:"ssoStartUrl" jsonschema:"AWS IAM Identity Center start URL"`
	SSORegion     string `json:"ssoRegion" jsonschema:"AWS IAM Identity Center region"`
	RoleName      string `json:"roleName,omitempty" jsonschema:"AWS role name used during SSO login"`
	Region        string `json:"region,omitempty" jsonschema:"default AWS region for the recreated provider (derived from referenced cloud context when blank)"`
	OIDCIssuerURL string `json:"oidcIssuerUrl,omitempty" jsonschema:"override for the OIDC issuer URL; usually inferred from AWS web identity token"`
	SkipLogin     bool   `json:"skipLogin,omitempty" jsonschema:"when true, skip aws sso login and rely on existing credentials"`
}

// DoctorRootConfigReport pairs the inspection with whatever repair
// outcomes happened in the current call. Returned in the structured
// output so an LLM agent can decide whether to retry with full
// per-alias init params or escalate to the user.
type DoctorRootConfigReport struct {
	Inspection         eruncommon.RootConfigInspection `json:"inspection"`
	RestoredFromBackup string                          `json:"restoredFromBackup,omitempty"`
	RepairedAliases    []string                        `json:"repairedAliases,omitempty"`
	UnresolvedAliases  []string                        `json:"unresolvedAliases,omitempty"`
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
	report, fatal, err := runDoctorRootConfigToolFlow(runtime, input, runCtx)
	if err != nil {
		return report, err
	}
	if fatal {
		return report, nil
	}
	if onlyRootConfigDoctorInput(input) {
		return report, nil
	}
	target, err := resolveDoctorOpenResult(runtime, input)
	if err != nil {
		return report, err
	}
	req := eruncommon.ShellLaunchParamsFromResult(target)
	if err := writeDoctorDeployDiagnosis(runCtx, req); err != nil {
		return report, err
	}
	if err := writeDoctorInspection(runCtx, target, req); err != nil {
		return report, err
	}
	if err := runDoctorToolActions(runCtx, input, req); err != nil {
		return report, err
	}
	return report, nil
}

// onlyRootConfigDoctorInput mirrors the CLI's doctorOnlyRepairConfig:
// when the caller asked exclusively for root-config work (restore or
// repair) and no tenant/env action flags are set, do not fall through
// to resolveDoctorOpenResult — that resolver would otherwise fail on
// the same dangling alias the caller just asked us to fix.
func onlyRootConfigDoctorInput(input DoctorInput) bool {
	if len(input.RepairOrphanedAliases) == 0 && strings.TrimSpace(input.RestoreConfigFromBackup) == "" {
		return false
	}
	return !input.PruneImages && !input.PruneBuildCache && !input.PruneContainers
}

// runDoctorRootConfigToolFlow handles inspection, optional
// restore-from-backup, and any per-alias repair requested by the
// caller. Returns the structured report so the outer tool wrapper
// can attach it to the CommandOutput.
//
// fatal=true means the root config is in a state where we should not
// proceed to tenant/env work even when more was requested — for
// example, the file is corrupted and no repair input was supplied.
func runDoctorRootConfigToolFlow(runtime RuntimeConfig, input DoctorInput, runCtx eruncommon.Context) (*DoctorRootConfigReport, bool, error) {
	inspection, err := eruncommon.InspectRootConfig(runtime.Store)
	if err != nil {
		return nil, false, err
	}
	report := &DoctorRootConfigReport{Inspection: inspection}

	if selector := strings.TrimSpace(input.RestoreConfigFromBackup); selector != "" {
		backup, ok, err := resolveDoctorBackupSelector(inspection, selector)
		if err != nil {
			return report, true, err
		}
		if !ok {
			return report, true, fmt.Errorf("no root config backup matches %q", selector)
		}
		if !runCtx.DryRun {
			if err := eruncommon.RestoreRootConfigFromBackup(backup.Path, inspection.ConfigPath); err != nil {
				return report, true, err
			}
		}
		runCtx.TraceCommand("", "cp", backup.Path, inspection.ConfigPath)
		report.RestoredFromBackup = backup.Path
		refreshed, refreshErr := eruncommon.InspectRootConfig(runtime.Store)
		if refreshErr != nil {
			return report, true, refreshErr
		}
		report.Inspection = refreshed
		inspection = refreshed
	}

	if len(input.RepairOrphanedAliases) > 0 {
		if err := runDoctorAliasRepairs(runtime, input, runCtx, inspection, report); err != nil {
			return report, true, err
		}
		refreshed, refreshErr := eruncommon.InspectRootConfig(runtime.Store)
		if refreshErr != nil {
			return report, true, refreshErr
		}
		report.Inspection = refreshed
		inspection = refreshed
	}

	if inspection.ConfigStatus != eruncommon.RootConfigStatusOK {
		return report, true, nil
	}
	if len(inspection.OrphanedAliases) > 0 && onlyRootConfigDoctorInput(input) {
		// The caller scoped the work to root-config repair but did not
		// supply enough input to finish it; surface the remaining
		// orphans so the agent can re-call with the missing aliases.
		return report, true, nil
	}
	return report, false, nil
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

func resolveDoctorBackupSelector(inspection eruncommon.RootConfigInspection, selector string) (eruncommon.RootConfigBackup, bool, error) {
	if strings.ContainsAny(selector, "/\\") {
		return eruncommon.RootConfigBackup{Path: selector}, true, nil
	}
	backup, ok, err := eruncommon.FindRootConfigBackupByDate(inspection.ConfigPath, selector)
	if err != nil {
		return eruncommon.RootConfigBackup{}, false, err
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

// writeDoctorDeployDiagnosis reports the helm release status and runtime pods
// so an agent can see why a deploy failed before any cleanup. Read-only; the
// commands are traced for dry-run previews.
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
