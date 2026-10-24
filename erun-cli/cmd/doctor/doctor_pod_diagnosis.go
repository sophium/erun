package doctor

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
)

// reportPodUnreachable degrades a pod-dependent check to a report instead of aborting.
func reportPodUnreachable(ctx core.Context, header string, err error) error {
	_, ferr := fmt.Fprintf(ctx.Stdout, "== %s ==\ncould not read: %s\nThe runtime pod is not reachable for this check; see the helm release status and pod state reported above for why, then retry once it is running.\n\n", header, strings.TrimSpace(err.Error()))
	return ferr
}

// reportPodSkippedUnreachable skips the probe once the cluster is known unreachable,
// avoiding repeated kubectl timeouts.
func reportPodSkippedUnreachable(ctx core.Context, header string) error {
	_, err := fmt.Fprintf(ctx.Stdout, "== %s ==\nskipped: the runtime pod is not reachable for this check; see the helm release status and pod state reported above for why, then retry once it is running.\n\n", header)
	return err
}

// reportRuntimeImageRegistryMismatch flags a runtime image outside the env's
// runtimeregistry, whose credential failure leaves a redeployed pod unable to pull.
func reportRuntimeImageRegistryMismatch(ctx core.Context, result config.OpenResult) error {
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
		imageRegistry, runtimeRegistry, config.DevopsComponentName, imageRegistry, runtimeRegistry, imageRegistry,
		result.Tenant, result.Environment, imageRegistry)
	return err
}

// reportRuntimeImageLineMismatch flags a runtimeimage on a different release line than
// the last deploy; a re-resolving deploy could move the pod back, and Recreate hides it.
func reportRuntimeImageLineMismatch(ctx core.Context, result config.OpenResult) error {
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
