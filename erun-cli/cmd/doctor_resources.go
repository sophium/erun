package cmd

import (
	"fmt"

	common "github.com/sophium/erun/erun-common"
)

// reportEnvironmentResources reports an environment that is under real
// resource pressure -- memory against its own limit with the OOM kills behind
// it, CPU against its quota -- together with the standing sizing verdict erun
// already computes from the same evidence.
//
// Doctor is the command an operator reaches for when an environment is
// misbehaving, and it inspected disk and said nothing about an environment
// sitting at 100% of its memory limit with OOM kills recorded -- even though
// `erun usage` had measured exactly that, warned about it, and produced a
// high-confidence resize verdict. The signal was on a command nobody runs
// unless they already suspect sizing, which is why the symptom presented as
// something else entirely.
//
// It renders through the same writers `erun usage` uses, so the two cannot
// report different numbers or a different verdict for one environment. It
// never resizes: a resize rolls the pod and kills whatever is running, so this
// diagnoses and names the remedy (`erun resize --apply-recommendation`) rather
// than applying it.
//
// Under --dry-run the read is traced and not taken, the same way the docker
// inspection behaves: a dry run of this command performs no subprocess at all,
// so an operator sees the read doctor would make without it being made. The
// section therefore reports nothing in a dry run, which is the one thing to
// know about reading doctor's resources output from one.
//
// The section prints only when there is something to say -- a warning fired,
// or a verdict other than a hold -- so a healthy environment keeps the
// diagnosis it had, and an environment that could not be read stays silent
// here rather than reporting a pressure it has not measured (the deploy
// diagnosis above already speaks to an unreachable pod).
func reportEnvironmentResources(ctx common.Context, req common.ShellLaunchParams, result common.OpenResult) error {
	// RunRuntimeUsage traces the exec it would run and returns an empty reading
	// under DryRun rather than executing it, so the trace stays and no
	// subprocess is spawned.
	usage, err := common.RunRuntimeUsage(ctx, nil, req, common.RuntimeUsageParams{})
	if err != nil || ctx.DryRun {
		return nil
	}
	report := common.ResolveRuntimeUsageReport(result.Tenant, result.EnvConfig, usage)
	if !environmentResourcesWorthReporting(usage, report) {
		return nil
	}

	if _, err := fmt.Fprintln(ctx.Stdout, "== Resources =="); err != nil {
		return err
	}
	if err := writeUsageCPU(ctx, "  ", usage.CPU); err != nil {
		return err
	}
	if err := writeUsageMemory(ctx, "  ", usage.Memory); err != nil {
		return err
	}
	if err := writeUsageWarnings(ctx, usage.Warnings); err != nil {
		return err
	}
	if err := writeUsageSizing(ctx, report.Sizing); err != nil {
		return err
	}
	return writeResizeRemedy(ctx)
}

// environmentResourcesWorthReporting decides whether this environment has
// anything to report here. A warning already means a threshold was crossed; a
// verdict other than a hold means erun has a concrete change to recommend.
// Either alone is worth the section, and neither is invented when the reading
// was clean.
func environmentResourcesWorthReporting(usage common.RuntimeUsage, report common.RuntimeUsageReport) bool {
	if len(usage.Warnings) > 0 {
		return true
	}
	if report.Sizing == nil {
		return false
	}
	for _, verdict := range report.Sizing.Verdicts {
		if verdict.Action == common.RuntimeSizingRaise || verdict.Action == common.RuntimeSizingLower {
			return true
		}
	}
	return false
}

// writeResizeRemedy names the command that acts on what this section just
// reported, so the section is a diagnosis with a next action rather than an
// alarm. It says plainly that doctor does not apply it, because the reason it
// does not is the same reason the operator needs to decide: the resize rolls
// the pod.
func writeResizeRemedy(ctx common.Context) error {
	_, err := fmt.Fprintln(ctx.Stdout,
		"  doctor does not resize: it rolls the pod and kills whatever is running. Apply the recommendation with `erun resize --apply-recommendation` once this environment is clear.")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(ctx.Stdout, "")
	return err
}
