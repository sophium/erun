package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	common "github.com/sophium/erun/erun-common"
)

// resolveRuntimeConfigHome mirrors the entrypoint's config-home precedence
// (XDG_CONFIG_HOME, else $HOME/.config) so the in-pod reconciliation reads and
// writes the same tree initialize_erun_config wrote.
func resolveRuntimeConfigHome(homeDir string) string {
	if configHome := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); configHome != "" {
		return configHome
	}
	return filepath.Join(homeDir, ".config")
}

// runRuntimeConfigSync reconciles the in-pod erun config with the
// helm-injected ERUN_* env vars (`erun doctor --sync-config`). The
// --sync-config flag is the explicit confirmation, so it applies without a
// further prompt; in --dry-run it traces the file writes without performing
// them.
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
	writeConfigSyncDriftReport(ctx, inspection)
	if _, err := common.RunRuntimeConfigSync(ctx, inspection); err != nil {
		return err
	}
	if ctx.DryRun {
		_, err := fmt.Fprintln(ctx.Stdout, "Dry-run: would rewrite the in-pod config files traced above.")
		return err
	}
	_, err = fmt.Fprintln(ctx.Stdout, "In-pod config reconciled.")
	return err
}

func writeConfigSyncDriftReport(ctx common.Context, inspection common.ConfigSyncInspection) {
	fmt.Fprintf(ctx.Stdout, "In-pod config drift for %s/%s:\n", inspection.Tenant, inspection.Environment)
	for _, field := range inspection.Drift {
		fmt.Fprintf(ctx.Stdout, "  %-5s %-18s on-disk=%q injected=%q [%s]\n",
			field.Scope, field.Key, field.OnDisk, field.Injected, field.Kind)
	}
}
