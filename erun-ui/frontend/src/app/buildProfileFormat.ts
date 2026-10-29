import type { UIBuildCgroupMetrics } from '@/types';

// formatDurationSeconds keeps sub-second precision so cached steps do not read "0s".
export function formatDurationSeconds(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) {
    return '—';
  }
  if (seconds < 1) {
    return `${String(Math.round(seconds * 1000))}ms`;
  }
  if (seconds < 60) {
    return `${seconds.toFixed(1)}s`;
  }
  const minutes = Math.floor(seconds / 60);
  const remainingSeconds = Math.round(seconds % 60);
  return `${String(minutes)}m ${String(remainingSeconds)}s`;
}

export function formatBytes(bytes: number | undefined): string {
  if (bytes === undefined || !Number.isFinite(bytes) || bytes < 0) {
    return '—';
  }
  const mebibyte = 1024 * 1024;
  if (bytes < mebibyte) {
    return `${String(Math.round(bytes / 1024))} KiB`;
  }
  const gibibyte = 1024 * mebibyte;
  if (bytes < gibibyte) {
    return `${(bytes / mebibyte).toFixed(1)} MiB`;
  }
  return `${(bytes / gibibyte).toFixed(2)} GiB`;
}

// cgroupIsUsable gates every cgroup cell: missing or failed reads render "not
// available", never zeros.
export function cgroupIsUsable(
  cgroup: UIBuildCgroupMetrics | undefined,
): cgroup is UIBuildCgroupMetrics {
  return cgroup?.available ?? false;
}

// cpuLabel shows only seconds when there is no quota percentage.
export function cpuLabel(cgroup: UIBuildCgroupMetrics): string {
  const seconds = `${(cgroup.cpuSeconds ?? 0).toFixed(1)}s`;
  if (cgroup.cpuPercentOfQuota !== undefined && cgroup.cpuPercentOfQuota > 0) {
    return `${seconds} (${String(Math.round(cgroup.cpuPercentOfQuota))}% of quota)`;
  }
  return seconds;
}

// throttleRatioLabel separates starved from busy steps, which a CPU percentage
// alone cannot.
export function throttleRatioLabel(cgroup: UIBuildCgroupMetrics): string | undefined {
  if (cgroup.totalPeriods === undefined || cgroup.totalPeriods <= 0) {
    return undefined;
  }
  return `throttled ${String(cgroup.throttledPeriods ?? 0)}/${String(cgroup.totalPeriods)} periods`;
}

// ioLabel returns undefined for all-zero I/O, which is normal for cache hits.
export function ioLabel(cgroup: UIBuildCgroupMetrics): string | undefined {
  if (!cgroup.ioReadBytes && !cgroup.ioWriteBytes) {
    return undefined;
  }
  return `${formatBytes(cgroup.ioReadBytes)} read / ${formatBytes(cgroup.ioWriteBytes)} written`;
}

export function buildProfileViewButtonLabel(buildId: string): string {
  return `View build profile for ${buildId}`;
}
