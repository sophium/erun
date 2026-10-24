import { formatElapsed } from './activityQueueState';

// orchestratorBusyLabel names a working orchestrator and, when the report
// carries a timestamp, how long it has been working.
// `${name} is working` alone restates the spinner; the elapsed time comes from
// `busyAtUnix`, carried from the same activity report the spinner reads.
// Sidebar.ErunSection's orchestratorShellLabel does the same for a background
// shell.
//
// A missing or zero timestamp degrades to the bare statement rather than
// inventing an elapsed time: an orchestrator that reported busy before this
// field existed, or whose report predates its current session, has no honest
// duration to show. That is the same fail-soft rule the rest of the sidebar
// follows -- say less rather than say something untrue.
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
