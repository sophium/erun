import { formatElapsed } from './activityQueueState';

// orchestratorShellLabel names what a running background shell is doing and
// for how long: the operator asked for a spinner four times over one
// session because "1 shell still running" said a process existed without
// saying whether it was still doing anything useful. Elapsed time plus the
// command it is running answers that at a glance instead of requiring a trip
// to the process table.
export function orchestratorShellLabel(
  orchestratorName: string,
  command: string,
  startedAtUnix: number,
  nowMs: number,
): string {
  const elapsed = formatElapsed(new Date(startedAtUnix * 1000).toISOString(), nowMs).trim();
  const suffix = elapsed ? ` for ${elapsed}` : '';
  return command
    ? `${orchestratorName} has a shell running${suffix}: ${command}`
    : `${orchestratorName} has a shell running${suffix}`;
}
