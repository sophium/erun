import { formatElapsed } from './activityQueueState';

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
