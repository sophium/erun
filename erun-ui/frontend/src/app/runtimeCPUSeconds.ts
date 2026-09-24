// cumulativeCPUSeconds renders cpu.stat's cumulative usage_usec as whole
// CPU-seconds, or null when the counter carries no measurement.
//
// It exists because a container with no cpu.max quota has no percentage to
// report — utilisation is a share of a ceiling, and there is no ceiling — while
// still having done measurable work. That is the common shape of the erun-dind
// sidecar, which declares no limit so a build can use the node, so on exactly
// the environments where "is the build working?" matters, the percentage-only
// view has nothing to say. CPU-seconds is the honest figure there.
//
// Null, not 0, for a missing counter: the Go side carries usageUsec with
// `omitempty`, so a genuinely-zero reading and an unread one look the same in
// JSON, and a container that has run at all reports some usage. Reporting "0
// CPU-s" for an unread counter would be the confident-wrong-number failure this
// whole family of readings avoids.
export function cumulativeCPUSeconds(usageUsec: number | undefined): number | null {
  if (usageUsec === undefined || !Number.isFinite(usageUsec) || usageUsec <= 0) {
    return null;
  }
  return Math.round(usageUsec / 1e6);
}
