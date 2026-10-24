package doctor

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/sophium/erun/cmd/shared/cloudcli"
	"github.com/sophium/erun/cmd/shared/prompt"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud"
)

func rootConfigSkipsInspection(inspection model.RootConfigInspection, options doctorOptions) bool {
	if !inspection.Complete() {
		return false
	}
	if options.repairConfig {
		return false
	}
	return strings.TrimSpace(options.restoreConfigFromBackup) == ""
}

func runRootConfigDoctor(ctx core.Context, configStore config.ConfigStore, cloudDeps cloud.CloudDependencies, cloudContextDeps cloud.CloudContextDependencies, promptRunner prompt.PromptRunner, options doctorOptions) (bool, error) {
	inspection, err := config.InspectRootConfig(configStore)
	if err != nil {
		return false, err
	}
	if rootConfigSkipsInspection(inspection, options) {
		return false, nil
	}
	if err := writeRootConfigInspectionReport(ctx, inspection); err != nil {
		return false, err
	}
	inspection, handled, err := runRootConfigRestoreSelector(ctx, configStore, inspection, options)
	if err != nil {
		return handled, err
	}
	if handled && inspection.Complete() {
		return true, nil
	}
	shouldRepair, err := shouldOfferRootConfigRepair(ctx, promptRunner, inspection, options)
	if err != nil {
		return handled, err
	}
	if !shouldRepair {
		return handled, nil
	}
	repaired, err := runRootConfigRepair(ctx, configStore, cloudDeps, cloudContextDeps, promptRunner, inspection)
	return handled || repaired, err
}

func runRootConfigRestoreSelector(ctx core.Context, configStore config.ConfigStore, inspection model.RootConfigInspection, options doctorOptions) (model.RootConfigInspection, bool, error) {
	selector := strings.TrimSpace(options.restoreConfigFromBackup)
	if selector == "" {
		return inspection, false, nil
	}
	restored, err := runRootConfigRestoreFromBackup(ctx, inspection, selector)
	if err != nil {
		return inspection, false, err
	}
	if !restored {
		return inspection, false, nil
	}
	refreshed, err := config.InspectRootConfig(configStore)
	if err != nil {
		return inspection, true, err
	}
	if err := writeRootConfigInspectionReport(ctx, refreshed); err != nil {
		return refreshed, true, err
	}
	return refreshed, true, nil
}

func writeRootConfigInspectionReport(ctx core.Context, inspection model.RootConfigInspection) error {
	w := newLineWriter(ctx.Stdout)
	w.Linef("Root config: %s (status=%s)", emptyIfBlank(inspection.ConfigPath), inspection.ConfigStatus)
	if inspection.ConfigError != "" {
		w.Linef("  load error: %s", inspection.ConfigError)
	}
	writeRootConfigInspectionCounts(w, inspection)
	writeRootConfigInspectionOrphans(w, inspection)
	writeRootConfigInspectionBackups(w, inspection)
	return w.Err()
}

func writeRootConfigInspectionCounts(w *lineWriter, inspection model.RootConfigInspection) {
	if inspection.ConfigStatus != model.RootConfigStatusOK {
		return
	}
	w.Linef("  cloud providers configured: %d", inspection.ConfiguredCount)
	w.Linef("  cloud contexts: %d, tenants: %d", inspection.CloudContextHits, inspection.TenantHits)
}

func writeRootConfigInspectionOrphans(w *lineWriter, inspection model.RootConfigInspection) {
	switch {
	case len(inspection.OrphanedAliases) > 0:
		w.Linef("  orphaned aliases: %d", len(inspection.OrphanedAliases))
		for _, orphan := range inspection.OrphanedAliases {
			writeOrphanedAliasReport(w, orphan)
		}
	case inspection.ConfigStatus == model.RootConfigStatusOK && len(inspection.OrphanedContexts) == 0:
		w.Linef("  no orphaned cloud-provider aliases.")
	}
	if len(inspection.OrphanedContexts) > 0 {
		w.Linef("  orphaned cloud contexts: %d", len(inspection.OrphanedContexts))
		for _, orphan := range inspection.OrphanedContexts {
			writeOrphanedCloudContextReport(w, orphan)
		}
	}
}

func writeOrphanedCloudContextReport(w *lineWriter, orphan model.OrphanedCloudContext) {
	w.Linef("    - %s", orphan.KubernetesContext)
	if orphan.AccountID != "" && orphan.Region != "" {
		w.Linef("        account=%s region=%s alias=%s", orphan.AccountID, orphan.Region, orphan.CloudProviderAlias)
	} else {
		w.Linef("        cannot decode account/region from name; AWS auto-recovery unavailable")
	}
	if len(orphan.ReferencedByEnvs) > 0 {
		names := make([]string, 0, len(orphan.ReferencedByEnvs))
		for _, ref := range orphan.ReferencedByEnvs {
			names = append(names, ref.Tenant+"/"+ref.Environment)
		}
		w.Linef("        envs: %s", strings.Join(names, ", "))
	}
}

func writeRootConfigInspectionBackups(w *lineWriter, inspection model.RootConfigInspection) {
	if len(inspection.Backups) == 0 {
		return
	}
	w.Linef("  available backups (newest first):")
	for _, backup := range inspection.Backups {
		// A separate path= field keeps the integration <TMP> normalizer from eating a paren.
		w.Linef("    - %s path=%s", backup.Date.Format("2006-01-02"), backup.Path)
	}
}

func writeOrphanedAliasReport(w *lineWriter, orphan model.OrphanedAlias) {
	w.Linef("    - %s", orphan.Alias)
	if orphan.Parsed {
		w.Linef("        provider=%s account=%s user=%s", orphan.Provider, orphan.AccountID, orphan.Username)
	} else {
		w.Linef("        alias is malformed; cannot auto-seed an init")
	}
	if len(orphan.ReferencedByTenants) > 0 {
		w.Linef("        tenants: %s", strings.Join(orphan.ReferencedByTenants, ", "))
	}
	if len(orphan.ReferencedByCloudContexts) > 0 {
		w.Linef("        cloud contexts: %s", formatOrphanedContexts(orphan.ReferencedByCloudContexts))
	}
}

func formatOrphanedContexts(refs []model.OrphanedAliasContextRef) string {
	names := make([]string, 0, len(refs))
	for _, ref := range refs {
		if strings.TrimSpace(ref.Region) != "" {
			names = append(names, fmt.Sprintf("%s (%s)", ref.Name, ref.Region))
			continue
		}
		names = append(names, ref.Name)
	}
	return strings.Join(names, ", ")
}

// On a terminal, enter repair whenever something needs fixing; the per-alias prompt confirms.
func shouldOfferRootConfigRepair(ctx core.Context, promptRunner prompt.PromptRunner, inspection model.RootConfigInspection, options doctorOptions) (bool, error) {
	if inspection.Complete() {
		return false, nil
	}
	if options.repairConfig {
		return true, nil
	}
	if ctx.DryRun || promptRunner == nil {
		_, err := fmt.Fprintln(ctx.Stdout, "Run `erun doctor --repair-config` to walk through restoring the root config or re-initializing orphaned cloud provider aliases.")
		return false, err
	}
	return true, nil
}

func runRootConfigRepair(ctx core.Context, configStore config.ConfigStore, cloudDeps cloud.CloudDependencies, cloudContextDeps cloud.CloudContextDependencies, promptRunner prompt.PromptRunner, inspection model.RootConfigInspection) (bool, error) {
	inspection, restored, err := runRootConfigRepairRestore(ctx, configStore, promptRunner, inspection)
	if err != nil {
		return restored, err
	}
	if restored && inspection.Complete() {
		return true, nil
	}
	if inspection.ConfigStatus != model.RootConfigStatusOK {
		_, err := fmt.Fprintln(ctx.Stdout, "Root config is not loadable and no backup was restored; resolve the file manually (or supply --restore-config-from-backup <date>) and re-run.")
		return restored, err
	}
	return runRootConfigRepairOrphans(ctx, configStore, cloudDeps, cloudContextDeps, promptRunner, inspection, restored)
}

func runRootConfigRepairOrphans(ctx core.Context, configStore config.ConfigStore, cloudDeps cloud.CloudDependencies, cloudContextDeps cloud.CloudContextDependencies, promptRunner prompt.PromptRunner, inspection model.RootConfigInspection, restored bool) (bool, error) {
	repaired, err := runRootConfigRepairAliases(ctx, configStore, cloudDeps, promptRunner, inspection)
	if err != nil {
		return restored || repaired, err
	}
	refreshed, refreshErr := config.InspectRootConfig(configStore)
	if refreshErr != nil {
		return restored || repaired, refreshErr
	}
	recoveredContexts, err := runRootConfigRepairContexts(ctx, configStore, cloudContextDeps, promptRunner, refreshed)
	return restored || repaired || recoveredContexts, err
}

func runRootConfigRepairRestore(ctx core.Context, configStore config.ConfigStore, promptRunner prompt.PromptRunner, inspection model.RootConfigInspection) (model.RootConfigInspection, bool, error) {
	if inspection.ConfigStatus == model.RootConfigStatusOK || len(inspection.Backups) == 0 {
		return inspection, false, nil
	}
	restored, err := offerRootConfigBackupRestore(ctx, promptRunner, inspection)
	if err != nil || !restored {
		return inspection, false, err
	}
	refreshed, err := config.InspectRootConfig(configStore)
	if err != nil {
		return inspection, true, err
	}
	if err := writeRootConfigInspectionReport(ctx, refreshed); err != nil {
		return refreshed, true, err
	}
	return refreshed, true, nil
}

func runRootConfigRepairAliases(ctx core.Context, configStore config.ConfigStore, cloudDeps cloud.CloudDependencies, promptRunner prompt.PromptRunner, inspection model.RootConfigInspection) (bool, error) {
	handled := false
	for _, orphan := range inspection.OrphanedAliases {
		repaired, err := offerOrphanedAliasRepair(ctx, configStore, cloudDeps, promptRunner, orphan)
		if err != nil {
			return handled, err
		}
		if repaired {
			handled = true
		}
	}
	return handled, nil
}

func runRootConfigRepairContexts(ctx core.Context, configStore config.ConfigStore, cloudContextDeps cloud.CloudContextDependencies, promptRunner prompt.PromptRunner, inspection model.RootConfigInspection) (bool, error) {
	handled := false
	for _, orphan := range inspection.OrphanedContexts {
		recovered, err := offerOrphanedCloudContextRecovery(ctx, configStore, cloudContextDeps, promptRunner, orphan)
		if err != nil {
			return handled, err
		}
		if recovered {
			handled = true
		}
	}
	return handled, nil
}

func offerOrphanedCloudContextRecovery(ctx core.Context, configStore config.ConfigStore, cloudContextDeps cloud.CloudContextDependencies, promptRunner prompt.PromptRunner, orphan model.OrphanedCloudContext) (bool, error) {
	if reason, blocked := contextRecoveryBlockedReason(orphan); blocked {
		_, err := fmt.Fprintln(ctx.Stdout, reason)
		return false, err
	}
	if !ctx.DryRun && promptRunner == nil {
		_, err := fmt.Fprintf(ctx.Stdout, "Cannot recover cloud context %q non-interactively; re-run with a TTY or use --restore-config-from-backup <date>\n", orphan.KubernetesContext)
		return false, err
	}
	if !ctx.DryRun {
		label := fmt.Sprintf("Recover cloud context %s from AWS (account %s, region %s)?", orphan.KubernetesContext, orphan.AccountID, orphan.Region)
		ok, err := doctorConfirm(ctx, promptRunner, label, "Re-run with --restore-config-from-backup <date> to restore the config non-interactively.")
		if err != nil || !ok {
			return false, err
		}
	}
	params := model.RecoverCloudContextParams{
		KubernetesContext:  orphan.KubernetesContext,
		CloudProviderAlias: orphan.CloudProviderAlias,
		Region:             orphan.Region,
	}
	if _, err := fmt.Fprintf(ctx.Stdout, "Recovering cloud context %s...\n", orphan.KubernetesContext); err != nil {
		return false, err
	}
	result, err := cloud.RecoverCloudContextFromAWS(ctx, configStore, params, cloudContextDeps)
	if err != nil {
		_, _ = fmt.Fprintf(ctx.Stdout, "Recovery failed for %s: %v\n", orphan.KubernetesContext, err)
		return false, nil
	}
	writeCloudContextRecoverySummary(ctx, result)
	return true, nil
}

func contextRecoveryBlockedReason(orphan model.OrphanedCloudContext) (string, bool) {
	if strings.TrimSpace(orphan.AccountID) == "" || strings.TrimSpace(orphan.Region) == "" {
		return fmt.Sprintf("Skipping context %q: name does not match the erun-<seq>-<account>-<region> shape, so AWS recovery cannot be auto-seeded", orphan.KubernetesContext), true
	}
	if strings.TrimSpace(orphan.CloudProviderAlias) == "" {
		return fmt.Sprintf("Skipping context %q: env references it without a cloud provider alias, so AWS recovery cannot be authenticated", orphan.KubernetesContext), true
	}
	return "", false
}

func writeCloudContextRecoverySummary(ctx core.Context, result model.RecoverCloudContextResult) {
	w := newLineWriter(ctx.Stdout)
	w.Linef("Recovered cloud context %s from %s.", result.Saved.KubernetesContext, result.Source)
	w.Linef("  instance: %s (%s)", result.Saved.InstanceID, result.Saved.InstanceType)
	if result.Saved.PublicIP != "" {
		w.Linef("  public ip: %s", result.Saved.PublicIP)
	}
	if result.Saved.DiskType != "" || result.Saved.DiskSizeGB != 0 {
		w.Linef("  disk: %s %dG", result.Saved.DiskType, result.Saved.DiskSizeGB)
	}
	switch result.TokenFrom {
	case "kubeconfig":
		w.Linef("  admin token: restored from ~/.kube/config")
	case "input":
		w.Linef("  admin token: from --admin-token input")
	case "none":
		w.Linef("  admin token: NOT recovered — kubectl access will fail until you run `erun context init` for a fresh token or manually paste the existing one.")
	}
	_ = w.Err()
}

func offerRootConfigBackupRestore(ctx core.Context, promptRunner prompt.PromptRunner, inspection model.RootConfigInspection) (bool, error) {
	backup := inspection.Backups[0]
	if promptRunner == nil {
		_, err := fmt.Fprintln(ctx.Stdout, "Non-interactive run: pass --restore-config-from-backup "+backup.Date.Format("2006-01-02")+" to restore.")
		return false, err
	}
	label := fmt.Sprintf("Restore root config from backup %s path=%s?", backup.Date.Format("2006-01-02"), backup.Path)
	ok, err := doctorConfirm(ctx, promptRunner, label, "Re-run with --restore-config-from-backup "+backup.Date.Format("2006-01-02")+" to restore it without a prompt.")
	if err != nil || !ok {
		return false, err
	}
	return runRootConfigRestore(ctx, inspection.ConfigPath, backup)
}

func runRootConfigRestoreFromBackup(ctx core.Context, inspection model.RootConfigInspection, selector string) (bool, error) {
	backup, ok, err := resolveRootConfigBackupSelector(inspection, selector)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, fmt.Errorf("no root config backup matches %q", selector)
	}
	return runRootConfigRestore(ctx, inspection.ConfigPath, backup)
}

func resolveRootConfigBackupSelector(inspection model.RootConfigInspection, selector string) (model.ConfigBackup, bool, error) {
	if strings.ContainsAny(selector, "/\\") {
		return model.ConfigBackup{Path: selector}, true, nil
	}
	parsed, err := time.Parse("2006-01-02", selector)
	if err != nil {
		return model.ConfigBackup{}, false, fmt.Errorf("invalid backup date %q (expected YYYY-MM-DD or an absolute path)", selector)
	}
	for _, backup := range inspection.Backups {
		if backup.Date.Equal(parsed) {
			return backup, true, nil
		}
	}
	return model.ConfigBackup{}, false, nil
}

func runRootConfigRestore(ctx core.Context, livePath string, backup model.ConfigBackup) (bool, error) {
	ctx.TraceCommand("", "cp", backup.Path, livePath)
	if ctx.DryRun {
		_, err := fmt.Fprintf(ctx.Stdout, "Dry-run: would restore root config from %s\n", backup.Path)
		return true, err
	}
	if err := config.RestoreRootConfigFromBackup(backup.Path, livePath); err != nil {
		return false, err
	}
	_, err := fmt.Fprintf(ctx.Stdout, "Restored root config from %s\n", backup.Path)
	return true, err
}

func offerOrphanedAliasRepair(ctx core.Context, configStore config.ConfigStore, cloudDeps cloud.CloudDependencies, promptRunner prompt.PromptRunner, orphan model.OrphanedAlias) (bool, error) {
	if reason, blocked := orphanRepairBlockedReason(orphan); blocked {
		_, err := fmt.Fprintln(ctx.Stdout, reason)
		return false, err
	}
	if ctx.DryRun {
		return runOrphanRepairDryRun(ctx, orphan)
	}
	if promptRunner == nil {
		_, err := fmt.Fprintf(ctx.Stdout, "Cannot repair alias %q non-interactively; re-run with a TTY or run `erun cloud init aws` directly\n", orphan.Alias)
		return false, err
	}
	ok, err := doctorConfirm(ctx, promptRunner, fmt.Sprintf("Re-initialize cloud provider alias %s (account %s, user %s)?", orphan.Alias, orphan.AccountID, orphan.Username),
		"Run `erun cloud init aws` directly to re-initialize it without a prompt.")
	if err != nil || !ok {
		return false, err
	}
	return runOrphanRepair(ctx, configStore, cloudDeps, promptRunner, orphan)
}

func orphanRepairBlockedReason(orphan model.OrphanedAlias) (string, bool) {
	if !orphan.Parsed {
		return fmt.Sprintf("Skipping malformed alias %q; recreate it manually with `erun cloud init aws`", orphan.Alias), true
	}
	if orphan.Provider != config.CloudProviderAWS {
		return fmt.Sprintf("Skipping alias %q: provider %q is not supported by repair flow", orphan.Alias, orphan.Provider), true
	}
	return "", false
}

// Dry-run traces intent only; the real flow prompts on stdin.
func runOrphanRepairDryRun(ctx core.Context, orphan model.OrphanedAlias) (bool, error) {
	region := preferredRegionForOrphan(orphan)
	ctx.Trace(fmt.Sprintf("doctor repair-config: would re-init aws cloud provider alias=%s account=%s user=%s region=%s", orphan.Alias, orphan.AccountID, orphan.Username, region))
	_, err := fmt.Fprintf(ctx.Stdout, "Dry-run: would re-initialize AWS provider for alias %s (account %s, user %s)\n", orphan.Alias, orphan.AccountID, orphan.Username)
	return true, err
}

func runOrphanRepair(ctx core.Context, configStore config.ConfigStore, cloudDeps cloud.CloudDependencies, promptRunner prompt.PromptRunner, orphan model.OrphanedAlias) (bool, error) {
	params, err := buildOrphanRepairParams(ctx, orphan)
	if err != nil {
		return false, err
	}
	if _, err := fmt.Fprintf(ctx.Stdout, "Re-initializing AWS provider for alias %s...\n", orphan.Alias); err != nil {
		return false, err
	}
	if err := cloudcli.RunCloudInitAWSCommand(ctx, configStore, promptRunner, params, cloudDeps); err != nil {
		return false, err
	}
	return true, nil
}

// An existing SSO profile for the account supplies the start URL and region the alias lacks.
func buildOrphanRepairParams(ctx core.Context, orphan model.OrphanedAlias) (model.InitAWSCloudProviderParams, error) {
	w := newLineWriter(ctx.Stdout)
	params := model.InitAWSCloudProviderParams{
		Username:  orphan.Username,
		AccountID: orphan.AccountID,
		Region:    preferredRegionForOrphan(orphan),
	}
	profile, ok, err := config.LookupAWSSSOProfileByAccountID(orphan.AccountID)
	if err != nil {
		w.Linef("Note: could not scan ~/.aws/config for SSO defaults: %v", err)
		writeSSOLookupHelp(w, orphan)
		return params, w.Err()
	}
	if !ok {
		w.Linef("No SSO profile found in ~/.aws/config for account %s.", orphan.AccountID)
		writeSSOLookupHelp(w, orphan)
		return params, w.Err()
	}
	applyOrphanRepairProfile(w, &params, profile)
	if params.SSOStartURL == "" {
		writeSSOLookupHelp(w, orphan)
	}
	return params, w.Err()
}

func applyOrphanRepairProfile(w *lineWriter, params *model.InitAWSCloudProviderParams, profile config.AWSSSOProfile) {
	w.Linef("Found existing AWS SSO profile %q in ~/.aws/config; pre-filling defaults you can edit or accept with Enter.", profile.Profile)
	if profile.SSOStartURL != "" {
		params.SSOStartURL = profile.SSOStartURL
		w.Linef("  sso_start_url = %s", profile.SSOStartURL)
	}
	if profile.SSORegion != "" {
		params.SSORegion = profile.SSORegion
		w.Linef("  sso_region    = %s", profile.SSORegion)
	}
	if profile.RoleName != "" {
		params.RoleName = profile.RoleName
		w.Linef("  sso_role_name = %s", profile.RoleName)
	}
	if params.Region == "" && profile.Region != "" {
		params.Region = profile.Region
		w.Linef("  region        = %s", profile.Region)
	}
}

func writeSSOLookupHelp(w *lineWriter, orphan model.OrphanedAlias) {
	w.Linef("Where to find your AWS SSO start URL:")
	w.Linef("  1. `grep -E 'sso_start_url|sso_region' ~/.aws/config` — fastest if you have ever run `aws sso login`.")
	w.Linef("  2. AWS Console → IAM Identity Center → Settings → AWS access portal URL.")
	w.Linef("  3. The IAM Identity Center invitation email your admin sent when this account was provisioned.")
	w.Linef("  Format: https://<id>.awsapps.com/start (or your org's custom domain).")
	if orphan.AccountID != "" {
		w.Linef("  Target account: %s", orphan.AccountID)
	}
}

func preferredRegionForOrphan(orphan model.OrphanedAlias) string {
	for _, ref := range orphan.ReferencedByCloudContexts {
		region := strings.TrimSpace(ref.Region)
		if region != "" {
			return region
		}
	}
	return ""
}

func emptyIfBlank(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "<unresolved>"
	}
	return value
}

// lineWriter makes the first write error sticky.
type lineWriter struct {
	out io.Writer
	err error
}

func newLineWriter(w io.Writer) *lineWriter {
	return &lineWriter{out: w}
}

func (l *lineWriter) Linef(format string, args ...any) {
	if l.err != nil {
		return
	}
	_, err := fmt.Fprintf(l.out, format+"\n", args...)
	if err != nil {
		l.err = err
	}
}

func (l *lineWriter) Err() error {
	return l.err
}
