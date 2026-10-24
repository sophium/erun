import { formatElapsed } from './activityQueueState';

// orchestratorBusyElapsed is '' for a missing or zero timestamp rather than an
// invented duration.
export function orchestratorBusyElapsed(busyAtUnix: number | undefined, nowMs: number): string {
  if (busyAtUnix === undefined || busyAtUnix <= 0) {
    return '';
  }
  return formatElapsed(new Date(busyAtUnix * 1000).toISOString(), nowMs).trim();
}

export function orchestratorBusyLabel(
  orchestratorName: string,
  busyAtUnix: number | undefined,
  nowMs: number,
): string {
  const elapsed = orchestratorBusyElapsed(busyAtUnix, nowMs);
  return elapsed
    ? `${orchestratorName} is working, for ${elapsed}`
    : `${orchestratorName} is working`;
}
