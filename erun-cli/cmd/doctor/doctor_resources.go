package doctor

import (
	"fmt"
	"strings"
	"time"

	"github.com/sophium/erun/cmd/shared"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/command/podruntime"
	"github.com/sophium/erun/erun-common/primitive/deploy"
)

// doctorResourceInterval is the CPU sample window; utilisation is a rate.
const doctorResourceInterval = time.Second

// reportDoctorResources prints live CPU/memory pressure and the sizing verdict via
// `erun usage`'s renderer. It never resizes: that rolls the pod and kills its work.
func reportDoctorResources(ctx core.Context, req config.ShellLaunchParams, result config.OpenResult, diagnosis deploy.DeployDiagnosisResult) error {
	if diagnosis.ClusterUnreachable {
		return reportPodSkippedUnreachable(ctx, "Resources")
	}
	usage, err := podruntime.RunRuntimeUsage(ctx, nil, nil, req, podruntime.RuntimeUsageParams{Interval: doctorResourceInterval})
	if err != nil {
		return reportPodUnreachable(ctx, "Resources", err)
	}
	if ctx.DryRun {
		// The read is already traced; a dry run's figures would be zero.
		return nil
	}
	if _, err := fmt.Fprintln(ctx.Stdout, "== Resources =="); err != nil {
		return err
	}
	if !usage.HasCounters() {
		return writeDoctorResourcesNotObserved(ctx, usage)
	}
	report := podruntime.ResolveRuntimeUsageReport(result.Tenant, result.EnvConfig, usage)
	if err := shared.WriteUsageResult(ctx, report); err != nil {
		return err
	}
	return writeDoctorResizeNextAction(ctx, result, report.Sizing)
}

// writeDoctorResourcesNotObserved reports missing counters as not observed, never as healthy.
func writeDoctorResourcesNotObserved(ctx core.Context, usage config.RuntimeUsage) error {
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

// writeDoctorResizeNextAction names the resize command with explicit values, which
// work from anywhere (--apply-recommendation only works in-pod).
func writeDoctorResizeNextAction(ctx core.Context, result config.OpenResult, sizing *deploy.RuntimeSizingRecommendation) error {
	if sizing == nil {
		return nil
	}
	flags := make([]string, 0, 2)
	for _, verdict := range sizing.Verdicts {
		if verdict.Action != deploy.RuntimeSizingRaise && verdict.Action != deploy.RuntimeSizingLower {
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
