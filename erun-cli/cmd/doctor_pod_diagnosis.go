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
