package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// runtime_usage.go wires the desktop's Runtime tab to the same reader
// erun-common/runtime_usage.go already gives `erun usage` and the MCP usage
// tool: CPU quota utilisation, memory against the runtime container's own
// cgroup limit with a real OOM-kill count, and disk on the workspace mount.
// The tab already holds the CPU/memory sliders, so this reading is directly
// actionable there in a way `erun usage` on the CLI is not.

// runtimeUsageTimeout bounds the on-demand probe, matching
// runtimeActivityTimeout in runtime_activity.go: a wedged pod must surface as
// unavailable quickly rather than hanging the Runtime tab. This is a
// refresh-button probe, not a poller.
const runtimeUsageTimeout = 10 * time.Second

// LoadRuntimeUsage reports the selected environment's own CPU, memory and
// disk usage against its cgroup limits.
//
// Fail-soft in the same way as LoadRuntimeActivity: an unreachable or stopped
// environment yields an unavailable report with the reason, never an error
// that turns the Runtime tab into a failure surface.
func (a *App) LoadRuntimeUsage(selection uiSelection) (uiRuntimeUsage, error) {
	selection = normalizeSelection(selection)
	if err := errMissingTenantOrEnvironment("load runtime usage", selection.Tenant, selection.Environment); err != nil {
		return uiRuntimeUsage{}, err
	}
	usage, err := a.deps.loadRuntimeUsage(a.backgroundContext(), selection)
	if err != nil {
		return uiRuntimeUsage{
			Tenant:      selection.Tenant,
			Environment: selection.Environment,
			Message:     "Cannot read this environment's resource usage: " + err.Error(),
		}, nil
	}
	return usage, nil
}

// loadRuntimeUsageViaKubectl resolves the selected env to a shell launch
// target and runs the shared reader against it, bounded by runtimeUsageTimeout.
func loadRuntimeUsageViaKubectl(parent context.Context, store erunUIStore, selection uiSelection) (uiRuntimeUsage, error) {
	if store == nil {
		return uiRuntimeUsage{}, fmt.Errorf("configuration store is unavailable")
	}
	result, err := eruncommon.ResolveOpen(store, eruncommon.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return uiRuntimeUsage{}, err
	}
	req := eruncommon.ShellLaunchParamsFromResult(result)

	ctx, cancel := context.WithTimeout(parent, runtimeUsageTimeout)
	defer cancel()

	reading, err := eruncommon.RunRuntimeUsage(quietOutputsContext(), runtimeUsageRunner(ctx), req, eruncommon.RuntimeUsageParams{})
	if err != nil {
		return uiRuntimeUsage{}, errors.New(runtimeProbeFailureMessage(ctx, runtimeUsageTimeout, err, func(e error) string { return e.Error() }))
	}
	return uiRuntimeUsageFromReading(reading), nil
}

// runtimeUsageRunner wraps the kubectl exec RunRuntimeUsage needs with the
// bounded ctx above. erun-common's own RunRuntimeContainerCommand has no
// timeout, since a CLI or MCP call is fine waiting on a real terminal; a UI
// probe on a refresh button is not.
func runtimeUsageRunner(ctx context.Context) eruncommon.RuntimeContainerCommandRunnerFunc {
	return func(req eruncommon.ShellLaunchParams, container, script string) (eruncommon.RemoteCommandResult, error) {
		args := make([]string, 0, 10)
		if kubernetesContext := strings.TrimSpace(req.KubernetesContext); kubernetesContext != "" {
			args = append(args, "--context", kubernetesContext)
		}
		if namespace := strings.TrimSpace(req.Namespace); namespace != "" {
			args = append(args, "--namespace", namespace)
		}
		args = append(args, "exec", "-c", strings.TrimSpace(container),
			"deployment/"+eruncommon.RuntimeReleaseName(req.Tenant), "--", "/bin/sh", "-lc", script)
		cmd := exec.CommandContext(ctx, "kubectl", args...)
		eruncommon.HideConsoleWindow(cmd)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		return eruncommon.RemoteCommandResult{Stdout: stdout.String(), Stderr: stderr.String()}, err
	}
}

// uiRuntimeUsageFromReading maps the shared reader's output onto the UI
// model. Every field carries its own unavailability rather than collapsing to
// zero, matching the reader's own fail-soft contract.
func uiRuntimeUsageFromReading(reading eruncommon.RuntimeUsage) uiRuntimeUsage {
	usage := uiRuntimeUsage{
		Tenant:      reading.Tenant,
		Environment: reading.Environment,
		Available:   true,
		CPU:         uiRuntimeCPUUsageFromReading(reading.CPU),
		Memory:      uiRuntimeMemoryUsageFromReading(reading.Memory),
		Warnings:    reading.Warnings,
	}
	for _, disk := range reading.Disk {
		usage.Disk = append(usage.Disk, uiRuntimeDiskUsageFromReading(disk))
	}
	usage.Message = uiRuntimeUsageMessage(usage)
	return usage
}

func uiRuntimeCPUUsageFromReading(cpu eruncommon.RuntimeCPUUsage) uiRuntimeCPUUsage {
	if cpu.Unavailable != "" {
		return uiRuntimeCPUUsage{Unavailable: cpu.Unavailable}
	}
	return uiRuntimeCPUUsage{
		Available:          true,
		QuotaCores:         cpu.QuotaCores,
		Quota:              fmt.Sprintf("%.2f cores", cpu.QuotaCores),
		UtilizationPercent: cpu.UtilizationPercent,
		Utilization:        fmt.Sprintf("%.1f%%", cpu.UtilizationPercent),
	}
}

func uiRuntimeMemoryUsageFromReading(memory eruncommon.RuntimeMemoryUsage) uiRuntimeMemoryUsage {
	if memory.Unavailable != "" {
		return uiRuntimeMemoryUsage{Unavailable: memory.Unavailable}
	}
	result := uiRuntimeMemoryUsage{
		Available:    true,
		Unlimited:    memory.Unlimited,
		CurrentBytes: memory.CurrentBytes,
		Current:      formatRuntimeUsageBytes(memory.CurrentBytes),
		PeakBytes:    memory.PeakBytes,
		Peak:         formatRuntimeUsageBytes(memory.PeakBytes),
		OOMKills:     memory.OOMKills,
	}
	if !memory.Unlimited {
		result.LimitBytes = memory.LimitBytes
		result.Limit = formatRuntimeUsageBytes(memory.LimitBytes)
		result.PercentOfLimit = memory.PercentOfLimit
	}
	return result
}

func uiRuntimeDiskUsageFromReading(disk eruncommon.RuntimeDiskUsage) uiRuntimeDiskUsage {
	if disk.Unavailable != "" {
		return uiRuntimeDiskUsage{Mount: disk.Mount, Unavailable: disk.Unavailable}
	}
	return uiRuntimeDiskUsage{
		Mount:       disk.Mount,
		Available:   true,
		TotalBytes:  disk.TotalBytes,
		Total:       formatRuntimeUsageBytes(disk.TotalBytes),
		UsedBytes:   disk.UsedBytes,
		Used:        formatRuntimeUsageBytes(disk.UsedBytes),
		PercentUsed: disk.PercentUsed,
		Percent:     fmt.Sprintf("%.1f%%", disk.PercentUsed),
	}
}

// uiRuntimeUsageMessage states what is actually known in one line, omitting
// whichever of CPU/memory could not be read rather than reporting a zero for
// it.
func uiRuntimeUsageMessage(usage uiRuntimeUsage) string {
	var parts []string
	if usage.CPU.Available {
		parts = append(parts, fmt.Sprintf("CPU %s of a %s quota", usage.CPU.Utilization, usage.CPU.Quota))
	}
	if usage.Memory.Available {
		if usage.Memory.Unlimited {
			parts = append(parts, fmt.Sprintf("memory %s used (no limit set)", usage.Memory.Current))
		} else {
			parts = append(parts, fmt.Sprintf("memory %s of %s (%.0f%%)", usage.Memory.Current, usage.Memory.Limit, usage.Memory.PercentOfLimit))
		}
	}
	if len(parts) == 0 {
		return "This environment's own CPU and memory usage could not be read."
	}
	return "This environment: " + strings.Join(parts, ", ") + "."
}

func formatRuntimeUsageBytes(bytes int64) string {
	if bytes <= 0 {
		return "0 MiB"
	}
	return formatMebibytes(bytes / (1 << 20))
}
