package cmd

import (
	"fmt"

	common "github.com/sophium/erun/erun-common"
)

func writeUsageResult(ctx common.Context, report common.RuntimeUsageReport) error {
	usage := report.RuntimeUsage
	if err := writeUsageCPU(ctx, usage.CPU); err != nil {
		return err
	}
	if err := writeUsageMemory(ctx, usage.Memory); err != nil {
		return err
	}
	if err := writeUsageBuildsCaveat(ctx, usage.ExcludesBuilds); err != nil {
		return err
	}
	if err := writeUsageDisk(ctx, usage.Disk); err != nil {
		return err
	}
	if err := writeUsageWarnings(ctx, usage.Warnings); err != nil {
		return err
	}
	return writeUsageSizing(ctx, report.Sizing)
}

// writeUsageBuildsCaveat names the gap CPU/Memory above cannot close on a
// build-capable environment: an image build runs in the erun-dind sidecar, a
// separate cgroup this reading cannot see, so it can read idle while a build
// saturates the sidecar. Matches the desktop hover card's "-- excludes
// builds" caveat (Sidebar.EnvHoverCard.tsx) so the two transports never
// disagree about whether this reading covers builds.
func writeUsageBuildsCaveat(ctx common.Context, excludesBuilds bool) error {
	if !excludesBuilds {
		return nil
	}
	_, err := fmt.Fprintln(ctx.Stdout,
		"Note: CPU/Memory above exclude the erun-dind sidecar where builds run -- its usage is not visible from inside this container; see `erun observe` for the sidecar's own limits.")
	return err
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

// writeUsageSizing prints the standing recommendation directly beneath the
// warnings, because the two are one subject: an environment reported as
// saturated with no advice beside it leaves the operator holding an alarm and
// no next action. It renders through runtimeSizingLines -- the same renderer
// `erun list` uses, over the same recommendation, computed once from the
// reading above plus retained history -- so the remedy shown here cannot
// contradict the one shown there.
//
// A recommendation is omitted only when the reading and the history together
// support none at all, which is silence about an environment erun has never
// observed rather than silence about a saturated one: a memory warning is
// derived from the same evidence and the same threshold as a raise, so a
// warning always arrives with its verdict.
func writeUsageSizing(ctx common.Context, sizing *common.RuntimeSizingRecommendation) error {
	if sizing == nil {
		return nil
	}
	if _, err := fmt.Fprintln(ctx.Stdout, "Sizing recommendation:"); err != nil {
		return err
	}
	for _, line := range runtimeSizingLines(sizing, "  ") {
		if _, err := fmt.Fprintln(ctx.Stdout, line); err != nil {
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
