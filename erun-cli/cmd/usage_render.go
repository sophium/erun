package cmd

import (
	"fmt"

	common "github.com/sophium/erun/erun-common"
)

func writeUsageResult(ctx common.Context, usage common.RuntimeUsage) error {
	if err := writeUsageCPU(ctx, usage.CPU); err != nil {
		return err
	}
	if err := writeUsageMemory(ctx, usage.Memory); err != nil {
		return err
	}
	if err := writeUsageBuildsCaveat(ctx, usage); err != nil {
		return err
	}
	if err := writeUsageBuild(ctx, usage.Build); err != nil {
		return err
	}
	if err := writeUsageDisk(ctx, usage.Disk); err != nil {
		return err
	}
	return writeUsageWarnings(ctx, usage.Warnings)
}

// writeUsageBuildsCaveat names the gap CPU/Memory above cannot close on a
// build-capable environment: an image build runs in the erun-dind sidecar, a
// separate cgroup this reading cannot see, so those two lines can read idle
// while a build saturates the sidecar. It names the surface that does answer
// the question -- writeUsageBuild below, when the cgroup was reachable -- and
// when it was not, says plainly that no surface in this report can, instead of
// pointing at `erun observe`, which reports the sidecar's limits and never its
// usage.
func writeUsageBuildsCaveat(ctx common.Context, usage common.RuntimeUsage) error {
	if !usage.ExcludesBuilds {
		return nil
	}
	note := "Note: CPU/Memory above are the runtime container's alone -- an image build runs in the erun-dind sidecar, so this container reads near zero while a build saturates it; "
	switch {
	case usage.Build != nil && usage.Build.Available:
		note += "`Build CPU` below is that build cgroup's own reading."
	case usage.Build != nil:
		note += "the build cgroup exists but `Build CPU` below could not be read, so this report has no build figure."
	default:
		note += "no build cgroup was reachable from here (read from outside the pod, or an image without one), so this report has no build figure; `erun observe` reports the sidecar's limits, not its usage."
	}
	_, err := fmt.Fprintln(ctx.Stdout, note)
	return err
}

// writeUsageBuild reports the build cgroup's own CPU, or says why there is
// none. A build pinned at its cap shows 100% of a four-core quota here while
// the CPU line above shows a fraction of a percent, which is exactly the
// reading that tells an operator whether a build is running, working, and
// starved. Nothing is printed when no build cgroup applies at all: an
// environment without the sidecar has no build figure to be missing.
func writeUsageBuild(ctx common.Context, build *common.BuildCgroupMetrics) error {
	if build == nil {
		return nil
	}
	if !build.Available {
		reason := build.Unavailable
		if reason == "" {
			reason = "the build cgroup's counters were not readable from this process"
		}
		_, err := fmt.Fprintf(ctx.Stdout, "Build CPU: unavailable (%s)\n", reason)
		return err
	}
	if build.QuotaCores <= 0 {
		// A readable cgroup whose cpu.max was not: report the CPU actually
		// consumed rather than borrowing a percentage of an unknown quota.
		_, err := fmt.Fprintf(ctx.Stdout, "Build CPU: %.1fs of CPU used over the sample window (quota not readable)%s\n",
			build.CPUSeconds, buildUsageThrottleSuffix(build))
		return err
	}
	_, err := fmt.Fprintf(ctx.Stdout, "Build CPU: %.0f%% of a %.2f-core quota%s\n",
		build.CPUPercentOfQuota, build.QuotaCores, buildUsageThrottleSuffix(build))
	return err
}

func buildUsageThrottleSuffix(build *common.BuildCgroupMetrics) string {
	if build.TotalPeriods <= 0 {
		return ""
	}
	return fmt.Sprintf(" (throttled %d/%d periods, %.1fs)", build.ThrottledPeriods, build.TotalPeriods, build.ThrottledSeconds)
}

func writeUsageCPU(ctx common.Context, cpu common.RuntimeCPUUsage) error {
	if cpu.Unavailable != "" {
		_, err := fmt.Fprintf(ctx.Stdout, "CPU: unavailable (%s)\n", cpu.Unavailable)
		return err
	}
	_, err := fmt.Fprintf(ctx.Stdout, "CPU: %.1f%% of a %.2f-core quota (sampled over %.1fs)\n",
		cpu.UtilizationPercent, cpu.QuotaCores, cpu.IntervalSeconds)
	return err
}

func writeUsageMemory(ctx common.Context, memory common.RuntimeMemoryUsage) error {
	if memory.Unavailable != "" {
		_, err := fmt.Fprintf(ctx.Stdout, "Memory: unavailable (%s)\n", memory.Unavailable)
		return err
	}
	peak := formatUsagePeak(memory)
	oomKills := formatUsageOOMKills(memory)
	if memory.Unlimited {
		_, err := fmt.Fprintf(ctx.Stdout, "Memory: %s used, no limit set, peak %s, OOM kills %s\n",
			formatUsageBytes(memory.CurrentBytes), peak, oomKills)
		return err
	}
	_, err := fmt.Fprintf(ctx.Stdout, "Memory: %s / %s (%.1f%%), peak %s, OOM kills %s\n",
		formatUsageBytes(memory.CurrentBytes), formatUsageBytes(memory.LimitBytes), memory.PercentOfLimit,
		peak, oomKills)
	return err
}

// formatUsagePeak and formatUsageOOMKills report "unavailable" rather than a
// fabricated zero when memory.peak / memory.events' oom_kill counter could
// not be read -- the same distinction the reader itself carries via
// PeakObserved/OOMKillsObserved, so the two never collapse into a confident
// zero here either.
func formatUsagePeak(memory common.RuntimeMemoryUsage) string {
	if !memory.PeakObserved {
		return "unavailable"
	}
	return formatUsageBytes(memory.PeakBytes)
}

func formatUsageOOMKills(memory common.RuntimeMemoryUsage) string {
	if !memory.OOMKillsObserved {
		return "unavailable"
	}
	return fmt.Sprintf("%d", memory.OOMKills)
}

func writeUsageDisk(ctx common.Context, disks []common.RuntimeDiskUsage) error {
	for _, disk := range disks {
		if disk.Unavailable != "" {
			if _, err := fmt.Fprintf(ctx.Stdout, "Disk %s: unavailable (%s)\n", disk.Mount, disk.Unavailable); err != nil {
				return err
			}
			continue
		}
		if _, err := fmt.Fprintf(ctx.Stdout, "Disk %s: %s / %s (%.1f%%)\n",
			disk.Mount, formatUsageBytes(disk.UsedBytes), formatUsageBytes(disk.TotalBytes), disk.PercentUsed); err != nil {
			return err
		}
	}
	return nil
}

func writeUsageWarnings(ctx common.Context, warnings []string) error {
	if len(warnings) == 0 {
		return nil
	}
	if _, err := fmt.Fprintf(ctx.Stdout, "Warnings (%d):\n", len(warnings)); err != nil {
		return err
	}
	for _, warning := range warnings {
		if _, err := fmt.Fprintf(ctx.Stdout, "  %s\n", warning); err != nil {
			return err
		}
	}
	return nil
}

func formatUsageBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%dB", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%ciB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
