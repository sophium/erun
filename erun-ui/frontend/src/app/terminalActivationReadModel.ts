// Publishes the last activation's write count and mode (snapshot vs replay).
// xterm coalesces writes, so timing cannot tell a restore from a replay; the
// write count can.
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
