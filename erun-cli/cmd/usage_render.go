package cmd

import (
	"fmt"
	"sort"

	common "github.com/sophium/erun/erun-common"
)

func writeUsageResult(ctx common.Context, report common.RuntimeUsageReport) error {
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

// writeUsageDindReading prints the erun-dind sidecar's own CPU/memory reading
// on a build-capable environment: every image build actually runs there, a
// separate cgroup the runtime container's own reading above cannot see, so
// without this a busy build reads as an idle environment. Falls back to
// naming the gap when the sidecar's own cgroup could not be read (an older
// runtime image, a sidecar mid-restart), matching the desktop hover card's
// "-- excludes builds" caveat (Sidebar.EnvHoverCard.tsx) so both transports
// agree on what this reading covers either way.
func writeUsageDindReading(ctx common.Context, usage common.RuntimeUsage) error {
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

func writeUsageCPU(ctx common.Context, prefix string, cpu common.RuntimeCPUUsage) error {
	if cpu.Unavailable != "" {
		_, err := fmt.Fprintf(ctx.Stdout, "%sCPU: unavailable (%s)\n", prefix, cpu.Unavailable)
		return err
	}
	_, err := fmt.Fprintf(ctx.Stdout, "%sCPU: %.1f%% of a %.2f-core quota (sampled over %.1fs)\n",
		prefix, cpu.UtilizationPercent, cpu.QuotaCores, cpu.IntervalSeconds)
	return err
}

// writeUsageMemory names the denominator a memory percentage is taken against
// as a *limit*. Unqualified, `394.4MiB / 2.0GiB (19.3%)` reads as "this
// environment holds 2.0GiB": a cgroup ceiling is what the container may grow to
// under pressure, and it reserves nothing at all. The reservation is the line
// writeUsageRequests prints beneath it.
func writeUsageMemory(ctx common.Context, prefix string, memory common.RuntimeMemoryUsage) error {
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

// writeUsageRequests prints the reservation the scheduler admits this
// environment's pod on, which is a different number from every limit above and
// the one that decides whether the pod can be placed on a node at all. Without
// it the only figures on this output are ceilings, and a ceiling reads as
// provisioned: ~135 CPU and ~198GiB of limits on one node against 2.5 CPU and
// 10GiB of actual reservation is indistinguishable here from five environments
// holding their limits.
//
// It names each container's own request as well as the pod's effective total,
// because the container is what each limit above belongs to -- the runtime
// container's 1GiB request is the figure to read against its own 27GiB limit.
func writeUsageRequests(ctx common.Context, requests *common.RuntimeUsageRequests) error {
	if requests == nil {
		// A dry run read nothing, and the reading is omitted rather than
		// printed as a reservation of nothing.
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

func sortedUsageRequestContainers(containers map[string]common.KubernetesRequests) []string {
	names := make([]string, 0, len(containers))
	for name := range containers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// formatUsageRequests renders one request pair, naming the parts rather than
// emitting two bare numbers ("0.5 CPU / 2.0GiB"), and stating a resource the
// container declares nothing for as "none" -- an undeclared request reserves
// nothing, and a bare 0 would read as a measured reservation of zero.
func formatUsageRequests(requests common.KubernetesRequests) string {
	cpu := "none"
	if requests.CPUMilli > 0 {
		cpu = common.FormatKubernetesCPUFromMilli(requests.CPUMilli) + " CPU"
	}
	memory := "none"
	if requests.MemoryBytes > 0 {
		memory = formatUsageBytes(requests.MemoryBytes)
	}
	return cpu + " / " + memory
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

// writeUsageDisk labels the mount's total/used/percent as "(node, shared)":
// they come from a statfs of the whole mount, so every environment scheduled
// on the same node reports the identical figures regardless of which of them
// actually wrote the bytes (see RuntimeDiskUsage.NodeShared). The own-usage
// line beneath it is the number scoped to this environment alone -- the one
// an operator can actually reduce by cleaning up this environment.
func writeUsageDisk(ctx common.Context, disks []common.RuntimeDiskUsage) error {
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

func formatUsageDiskOwn(disk common.RuntimeDiskUsage) string {
	if !disk.OwnUsageObserved {
		return "unavailable"
	}
	return formatUsageBytes(disk.OwnUsedBytes)
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
