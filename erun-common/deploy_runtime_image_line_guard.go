package eruncommon

import (
	"fmt"
	"strings"
)

// guardRuntimeImageLineSwitch refuses a runtime deploy that would move the pod
// from one release line to a different one -- stock erun-devops vs a
// tenant's own <tenant>-devops -- without the operator saying so on this very
// call. It exists for the pairing erun#1754 was filed over: a persisted
// runtimeimage can drift away from the line an environment actually moved
// onto (nothing catches half of a two-field coordinate updating), and the
// wrong tag can resolve fine -- erun's own stock image genuinely exists at
// almost any version number a tenant's own line also uses -- so a deploy that
// only checks tag existence installs it instead of refusing. The runtime
// chart's Recreate strategy tears the running pod down before the
// replacement is scheduled, so this runs at spec-resolution time, before any
// cluster mutation.
//
// The baseline is EnvConfig.RuntimeRunningImage, the last image a deploy
// actually confirmed running (healed alongside RuntimeVersion by
// PersistRuntimeVersionFromDeploySpecs) -- not the operative RuntimeImage
// field, which is exactly the value this issue found stale. Comparing against
// the observed truth rather than the (possibly stale) config catches the
// dangerous case directly: a deploy about to move the pod off the line it is
// actually running, with nothing on this call saying that is intended.
//
// Three outcomes:
//   - explicitLineChange (an operator's own --runtime-image/--runtime-chart on
//     this call, or a build --deploy of the working tree's own image) always
//     proceeds: moving release lines on purpose is exactly what those inputs
//     are for.
//   - Both sides classify and disagree: refuse before rollout.
//   - Either side does not classify (no prior deploy recorded yet, or an
//     image reference this guard cannot parse into a component name) never
//     blocks -- an unclassifiable pairing must not read as fine, but it must
//     not read as wrong either (root AGENTS.md: "never block a legitimate
//     configuration you merely could not classify"), so it proceeds with a
//     trace instead of a refusal.
func guardRuntimeImageLineSwitch(ctx Context, target OpenResult, resolvedImage string, explicitLineChange bool) error {
	if explicitLineChange {
		return nil
	}
	resolvedLine, resolvedOK := runtimeImageReleaseLine(resolvedImage)
	if !resolvedOK {
		return nil
	}
	previous := strings.TrimSpace(target.EnvConfig.RuntimeRunningImage)
	if previous == "" {
		return nil
	}
	previousLine, previousOK := runtimeImageReleaseLine(previous)
	if !previousOK {
		ctx.Trace("deploy: runtime image line for " + previous + " (this env's last confirmed deploy) could not be classified; proceeding")
		return nil
	}
	if resolvedLine == previousLine {
		return nil
	}
	return fmt.Errorf("deploy: runtime image %s is on the %s release line, but %s/%s's last confirmed deploy ran %s (the %s line) -- pass --runtime-image or --runtime-chart to move release lines on purpose; if runtimeimage config just drifted, `erun doctor` explains how to realign it (erun#1754)",
		resolvedImage, resolvedLine, target.Tenant, target.Environment, previous, previousLine)
}
