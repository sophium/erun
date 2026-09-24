package cmd

import (
	"fmt"
	"strings"
	"time"

	common "github.com/sophium/erun/erun-common"
)

// doctorResourceInterval is the CPU sample window the resources section reads
// over. Utilisation is a rate, so the reader has to elapse a window between two
// reads of cpu.stat; one second is `erun usage`'s own default, and it keeps the
// cost this section adds to every doctor run to a second.
const doctorResourceInterval = time.Second

// reportDoctorResources prints the environment's live CPU and memory pressure
// together with the standing sizing verdict that answers it. Both already
// existed -- `erun usage` computes them from the same cgroup counters -- and
// doctor, the command an operator reaches for when an environment misbehaves,
// reported neither: an environment pinned at 100% of its memory limit with real
// OOM kills diagnosed as a disk question.
//
// The body is `erun usage`'s own renderer over the same paired report (one
// reading, one recommendation), so the two commands cannot disagree about the
// figure or about the remedy.
//
// It reports and never repairs. Applying the verdict rolls the runtime pod
// (Recreate strategy) and kills whatever is running in it, so the command that
// would apply it, with its values spelled out, is named and left for the
// operator to run.
//
// A reading carrying no counters is reported as "not observed", never as a
// healthy environment: an environment whose pod was just replaced has no
// retained history to reason from, and one whose counters cannot be read at all
// is exactly the case where saying nothing would read as saying it is fine.
func reportDoctorResources(ctx common.Context, req common.ShellLaunchParams, result common.OpenResult, diagnosis common.DeployDiagnosisResult) error {
	if diagnosis.ClusterUnreachable {
		return reportPodSkippedUnreachable(ctx, "Resources")
	}
	usage, err := common.RunRuntimeUsage(ctx, nil, nil, req, common.RuntimeUsageParams{Interval: doctorResourceInterval})
	if err != nil {
		return reportPodUnreachable(ctx, "Resources", err)
	}
	if ctx.DryRun {
		// The read is already in the trace -- RunRuntimeUsage traces its exec
		// and its script -- which is what a dry run owes for a check whose whole
		// output is that read's result. Printing the figures here would print
		// the zero reading a dry run returns.
		return nil
	}
	if _, err := fmt.Fprintln(ctx.Stdout, "== Resources =="); err != nil {
		return err
	}
	if !usage.HasCounters() {
		return writeDoctorResourcesNotObserved(ctx, usage)
	}
	report := common.ResolveRuntimeUsageReport(result.Tenant, result.EnvConfig, usage)
	if err := writeUsageResult(ctx, report); err != nil {
		return err
	}
	return writeDoctorResizeNextAction(ctx, result, report.Sizing)
}

// writeDoctorResourcesNotObserved reports a reading that carries no counters at
// all. The distinction it exists for is the one `erun usage` already draws for
// its own unavailable fields: "nothing was observed" is not "nothing is wrong",
// and a doctor section that stays silent here reads as a clean bill of health
// for the one environment least able to say otherwise.
func writeDoctorResourcesNotObserved(ctx common.Context, usage common.RuntimeUsage) error {
	reason := strings.TrimSpace(usage.Memory.Unavailable)
	if reason == "" {
		reason = strings.TrimSpace(usage.CPU.Unavailable)
	}
	if reason == "" {
		reason = "the runtime container reported no cgroup counters"
	}
	_, err := fmt.Fprintf(ctx.Stdout,
		"not observed: %s.\n"+
			"This is not a healthy reading -- nothing here says this environment is sized correctly, only that its own CPU and memory counters could not be read. `erun usage` reports each field's own unavailability in the same words.\n\n",
		reason)
	return err
}

// writeDoctorResizeNextAction names the command that would apply the verdict,
// with the values spelled out. Doctor never runs it: a resize rolls the runtime
// pod and kills any live agent session inside it, so an environment silently
// resized mid-run would be a worse failure than the pressure this reports. The
// explicit values are the ones that work from anywhere -- `erun resize
// --apply-recommendation` re-derives the verdict from the environment's
// retained usage history, which is readable only from inside its own runtime
// pod. A verdict that suggests nothing ("hold", or insufficient evidence) gets
// no next action, because there is nothing to apply.
func writeDoctorResizeNextAction(ctx common.Context, result common.OpenResult, sizing *common.RuntimeSizingRecommendation) error {
	if sizing == nil {
		return nil
	}
	flags := make([]string, 0, 2)
	for _, verdict := range sizing.Verdicts {
		switch verdict.Action {
		case common.RuntimeSizingRaise, common.RuntimeSizingLower:
		default:
			continue
		}
		switch verdict.Resource {
		case "cpu":
			flags = append(flags, "--cpu "+verdict.Suggested)
		case "memory":
			flags = append(flags, "--memory "+verdict.Suggested)
		}
	}
	if len(flags) == 0 {
		return nil
	}
	_, err := fmt.Fprintf(ctx.Stdout,
		"Apply with `erun resize --tenant %s --environment %s %s` (rolls the runtime pod and kills any live session in it), or `--apply-recommendation` from inside the environment, where the retained usage history lives. Doctor reports this and never resizes.\n\n",
		result.Tenant, result.Environment, strings.Join(flags, " "))
	return err
}
