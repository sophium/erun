import { formatElapsed } from './activityQueueState';

// orchestratorBusyLabel names a working orchestrator and, when the report
// carries a timestamp, how long it has been working.
//
// #1228 set out to make a spinning orchestrator row say "what it is working on
// and for how long", and shipped a label of `${name} is working` -- which
// restates what the spinner already conveys visually and answers neither half.
// Its own premise check recorded why: `OrchestratorInfo.busy` was a plain
// boolean, so the elapsed figure needed the timestamp carried through, and it
// was not. #1343 carries it (`busyAtUnix`, from the same activity report the
// spinner reads), and this is the label that spends it.
//
// The sibling one function below in Sidebar.ErunSection already does exactly
// this for a background shell via orchestratorShellLabel; a working turn is the
// more important of the two to explain, and was the less informative.
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
