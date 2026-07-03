package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	common "github.com/sophium/erun/erun-common"
)

// resolveRuntimeConfigHome mirrors the entrypoint's config-home precedence so
// in-pod reconciliation reads and writes the same tree initialize_erun_config wrote.
func resolveRuntimeConfigHome(homeDir string) string {
	if configHome := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); configHome != "" {
		return configHome
	}
	return filepath.Join(homeDir, ".config")
}

// runRuntimeConfigSync reconciles the in-pod erun config with the helm-injected
// env vars. The --sync-config flag is itself the operator's confirmation, so it
// applies without a further prompt.
func runRuntimeConfigSync(ctx common.Context, promptRunner PromptRunner, options doctorOptions, configHome string) error {
	_ = promptRunner
	_ = options
	inspection, err := common.InspectRuntimeConfigSync(configHome, os.Getenv)
	if err != nil {
		return err
	}
	if !inspection.HasInjected {
		_, err := fmt.Fprintln(ctx.Stdout, "Cannot reconcile in-pod config: ERUN_TENANT/ERUN_ENVIRONMENT are unset.")
		return err
	}
	if inspection.InSync() {
		_, err := fmt.Fprintln(ctx.Stdout, "In-pod config matches the injected env; nothing to reconcile.")
		return err
	}
	if err := writeConfigSyncDriftReport(ctx, inspection); err != nil {
		return err
	}
	if err := common.RunRuntimeConfigSync(ctx, inspection); err != nil {
		return err
	}
	if ctx.DryRun {
		_, err := fmt.Fprintln(ctx.Stdout, "Dry-run: would rewrite the in-pod config files traced above.")
		return err
	}
	_, err = fmt.Fprintln(ctx.Stdout, "In-pod config reconciled.")
	return err
}

func writeConfigSyncDriftReport(ctx common.Context, inspection common.ConfigSyncInspection) error {
	if _, err := fmt.Fprintf(ctx.Stdout, "In-pod config drift for %s/%s:\n", inspection.Tenant, inspection.Environment); err != nil {
		return err
	}
	for _, field := range inspection.Drift {
		if _, err := fmt.Fprintf(ctx.Stdout, "  %-5s %-18s on-disk=%q injected=%q [%s]\n",
			field.Scope, field.Key, field.OnDisk, field.Injected, field.Kind); err != nil {
			return err
		}
	}
	return nil
}
