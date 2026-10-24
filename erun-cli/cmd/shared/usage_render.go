package shared

import (
	"fmt"
	"sort"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
)

func WriteUsageResult(ctx core.Context, report model.RuntimeUsageReport) error {
	usage := report.RuntimeUsage
	if err := writeUsageCPU(ctx, "", usage.CPU); err != nil {
		return err
	}
	if err := writeUsageMemory(ctx, "", usage.Memory); err != nil {
		return err
	}
	if err := writeUsageRequests(ctx, usage.Requests); err != nil {
		return err
	}
	if err := writeUsageDindReading(ctx, usage); err != nil {
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

// writeUsageDindReading prints the dind sidecar's reading, where builds actually run,
// or names the gap when it could not be read.
func writeUsageDindReading(ctx core.Context, usage model.RuntimeUsage) error {
	if !usage.ExcludesBuilds {
		return nil
	}
	if usage.Dind == nil {
		_, err := fmt.Fprintln(ctx.Stdout,
			"Note: CPU/Memory above exclude the erun-dind sidecar where builds run -- its usage could not be read from inside this container; see `erun observe` for its resource limits.")
		return err
	}
	if _, err := fmt.Fprintln(ctx.Stdout, "erun-dind sidecar (where builds run):"); err != nil {
		return err
	}
	if err := writeUsageCPU(ctx, "  ", usage.Dind.CPU); err != nil {
		return err
	}
	return writeUsageMemory(ctx, "  ", usage.Dind.Memory)
}

func writeUsageCPU(ctx core.Context, prefix string, cpu model.RuntimeCPUUsage) error {
	if cpu.Unavailable != "" {
		_, err := fmt.Fprintf(ctx.Stdout, "%sCPU: unavailable (%s)\n", prefix, cpu.Unavailable)
		return err
	}
	_, err := fmt.Fprintf(ctx.Stdout, "%sCPU: %.1f%% of a %.2f-core quota (sampled over %.1fs)\n",
		prefix, cpu.UtilizationPercent, cpu.QuotaCores, cpu.IntervalSeconds)
	return err
}

// writeUsageMemory labels the denominator as a limit: a cgroup ceiling reserves nothing.
func writeUsageMemory(ctx core.Context, prefix string, memory model.RuntimeMemoryUsage) error {
	if memory.Unavailable != "" {
		_, err := fmt.Fprintf(ctx.Stdout, "%sMemory: unavailable (%s)\n", prefix, memory.Unavailable)
		return err
	}
	peak := formatUsagePeak(memory)
	oomKills := formatUsageOOMKills(memory)
	if memory.Unlimited {
		_, err := fmt.Fprintf(ctx.Stdout, "%sMemory: %s used, no limit set, peak %s, OOM kills %s\n",
			prefix, formatUsageBytes(memory.CurrentBytes), peak, oomKills)
		return err
	}
	_, err := fmt.Fprintf(ctx.Stdout, "%sMemory: %s / %s limit (%.1f%%), peak %s, OOM kills %s\n",
		prefix, formatUsageBytes(memory.CurrentBytes), formatUsageBytes(memory.LimitBytes), memory.PercentOfLimit,
		peak, oomKills)
	return err
}

// writeUsageRequests prints the scheduler reservation per container and for the pod,
// since limits alone read as provisioned capacity.
func writeUsageRequests(ctx core.Context, requests *model.RuntimeUsageRequests) error {
	if requests == nil {
		return nil
	}
	if requests.Unavailable != "" {
		_, err := fmt.Fprintf(ctx.Stdout, "Requests: unavailable (%s)\n", requests.Unavailable)
		return err
	}
	if _, err := fmt.Fprintf(ctx.Stdout, "Requests: %s for the pod\n", formatUsageRequests(requests.Pod)); err != nil {
		return err
	}
	for _, name := range sortedUsageRequestContainers(requests.Containers) {
		if _, err := fmt.Fprintf(ctx.Stdout, "  %s: %s\n", name, formatUsageRequests(requests.Containers[name])); err != nil {
			return err
		}
	}
	return nil
}

func sortedUsageRequestContainers(containers map[string]model.KubernetesRequests) []string {
	names := make([]string, 0, len(containers))
	for name := range containers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// formatUsageRequests renders an undeclared request as "none", not 0.
func formatUsageRequests(requests model.KubernetesRequests) string {
	cpu := "none"
	if requests.CPUMilli > 0 {
		cpu = config.FormatKubernetesCPUFromMilli(requests.CPUMilli) + " CPU"
	}
	memory := "none"
	if requests.MemoryBytes > 0 {
		memory = formatUsageBytes(requests.MemoryBytes)
	}
	return cpu + " / " + memory
}

// formatUsagePeak reports "unavailable" rather than zero when the counter was not read.
func formatUsagePeak(memory model.RuntimeMemoryUsage) string {
	if !memory.PeakObserved {
		return "unavailable"
	}
	return formatUsageBytes(memory.PeakBytes)
}

func formatUsageOOMKills(memory model.RuntimeMemoryUsage) string {
	if !memory.OOMKillsObserved {
		return "unavailable"
	}
	return fmt.Sprintf("%d", memory.OOMKills)
}

// writeUsageDisk labels mount figures "(node, shared)"; the own-usage line is this environment's alone.
func writeUsageDisk(ctx core.Context, disks []model.RuntimeDiskUsage) error {
	for _, disk := range disks {
		if disk.Unavailable != "" {
			if _, err := fmt.Fprintf(ctx.Stdout, "Disk (node, shared) %s: unavailable (%s)\n", disk.Mount, disk.Unavailable); err != nil {
				return err
			}
		} else if _, err := fmt.Fprintf(ctx.Stdout, "Disk (node, shared): %s %s / %s (%.1f%%)\n",
			disk.Mount, formatUsageBytes(disk.UsedBytes), formatUsageBytes(disk.TotalBytes), disk.PercentUsed); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(ctx.Stdout, "  this environment's own usage: %s\n", formatUsageDiskOwn(disk)); err != nil {
			return err
		}
	}
	return nil
}

func formatUsageDiskOwn(disk model.RuntimeDiskUsage) string {
	if !disk.OwnUsageObserved {
		return "unavailable"
	}
	return formatUsageBytes(disk.OwnUsedBytes)
}

func writeUsageWarnings(ctx core.Context, warnings []string) error {
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

// writeUsageSizing prints the recommendation beside the warnings using the same renderer as `erun list`.
func writeUsageSizing(ctx core.Context, sizing *model.RuntimeSizingRecommendation) error {
	if sizing == nil {
		return nil
	}
	if _, err := fmt.Fprintln(ctx.Stdout, "Sizing recommendation:"); err != nil {
		return err
	}
	for _, line := range RuntimeSizingLines(sizing, "  ") {
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
