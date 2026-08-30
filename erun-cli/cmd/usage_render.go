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
	if err := writeUsageDisk(ctx, usage.Disk); err != nil {
		return err
	}
	return writeUsageWarnings(ctx, usage.Warnings)
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
