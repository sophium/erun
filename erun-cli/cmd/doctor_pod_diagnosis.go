package cmd

import (
	"fmt"
	"strings"

	common "github.com/sophium/erun/erun-common"
)

// reportPodUnreachable prints a degraded report for a doctor check that needs
// the runtime pod, instead of aborting the rest of the command's report: the
// helm release status and pod state doctor prints first are the causal
// evidence, so this only names what could not be read and points back at
// them rather than surfacing a raw exec error with no next step.
func reportPodUnreachable(ctx common.Context, header string, err error) error {
	_, ferr := fmt.Fprintf(ctx.Stdout, "== %s ==\ncould not read: %s\nThe runtime pod is not reachable for this check; see the helm release status and pod state reported above for why, then retry once it is running.\n\n", header, strings.TrimSpace(err.Error()))
	return ferr
}

// reportRuntimeImageRegistryMismatch names a runtime image pinned to a
// registry other than the env's own runtimeregistry, before any pod-dependent
// check runs — the config alone is enough to tell, so this never execs and
// runs the same in --dry-run as for real: there is no live read to skip. The
// pull secret erun refreshes on deploy is keyed off exactly the registries in
// play (deploy_image_pull_secret.go covers both now), but that refresh still
// depends on a credential resolving for each at deploy time; a mismatch here
// is worth flagging on its own, because a credential failure for the image's
// own registry is exactly what leaves a redeployed pod unable to pull.
func reportRuntimeImageRegistryMismatch(ctx common.Context, result common.OpenResult) error {
	imageRegistry, runtimeRegistry, mismatched := result.EnvConfig.RuntimeImageRegistryMismatch()
	if !mismatched {
		return nil
	}
	_, err := fmt.Fprintf(ctx.Stdout,
		"== Runtime image registry ==\nruntimeimage resolves to registry %s, but runtimeregistry is %s.\n"+
			"The deploy that installs this env sets imageOverrides.%s from %s while runtimeRegistry stays %s; "+
			"the runtime pod can only pull if a credential for %s also resolves where `erun deploy`/`erun open --deploy` runs "+
			"(the same AWS/docker session that can push to it). If the pod is failing to pull, confirm that credential is "+
			"available there, or realign the two with `erun init %s %s --runtime-registry %s` to match the image.\n\n",
		imageRegistry, runtimeRegistry, common.DevopsComponentName, imageRegistry, runtimeRegistry, imageRegistry,
		result.Tenant, result.Environment, imageRegistry)
	return err
}

// reportRuntimeImageLineMismatch names a runtimeimage stuck on a different
// release line than the environment's last confirmed deploy actually ran —
// config alone is enough to tell (see EnvConfig.RuntimeImageLineMismatch), so
// this never execs and runs the same in --dry-run as for real. It is the
// static half of erun#1754: a future deploy that re-resolves runtimeimage
// without an explicit --runtime-image/--runtime-chart can silently move the
// pod back onto the wrong line, and the runtime chart's Recreate strategy
// tears the running pod down before that mistake is visible.
func reportRuntimeImageLineMismatch(ctx common.Context, result common.OpenResult) error {
	recordedLine, observedLine, mismatched := result.EnvConfig.RuntimeImageLineMismatch()
	if !mismatched {
		return nil
	}
	_, err := fmt.Fprintf(ctx.Stdout,
		"== Runtime image release line ==\nruntimeimage names the %s release line, but this env's last confirmed deploy actually ran the %s line (recorded as runtimerunningimage).\n"+
			"The next deploy that does not explicitly restate --runtime-image or --runtime-chart reads runtimeimage back to pick the pod's image; if that "+
			"still resolves to a real, existing tag on the %s line, the deploy would succeed and silently move the environment off the %s line it is "+
			"actually running. Realign it with `erun deploy %s %s --runtime-image <the %s image>` (or --runtime-chart), which records the choice explicitly.\n\n",
		recordedLine, observedLine, recordedLine, observedLine, result.Tenant, result.Environment, observedLine)
	return err
}
