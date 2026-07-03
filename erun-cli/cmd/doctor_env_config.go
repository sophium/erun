package cmd

import (
	"fmt"
	"strings"
	"time"

	common "github.com/sophium/erun/erun-common"
)

// runEnvConfigRestoreFromArgs recovers a changed or corrupted env config before
// the rest of doctor tries to load it.
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

func resolveEnvConfigBackupSelector(tenant, environment, selector string) (common.ConfigBackup, bool, error) {
	if strings.ContainsAny(selector, "/\\") {
		return common.ConfigBackup{Path: selector}, true, nil
	}
	if _, err := time.Parse("2006-01-02", selector); err != nil {
		return common.ConfigBackup{}, false, fmt.Errorf("invalid backup date %q (expected YYYY-MM-DD or an absolute path)", selector)
	}
	return common.FindEnvConfigBackupByDate(tenant, environment, selector)
}

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
