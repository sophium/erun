package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam"
	"github.com/sophium/erun/erun-common/command/podruntime"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/primitive/open"
)

const runtimeUsageTimeout = 10 * time.Second

// LoadRuntimeUsage reports the environment's CPU, memory and disk usage against its
// cgroup limits; it fails soft to an unavailable report.
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
			Message:     fmt.Sprintf("Cannot read this environment's resource usage: %v", err),
		}, nil
	}
	return usage, nil
}

func loadRuntimeUsageViaKubectl(parent context.Context, store erunUIStore, selection uiSelection) (uiRuntimeUsage, error) {
	if store == nil {
		return uiRuntimeUsage{}, fmt.Errorf("configuration store is unavailable")
	}
	result, err := open.ResolveOpen(store, model.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return uiRuntimeUsage{}, err
	}
	req := cloud.ShellLaunchParamsFromResult(result)

	ctx, cancel := context.WithTimeout(parent, runtimeUsageTimeout)
	defer cancel()

	reading, err := podruntime.RunRuntimeUsage(quietOutputsContext(), runtimeUsageRunner(ctx), runtimePodRequestsRunner(ctx), req, model.RuntimeUsageParams{})
	if err != nil {
		return uiRuntimeUsage{}, errors.New(runtimeProbeFailureMessage(ctx, runtimeUsageTimeout, err, func(e error) string { return e.Error() }))
	}
	return uiRuntimeUsageFromReading(reading), nil
}

// erun-common's RunRuntimeContainerCommand has no timeout; a UI probe needs one.
func runtimeUsageRunner(ctx context.Context) seam.RuntimeContainerCommandRunnerFunc {
	return func(req config.ShellLaunchParams, container, script string) (model.RemoteCommandResult, error) {
		args := make([]string, 0, 10)
		if kubernetesContext := strings.TrimSpace(req.KubernetesContext); kubernetesContext != "" {
			args = append(args, "--context", kubernetesContext)
		}
		if namespace := strings.TrimSpace(req.Namespace); namespace != "" {
			args = append(args, "--namespace", namespace)
		}
		args = append(args, "exec", "-c", strings.TrimSpace(container),
			"deployment/"+config.RuntimeReleaseName(req.Tenant), "--", "/bin/sh", "-lc", script)
		cmd := exec.CommandContext(ctx, "kubectl", args...)
		core.HideConsoleWindow(cmd)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		return model.RemoteCommandResult{Stdout: stdout.String(), Stderr: stderr.String()}, err
	}
}

func runtimePodRequestsRunner(ctx context.Context) podruntime.RuntimePodRequestsRunnerFunc {
	return func(args []string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, "kubectl", args...)
		core.HideConsoleWindow(cmd)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("kubectl get pods: %w", err)
		}
		return stdout.Bytes(), nil
	}
}

// Every field keeps its own unavailability rather than collapsing to zero.
func uiRuntimeUsageFromReading(reading model.RuntimeUsage) uiRuntimeUsage {
	usage := uiRuntimeUsage{
		Tenant:         reading.Tenant,
		Environment:    reading.Environment,
		Available:      true,
		CPU:            uiRuntimeCPUUsageFromReading(reading.CPU),
		Memory:         uiRuntimeMemoryUsageFromReading(reading.Memory),
		Warnings:       reading.Warnings,
		ExcludesBuilds: reading.ExcludesBuilds,
		Requests:       uiRuntimeUsageRequestsFromReading(reading.Requests),
	}
	for _, disk := range reading.Disk {
		usage.Disk = append(usage.Disk, uiRuntimeDiskUsageFromReading(disk))
	}
	// Builds run in the dind sidecar, so it is carried separately; nil (older image) stays nil, not zero.
	if reading.Dind != nil {
		usage.Dind = &uiRuntimeDindUsage{
			CPU:    uiRuntimeCPUUsageFromReading(reading.Dind.CPU),
			Memory: uiRuntimeMemoryUsageFromReading(reading.Dind.Memory),
		}
	}
	usage.Message = uiRuntimeUsageMessage(usage)
	return usage
}

func uiRuntimeCPUUsageFromReading(cpu model.RuntimeCPUUsage) uiRuntimeCPUUsage {
	// UsageUsec is read before the quota, so it is set even without a ceiling.
	if cpu.Unavailable != "" {
		return uiRuntimeCPUUsage{Unavailable: cpu.Unavailable, UsageUsec: cpu.UsageUsec}
	}
	return uiRuntimeCPUUsage{
		Available:          true,
		QuotaCores:         cpu.QuotaCores,
		Quota:              fmt.Sprintf("%.2f cores", cpu.QuotaCores),
		UtilizationPercent: cpu.UtilizationPercent,
		Utilization:        fmt.Sprintf("%.1f%%", cpu.UtilizationPercent),
		UsageUsec:          cpu.UsageUsec,
	}
}

func uiRuntimeMemoryUsageFromReading(memory model.RuntimeMemoryUsage) uiRuntimeMemoryUsage {
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

// nil means never read; an unreadable pod spec arrives as unavailable with its reason.
func uiRuntimeUsageRequestsFromReading(requests *model.RuntimeUsageRequests) *uiRuntimeUsageRequests {
	if requests == nil {
		return nil
	}
	return &uiRuntimeUsageRequests{
		Unavailable: requests.Unavailable,
		Runtime:     uiRuntimeContainerRequestsFromReading(requests.Containers[config.DevopsComponentName]),
	}
}

// CPU/memory strings are formatted once here so every view spells them the same.
func uiRuntimeContainerRequestsFromReading(requests model.KubernetesRequests) uiRuntimeContainerRequests {
	rendered := uiRuntimeContainerRequests{
		CPUMilli:    requests.CPUMilli,
		MemoryBytes: requests.MemoryBytes,
	}
	if requests.CPUMilli > 0 {
		rendered.CPU = config.FormatKubernetesCPUFromMilli(requests.CPUMilli) + " CPU"
	}
	if requests.MemoryBytes > 0 {
		rendered.Memory = formatRuntimeUsageBytes(requests.MemoryBytes)
	}
	return rendered
}

func uiRuntimeDiskUsageFromReading(disk model.RuntimeDiskUsage) uiRuntimeDiskUsage {
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
	// Builds run in erun-dind, so an idle runtime container does not mean an idle environment.
	builds := ""
	if usage.ExcludesBuilds {
		builds = ", excluding builds (they run in the erun-dind sidecar)"
	}
	return "This environment: " + strings.Join(parts, ", ") + builds + "."
}

func formatRuntimeUsageBytes(bytes int64) string {
	if bytes <= 0 {
		return "0 MiB"
	}
	return formatMebibytes(bytes / (1 << 20))
}
