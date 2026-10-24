// cumulativeCPUSeconds renders usage_usec as CPU-seconds, for containers with no
// cpu.max quota (no percentage). Null for a missing counter: Go omits a zero
// usageUsec, so 0 would be a confident wrong number.
export function cumulativeCPUSeconds(usageUsec: number | undefined): number | null {
  if (usageUsec === undefined || !Number.isFinite(usageUsec) || usageUsec <= 0) {
    return null;
  }
  return Math.round(usageUsec / 1e6);
}
