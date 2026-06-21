package cmd

import (
	"fmt"
	"strings"
	"time"

	common "github.com/sophium/erun/erun-common"
)

// runEnvConfigRestoreFromArgs restores the target environment's config.yaml
// from a dated backup (or an absolute path). It runs before resolveOpen so a
// config that was changed or corrupted — for example a type that resolved to
// the wrong value and was persisted — can be recovered before the rest of
// doctor tries to load it. Returns true when a restore (or its --dry-run
// trace) was performed.
func runEnvConfigRestoreFromArgs(ctx common.Context, args []string, selector string) (bool, error) {
	params, err := common.OpenParamsForArgs(args)
	if err != nil {
		return false, err
	}
	tenant := strings.TrimSpace(params.Tenant)
	environment := strings.TrimSpace(params.Environment)
	if tenant == "" || environment == "" {
		return false, fmt.Errorf("--restore-env-config-from-backup needs an explicit tenant and environment: erun doctor <tenant> <environment> --restore-env-config-from-backup %s", selector)
	}
	backup, ok, err := resolveEnvConfigBackupSelector(tenant, environment, selector)
	if err != nil {
		return false, err
	}
	if !ok {
		if dates := availableEnvBackupDates(tenant, environment); dates != "" {
			return false, fmt.Errorf("no env config backup matches %q for %s/%s; available: %s", selector, tenant, environment, dates)
		}
		return false, fmt.Errorf("no env config backup matches %q for %s/%s (no backups exist yet)", selector, tenant, environment)
	}
	return runEnvConfigRestore(ctx, tenant, environment, backup)
}

// availableEnvBackupDates returns the env's dated backups as a newest-first
// comma list for the no-match error, so the user can pick a real date
// (recognition over recall). Empty when none exist or listing fails.
func availableEnvBackupDates(tenant, environment string) string {
	backups, err := common.ListEnvConfigBackups(tenant, environment)
	if err != nil || len(backups) == 0 {
		return ""
	}
	dates := make([]string, 0, len(backups))
	for _, backup := range backups {
		dates = append(dates, backup.Date.Format("2006-01-02"))
	}
	return strings.Join(dates, ", ")
}

// resolveEnvConfigBackupSelector maps a user-supplied selector to a backup:
// an absolute/relative path is taken verbatim; otherwise it must be a
// YYYY-MM-DD stamp resolved against the env's dated backups.
func resolveEnvConfigBackupSelector(tenant, environment, selector string) (common.ConfigBackup, bool, error) {
	if strings.ContainsAny(selector, "/\\") {
		return common.ConfigBackup{Path: selector}, true, nil
	}
	if _, err := time.Parse("2006-01-02", selector); err != nil {
		return common.ConfigBackup{}, false, fmt.Errorf("invalid backup date %q (expected YYYY-MM-DD or an absolute path)", selector)
	}
	return common.FindEnvConfigBackupByDate(tenant, environment, selector)
}

// runEnvConfigRestore traces the copy for the --dry-run contract and, when not
// dry-running, restores the env config (validating it deserializes into an
// EnvConfig before overwriting the live file).
func runEnvConfigRestore(ctx common.Context, tenant, environment string, backup common.ConfigBackup) (bool, error) {
	livePath, err := common.EnvConfigPath(tenant, environment)
	if err != nil {
		return false, err
	}
	ctx.TraceCommand("", "cp", backup.Path, livePath)
	if ctx.DryRun {
		_, err := fmt.Fprintf(ctx.Stdout, "Dry-run: would restore %s/%s config from %s\n", tenant, environment, backup.Path)
		return true, err
	}
	if err := common.RestoreEnvConfigFromBackup(backup.Path, tenant, environment); err != nil {
		return false, err
	}
	_, err = fmt.Fprintf(ctx.Stdout, "Restored %s/%s config from %s\n", tenant, environment, backup.Path)
	return true, err
}
