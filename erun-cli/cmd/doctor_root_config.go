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
func runRootConfigDoctor(ctx common.Context, configStore common.ConfigStore, cloudDeps common.CloudDependencies, promptRunner PromptRunner, options doctorOptions) (bool, error) {
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
	shouldRepair, err := shouldOfferRootConfigRepair(ctx, inspection, options)
	if err != nil {
		return handled, err
	}
	if !shouldRepair {
		return handled, nil
	}
	repaired, err := runRootConfigRepair(ctx, configStore, cloudDeps, promptRunner, inspection)
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
	if len(inspection.OrphanedAliases) == 0 {
		if inspection.ConfigStatus == common.RootConfigStatusOK {
			w.Linef("  no orphaned cloud-provider aliases.")
		}
		return
	}
	w.Linef("  orphaned aliases: %d", len(inspection.OrphanedAliases))
	for _, orphan := range inspection.OrphanedAliases {
		writeOrphanedAliasReport(w, orphan)
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
// Auto-detect only surfaces the inspection report and a suggestion;
// the actual repair (which prompts per orphan and may launch the
// interactive cloud-init SSO flow) only runs when the user opted in
// explicitly via --repair-config. Removing the "Repair root erun
// config now?" general prompt was a deliberate UX choice: with one
// orphan the per-alias prompt would have asked the same question
// twice in a row, and even with many orphans the per-alias prompt
// is the actionable confirmation.
func shouldOfferRootConfigRepair(ctx common.Context, inspection common.RootConfigInspection, options doctorOptions) (bool, error) {
	if inspection.Complete() {
		return false, nil
	}
	if options.repairConfig {
		return true, nil
	}
	_, err := fmt.Fprintln(ctx.Stdout, "Run `erun doctor --repair-config` to walk through restoring the root config or re-initializing orphaned cloud provider aliases.")
	return false, err
}

// runRootConfigRepair walks the user through restoring from a backup
// (when one is available) and/or re-initializing every orphaned
// cloud-provider alias.
func runRootConfigRepair(ctx common.Context, configStore common.ConfigStore, cloudDeps common.CloudDependencies, promptRunner PromptRunner, inspection common.RootConfigInspection) (bool, error) {
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
	repaired, err := runRootConfigRepairAliases(ctx, configStore, cloudDeps, promptRunner, inspection)
	return restored || repaired, err
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

func resolveRootConfigBackupSelector(inspection common.RootConfigInspection, selector string) (common.RootConfigBackup, bool, error) {
	if strings.ContainsAny(selector, "/\\") {
		return common.RootConfigBackup{Path: selector}, true, nil
	}
	parsed, err := time.Parse("2006-01-02", selector)
	if err != nil {
		return common.RootConfigBackup{}, false, fmt.Errorf("invalid backup date %q (expected YYYY-MM-DD or an absolute path)", selector)
	}
	for _, backup := range inspection.Backups {
		if backup.Date.Equal(parsed) {
			return backup, true, nil
		}
	}
	return common.RootConfigBackup{}, false, nil
}

func runRootConfigRestore(ctx common.Context, livePath string, backup common.RootConfigBackup) (bool, error) {
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
// via guard helpers (orphanRepairBlocked, runOrphanRepairDryRun) so
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
	params := common.InitAWSCloudProviderParams{
		Username:  orphan.Username,
		AccountID: orphan.AccountID,
		Region:    preferredRegionForOrphan(orphan),
	}
	if _, err := fmt.Fprintf(ctx.Stdout, "Re-initializing AWS provider for alias %s...\n", orphan.Alias); err != nil {
		return false, err
	}
	if err := runCloudInitAWSCommand(ctx, configStore, promptRunner, params, cloudDeps); err != nil {
		return false, err
	}
	return true, nil
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
