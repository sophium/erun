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
	if memory.Unlimited {
		_, err := fmt.Fprintf(ctx.Stdout, "Memory: %s used, no limit set, peak %s, OOM kills %d\n",
			formatUsageBytes(memory.CurrentBytes), formatUsageBytes(memory.PeakBytes), memory.OOMKills)
		return err
	}
	_, err := fmt.Fprintf(ctx.Stdout, "Memory: %s / %s (%.1f%%), peak %s, OOM kills %d\n",
		formatUsageBytes(memory.CurrentBytes), formatUsageBytes(memory.LimitBytes), memory.PercentOfLimit,
		formatUsageBytes(memory.PeakBytes), memory.OOMKills)
	return err
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
