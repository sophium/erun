import type { UIBuildCgroupMetrics } from '@/types';

// buildProfileFormat.ts holds the pure formatting/classification helpers the
// Builds tab's per-build profile view needs -- kept separate from the
// component per erun-ui/AGENTS.md's "keep model files free of behavior, put
// formatting logic in focused helper modules" guidance.

// formatDurationSeconds renders a step's own duration the way an operator
// scanning a table reads it: sub-second precision below a second (a
// near-instant cached step should not round to "0s"), one decimal of
// seconds below a minute, and minutes+seconds above that.
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

// formatBytes renders a byte count in binary units (MiB/GiB), matching the
// vocabulary erun's own CLI output and the runtime-usage panel already use
// for memory/disk figures.
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

// cgroupIsUsable is the one gate every cgroup-derived cell checks first: a
// step with no cgroup at all (never applicable) and one whose read failed
// (available: false) must both render "not available", never zeros -- a
// zero here would misread as "this step used no CPU" rather than "we could
// not tell".
export function cgroupIsUsable(
  cgroup: UIBuildCgroupMetrics | undefined,
): cgroup is UIBuildCgroupMetrics {
  return cgroup?.available ?? false;
}

// cpuLabel renders a step's CPU cost against its quota. Renders just the
// seconds when no quota percentage is available (e.g. outside a runtime pod
// preview) rather than fabricating a percentage.
export function cpuLabel(cgroup: UIBuildCgroupMetrics): string {
  const seconds = `${(cgroup.cpuSeconds ?? 0).toFixed(1)}s`;
  if (cgroup.cpuPercentOfQuota !== undefined && cgroup.cpuPercentOfQuota > 0) {
    return `${seconds} (${String(Math.round(cgroup.cpuPercentOfQuota))}% of quota)`;
  }
  return seconds;
}

// throttleRatioLabel is the "starved vs merely busy" signal called out
// explicitly in root AGENTS.md: a step at 100% CPU that is not throttled is
// sized correctly, and one at 18% that is throttled is starved. Duration
// alone, and a single CPU percentage alone, cannot distinguish those -- only
// the throttled/total periods ratio can, so this is rendered as that ratio,
// never collapsed into one percentage.
export function throttleRatioLabel(cgroup: UIBuildCgroupMetrics): string | undefined {
  if (cgroup.totalPeriods === undefined || cgroup.totalPeriods <= 0) {
    return undefined;
  }
  return `throttled ${String(cgroup.throttledPeriods ?? 0)}/${String(cgroup.totalPeriods)} periods`;
}

// ioLabel renders read/written bytes together, or undefined when there is
// nothing to report (both zero or absent) -- an all-zero I/O row is a
// legitimate outcome for a fully cache-hit step, not worth a dedicated
// "not available" treatment the way a missing cgroup read is.
export function ioLabel(cgroup: UIBuildCgroupMetrics): string | undefined {
  if (!cgroup.ioReadBytes && !cgroup.ioWriteBytes) {
    return undefined;
  }
  return `${formatBytes(cgroup.ioReadBytes)} read / ${formatBytes(cgroup.ioWriteBytes)} written`;
}

// buildProfileViewButtonLabel names the row's own build id in the "View
// profile" button's accessible label, so a screen reader announces which
// build's profile a row's button opens instead of an ambiguous "View
// profile" repeated once per row (WCAG 2.2 recognition-over-recall /
// accessible naming).
export function buildProfileViewButtonLabel(buildId: string): string {
  return `View build profile for ${buildId}`;
}
