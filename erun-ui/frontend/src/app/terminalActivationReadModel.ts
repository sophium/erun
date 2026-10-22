// What the last session activation did to xterm: how many content writes it
// took, and whether it restored a captured screen or re-fed the session's
// retained log. Published on the terminal pane's own data attributes beside
// terminalCols/terminalRows.
//
// This is the only place the difference between the two switch paths is
// observable. They land the same screen in the same time -- xterm coalesces a
// burst of write() calls into a single parse, so re-feeding a session's whole
// retained log parses the bytes a single snapshot write already carries -- so
// no wall-clock measurement of a switch can tell a restore from a replay, and
// a bound meant to catch a pane that went back to re-feeding the log cannot
// red on one. The write count can: it is the cost that actually differs.
//
// The two attributes catch different half-fixes, which is why they travel
// together. A controller that stops capturing a snapshot reports `replay` with
// a write per retained chunk; one that captures a snapshot but never reuses it
// -- the delta stored beside it is never cleared, so a later switch-back
// replays everything -- still reports `snapshot` while the count climbs with
// the log.
export function publishTerminalActivation(
  root: HTMLElement | null,
  writes: number,
  restoredFromSnapshot: boolean,
): void {
  if (!root) {
    return;
  }
  root.dataset.terminalActivationWrites = String(writes);
  root.dataset.terminalActivationSource = restoredFromSnapshot ? 'snapshot' : 'replay';
}
