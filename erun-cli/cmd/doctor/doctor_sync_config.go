package doctor

import (
	"fmt"
	"os"

	"github.com/sophium/erun/cmd/shared/prompt"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/command/doctor"
)

// runRuntimeConfigSync applies without a prompt; --sync-config is the confirmation.
func runRuntimeConfigSync(ctx core.Context, promptRunner prompt.PromptRunner, options doctorOptions, configHome string) error {
	_ = promptRunner
	_ = options
	inspection, err := doctor.InspectRuntimeConfigSync(configHome, os.Getenv)
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
	if err := doctor.RunRuntimeConfigSync(ctx, inspection); err != nil {
		return err
	}
	if ctx.DryRun {
		_, err := fmt.Fprintln(ctx.Stdout, "Dry-run: would rewrite the in-pod config files traced above.")
		return err
	}
	_, err = fmt.Fprintln(ctx.Stdout, "In-pod config reconciled.")
	return err
}

func writeConfigSyncDriftReport(ctx core.Context, inspection doctor.ConfigSyncInspection) error {
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
