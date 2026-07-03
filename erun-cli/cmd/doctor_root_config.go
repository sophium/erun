package cmd

import (
	"fmt"
	"io"
	"strings"
	"time"

	common "github.com/sophium/erun/erun-common"
)

// rootConfigSkipsInspection returns true when nothing about the root
// config inspection should fire: the file is healthy AND no explicit
// repair flag was set. Used to keep `erun doctor` silent on the
// common case where the user is invoking it for runtime cleanup.
func rootConfigSkipsInspection(inspection common.RootConfigInspection, options doctorOptions) bool {
	if !inspection.Complete() {
		return false
	}
	if options.repairConfig {
		return false
	}
	return strings.TrimSpace(options.restoreConfigFromBackup) == ""
}

// runRootConfigDoctor inspects the root config, prints a summary, and
// dispatches into repair flows when problems are found OR the user
// explicitly asked for one of the repair flags. The returned bool is
// retained so future callers can branch on "any work attempted," but
// today the caller short-circuits on doctorOnlyRepairConfig(options)
// regardless of outcome — that matches the user's mental model of
// "I asked for config repair, do not also run tenant/env cleanup."
func runRootConfigDoctor(ctx common.Context, configStore common.ConfigStore, cloudDeps common.CloudDependencies, cloudContextDeps common.CloudContextDependencies, promptRunner PromptRunner, options doctorOptions) (bool, error) {
	inspection, err := common.InspectRootConfig(configStore)
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

// runRootConfigRestoreSelector handles the --restore-config-from-backup
// path when that flag is set. When it actually restored something it
// re-runs the inspection and prints the refreshed report before
// returning the new inspection plus handled=true.
func runRootConfigRestoreSelector(ctx common.Context, configStore common.ConfigStore, inspection common.RootConfigInspection, options doctorOptions) (common.RootConfigInspection, bool, error) {
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
	refreshed, err := common.InspectRootConfig(configStore)
	if err != nil {
		return inspection, true, err
	}
	if err := writeRootConfigInspectionReport(ctx, refreshed); err != nil {
		return refreshed, true, err
	}
	return refreshed, true, nil
}

// writeRootConfigInspectionReport renders the inspection as a small
// human-readable block on ctx.Stdout. Errors are accumulated through
// a lineWriter so each individual write does not add a branch to the
// cyclomatic count; the function reads as a sequence of statements
// even though it produces several lines of output.
func writeRootConfigInspectionReport(ctx common.Context, inspection common.RootConfigInspection) error {
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

func writeRootConfigInspectionCounts(w *lineWriter, inspection common.RootConfigInspection) {
	if inspection.ConfigStatus != common.RootConfigStatusOK {
		return
	}
	w.Linef("  cloud providers configured: %d", inspection.ConfiguredCount)
	w.Linef("  cloud contexts: %d, tenants: %d", inspection.CloudContextHits, inspection.TenantHits)
}

func writeRootConfigInspectionOrphans(w *lineWriter, inspection common.RootConfigInspection) {
	switch {
	case len(inspection.OrphanedAliases) > 0:
		w.Linef("  orphaned aliases: %d", len(inspection.OrphanedAliases))
		for _, orphan := range inspection.OrphanedAliases {
			writeOrphanedAliasReport(w, orphan)
		}
	case inspection.ConfigStatus == common.RootConfigStatusOK && len(inspection.OrphanedContexts) == 0:
		w.Linef("  no orphaned cloud-provider aliases.")
	}
	if len(inspection.OrphanedContexts) > 0 {
		w.Linef("  orphaned cloud contexts: %d", len(inspection.OrphanedContexts))
		for _, orphan := range inspection.OrphanedContexts {
			writeOrphanedCloudContextReport(w, orphan)
		}
	}
}

func writeOrphanedCloudContextReport(w *lineWriter, orphan common.OrphanedCloudContext) {
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

func writeRootConfigInspectionBackups(w *lineWriter, inspection common.RootConfigInspection) {
	if len(inspection.Backups) == 0 {
		return
	}
	w.Linef("  available backups (newest first):")
	for _, backup := range inspection.Backups {
		// Path is emitted as a separate field rather than wrapped
		// in parens so the integration normalizer's <TMP> rule
		// (which matches /tmp/... up to whitespace) does not eat
		// a trailing closing paren into the placeholder.
		w.Linef("    - %s path=%s", backup.Date.Format("2006-01-02"), backup.Path)
	}
}

func writeOrphanedAliasReport(w *lineWriter, orphan common.OrphanedAlias) {
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

func formatOrphanedContexts(refs []common.OrphanedAliasContextRef) string {
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

// shouldOfferRootConfigRepair gates whether to enter the repair flow.
// On an interactive terminal we enter the repair flow whenever there
// is something to fix, even without --repair-config — the per-alias
// prompt below is the only confirmation the user has to answer, so
// the experience is "inspect, see the problem, confirm the fix" in
// a single step. Dry-run and non-interactive runs cannot prompt, so
// they print the suggestion line and exit the root-config flow; the
// explicit --repair-config flag is what the suggestion points at.
func shouldOfferRootConfigRepair(ctx common.Context, promptRunner PromptRunner, inspection common.RootConfigInspection, options doctorOptions) (bool, error) {
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

// runRootConfigRepair walks the user through restoring from a backup
// (when one is available), re-initializing every orphaned cloud
// provider alias, and recovering every orphaned cloud-context
// reference from AWS.
func runRootConfigRepair(ctx common.Context, configStore common.ConfigStore, cloudDeps common.CloudDependencies, cloudContextDeps common.CloudContextDependencies, promptRunner PromptRunner, inspection common.RootConfigInspection) (bool, error) {
	inspection, restored, err := runRootConfigRepairRestore(ctx, configStore, promptRunner, inspection)
	if err != nil {
		return restored, err
	}
	if restored && inspection.Complete() {
		return true, nil
	}
	if inspection.ConfigStatus != common.RootConfigStatusOK {
		_, err := fmt.Fprintln(ctx.Stdout, "Root config is not loadable and no backup was restored; resolve the file manually (or supply --restore-config-from-backup <date>) and re-run.")
		return restored, err
	}
	return runRootConfigRepairOrphans(ctx, configStore, cloudDeps, cloudContextDeps, promptRunner, inspection, restored)
}

func runRootConfigRepairOrphans(ctx common.Context, configStore common.ConfigStore, cloudDeps common.CloudDependencies, cloudContextDeps common.CloudContextDependencies, promptRunner PromptRunner, inspection common.RootConfigInspection, restored bool) (bool, error) {
	repaired, err := runRootConfigRepairAliases(ctx, configStore, cloudDeps, promptRunner, inspection)
	if err != nil {
		return restored || repaired, err
	}
	refreshed, refreshErr := common.InspectRootConfig(configStore)
	if refreshErr != nil {
		return restored || repaired, refreshErr
	}
	recoveredContexts, err := runRootConfigRepairContexts(ctx, configStore, cloudContextDeps, promptRunner, refreshed)
	return restored || repaired || recoveredContexts, err
}

func runRootConfigRepairRestore(ctx common.Context, configStore common.ConfigStore, promptRunner PromptRunner, inspection common.RootConfigInspection) (common.RootConfigInspection, bool, error) {
	if inspection.ConfigStatus == common.RootConfigStatusOK || len(inspection.Backups) == 0 {
		return inspection, false, nil
	}
	restored, err := offerRootConfigBackupRestore(ctx, promptRunner, inspection)
	if err != nil || !restored {
		return inspection, false, err
	}
	refreshed, err := common.InspectRootConfig(configStore)
	if err != nil {
		return inspection, true, err
	}
	if err := writeRootConfigInspectionReport(ctx, refreshed); err != nil {
		return refreshed, true, err
	}
	return refreshed, true, nil
}

func runRootConfigRepairAliases(ctx common.Context, configStore common.ConfigStore, cloudDeps common.CloudDependencies, promptRunner PromptRunner, inspection common.RootConfigInspection) (bool, error) {
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

func runRootConfigRepairContexts(ctx common.Context, configStore common.ConfigStore, cloudContextDeps common.CloudContextDependencies, promptRunner PromptRunner, inspection common.RootConfigInspection) (bool, error) {
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

// offerOrphanedCloudContextRecovery walks one orphaned cloud-context
// reference through the recovery decision tree. The recovery itself
// (AWS describe-instances + reconstruction + save) runs inside
// common.RecoverCloudContextFromAWS; this function only handles the
// CLI-side prompts and dry-run trace.
func offerOrphanedCloudContextRecovery(ctx common.Context, configStore common.ConfigStore, cloudContextDeps common.CloudContextDependencies, promptRunner PromptRunner, orphan common.OrphanedCloudContext) (bool, error) {
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
		ok, err := confirmPrompt(promptRunner, label)
		if err != nil || !ok {
			return false, err
		}
	}
	params := common.RecoverCloudContextParams{
		KubernetesContext:  orphan.KubernetesContext,
		CloudProviderAlias: orphan.CloudProviderAlias,
		Region:             orphan.Region,
	}
	if _, err := fmt.Fprintf(ctx.Stdout, "Recovering cloud context %s...\n", orphan.KubernetesContext); err != nil {
		return false, err
	}
	result, err := common.RecoverCloudContextFromAWS(ctx, configStore, params, cloudContextDeps)
	if err != nil {
		_, _ = fmt.Fprintf(ctx.Stdout, "Recovery failed for %s: %v\n", orphan.KubernetesContext, err)
		return false, nil
	}
	writeCloudContextRecoverySummary(ctx, result)
	return true, nil
}

func contextRecoveryBlockedReason(orphan common.OrphanedCloudContext) (string, bool) {
	if strings.TrimSpace(orphan.AccountID) == "" || strings.TrimSpace(orphan.Region) == "" {
		return fmt.Sprintf("Skipping context %q: name does not match the erun-<seq>-<account>-<region> shape, so AWS recovery cannot be auto-seeded", orphan.KubernetesContext), true
	}
	if strings.TrimSpace(orphan.CloudProviderAlias) == "" {
		return fmt.Sprintf("Skipping context %q: env references it without a cloud provider alias, so AWS recovery cannot be authenticated", orphan.KubernetesContext), true
	}
	return "", false
}

func writeCloudContextRecoverySummary(ctx common.Context, result common.RecoverCloudContextResult) {
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

func offerRootConfigBackupRestore(ctx common.Context, promptRunner PromptRunner, inspection common.RootConfigInspection) (bool, error) {
	backup := inspection.Backups[0]
	if promptRunner == nil {
		_, err := fmt.Fprintln(ctx.Stdout, "Non-interactive run: pass --restore-config-from-backup "+backup.Date.Format("2006-01-02")+" to restore.")
		return false, err
	}
	label := fmt.Sprintf("Restore root config from backup %s path=%s?", backup.Date.Format("2006-01-02"), backup.Path)
	ok, err := confirmPrompt(promptRunner, label)
	if err != nil || !ok {
		return false, err
	}
	return runRootConfigRestore(ctx, inspection.ConfigPath, backup)
}

func runRootConfigRestoreFromBackup(ctx common.Context, inspection common.RootConfigInspection, selector string) (bool, error) {
	backup, ok, err := resolveRootConfigBackupSelector(inspection, selector)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, fmt.Errorf("no root config backup matches %q", selector)
	}
	return runRootConfigRestore(ctx, inspection.ConfigPath, backup)
}

func resolveRootConfigBackupSelector(inspection common.RootConfigInspection, selector string) (common.ConfigBackup, bool, error) {
	if strings.ContainsAny(selector, "/\\") {
		return common.ConfigBackup{Path: selector}, true, nil
	}
	parsed, err := time.Parse("2006-01-02", selector)
	if err != nil {
		return common.ConfigBackup{}, false, fmt.Errorf("invalid backup date %q (expected YYYY-MM-DD or an absolute path)", selector)
	}
	for _, backup := range inspection.Backups {
		if backup.Date.Equal(parsed) {
			return backup, true, nil
		}
	}
	return common.ConfigBackup{}, false, nil
}

func runRootConfigRestore(ctx common.Context, livePath string, backup common.ConfigBackup) (bool, error) {
	ctx.TraceCommand("", "cp", backup.Path, livePath)
	if ctx.DryRun {
		_, err := fmt.Fprintf(ctx.Stdout, "Dry-run: would restore root config from %s\n", backup.Path)
		return true, err
	}
	if err := common.RestoreRootConfigFromBackup(backup.Path, livePath); err != nil {
		return false, err
	}
	_, err := fmt.Fprintf(ctx.Stdout, "Restored root config from %s\n", backup.Path)
	return true, err
}

// offerOrphanedAliasRepair walks a single orphan through the repair
// decision tree. It is intentionally only allowed to bail out early
// via guard helpers (orphanRepairBlockedReason, runOrphanRepairDryRun) so
// the function body stays linear and short.
func offerOrphanedAliasRepair(ctx common.Context, configStore common.ConfigStore, cloudDeps common.CloudDependencies, promptRunner PromptRunner, orphan common.OrphanedAlias) (bool, error) {
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
	ok, err := confirmPrompt(promptRunner, fmt.Sprintf("Re-initialize cloud provider alias %s (account %s, user %s)?", orphan.Alias, orphan.AccountID, orphan.Username))
	if err != nil || !ok {
		return false, err
	}
	return runOrphanRepair(ctx, configStore, cloudDeps, promptRunner, orphan)
}

func orphanRepairBlockedReason(orphan common.OrphanedAlias) (string, bool) {
	if !orphan.Parsed {
		return fmt.Sprintf("Skipping malformed alias %q; recreate it manually with `erun cloud init aws`", orphan.Alias), true
	}
	if orphan.Provider != common.CloudProviderAWS {
		return fmt.Sprintf("Skipping alias %q: provider %q is not supported by repair flow", orphan.Alias, orphan.Provider), true
	}
	return "", false
}

// runOrphanRepairDryRun emits the trace + summary line the repair
// flow would produce for one orphan in --dry-run mode. The cloud
// init flow expects to prompt for SSO start URL / region; reaching
// for that prompt under --dry-run would block on stdin or emit
// non-deterministic prompt UI in the trace. Trace the intent and
// let the user run the same step interactively when they actually
// want to repair.
func runOrphanRepairDryRun(ctx common.Context, orphan common.OrphanedAlias) (bool, error) {
	region := preferredRegionForOrphan(orphan)
	ctx.Trace(fmt.Sprintf("doctor repair-config: would re-init aws cloud provider alias=%s account=%s user=%s region=%s", orphan.Alias, orphan.AccountID, orphan.Username, region))
	_, err := fmt.Fprintf(ctx.Stdout, "Dry-run: would re-initialize AWS provider for alias %s (account %s, user %s)\n", orphan.Alias, orphan.AccountID, orphan.Username)
	return true, err
}

func runOrphanRepair(ctx common.Context, configStore common.ConfigStore, cloudDeps common.CloudDependencies, promptRunner PromptRunner, orphan common.OrphanedAlias) (bool, error) {
	params, err := buildOrphanRepairParams(ctx, orphan)
	if err != nil {
		return false, err
	}
	if _, err := fmt.Fprintf(ctx.Stdout, "Re-initializing AWS provider for alias %s...\n", orphan.Alias); err != nil {
		return false, err
	}
	if err := runCloudInitAWSCommand(ctx, configStore, promptRunner, params, cloudDeps); err != nil {
		return false, err
	}
	return true, nil
}

// buildOrphanRepairParams seeds InitAWSCloudProviderParams from the
// orphan plus whatever ~/.aws/config already knows about the same
// account. The discovery is a UX optimization: the user usually has
// an SSO profile registered for the orphaned account already (the
// previous erun-sso-* profile that wrote the now-missing root
// config), and that profile carries the SSO start URL + SSO region
// erun cannot derive from the alias string alone.
//
// On a miss the user is pointed at the canonical lookup steps so
// they can find the SSO start URL without leaving the prompt — the
// help text is the value-add even when discovery itself produces
// nothing.
func buildOrphanRepairParams(ctx common.Context, orphan common.OrphanedAlias) (common.InitAWSCloudProviderParams, error) {
	w := newLineWriter(ctx.Stdout)
	params := common.InitAWSCloudProviderParams{
		Username:  orphan.Username,
		AccountID: orphan.AccountID,
		Region:    preferredRegionForOrphan(orphan),
	}
	profile, ok, err := common.LookupAWSSSOProfileByAccountID(orphan.AccountID)
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

func applyOrphanRepairProfile(w *lineWriter, params *common.InitAWSCloudProviderParams, profile common.AWSSSOProfile) {
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

// writeSSOLookupHelp prints a short, copy-pasteable cheat sheet so a
// user who does not know their SSO portal URL can recover without
// abandoning the prompt. The block is deliberately terse — three
// pointers, one example shape — to stay readable beside the prompt
// itself.
func writeSSOLookupHelp(w *lineWriter, orphan common.OrphanedAlias) {
	w.Linef("Where to find your AWS SSO start URL:")
	w.Linef("  1. `grep -E 'sso_start_url|sso_region' ~/.aws/config` — fastest if you have ever run `aws sso login`.")
	w.Linef("  2. AWS Console → IAM Identity Center → Settings → AWS access portal URL.")
	w.Linef("  3. The IAM Identity Center invitation email your admin sent when this account was provisioned.")
	w.Linef("  Format: https://<id>.awsapps.com/start (or your org's custom domain).")
	if orphan.AccountID != "" {
		w.Linef("  Target account: %s", orphan.AccountID)
	}
}

func preferredRegionForOrphan(orphan common.OrphanedAlias) string {
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

// lineWriter accumulates the first write error so call sites that
// emit a fixed sequence of lines do not need a branch per write.
// Cyclomatic complexity goes down without sacrificing the contract
// that the first error halts the rest of the output.
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
