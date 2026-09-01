// The power state of the cloud node an environment's cluster runs on, as the
// Go side's cloud-context poller reports it (erun-ui/cloud_context_cache.go).
// This is the one place a wire status string becomes a state the UI renders:
// two surfaces read it — the sidebar's per-row node indicator and the
// titlebar's start/stop control — and they must not be able to disagree about
// what an unreadable node means.
export type CloudNodeState = 'running' | 'pending' | 'stopped' | 'unknown';

// The poller distinguishes "never observed" ('') from "a known-good reading has
// gone stale" ('unknown'), and both are the same answer to an operator: nothing
// about this node has been established. Neither may become 'stopped'. An
// unrecognised status lands here too — a state this build cannot name is not one
// it may describe.
export function cloudNodeState(status: string | undefined): CloudNodeState {
  switch ((status ?? '').trim().toLowerCase()) {
    case 'running':
      return 'running';
    case 'pending':
      return 'pending';
    case 'stopped':
      return 'stopped';
    default:
      return 'unknown';
  }
}

// cloudNodeIsRunning answers the one question the titlebar's power control
// asks of the current state: does this node's button offer to stop it. It is
// deliberately not the input to the *progressive* label — see idleCloudAction.
export function cloudNodeIsRunning(status: string | undefined): boolean {
  return cloudNodeState(status) === 'running';
}
