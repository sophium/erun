// The single mapping from the poller's node status to a UI state, so the sidebar
// and the titlebar agree on what an unreadable node means.
export type CloudNodeState = 'running' | 'pending' | 'stopped' | 'unknown';

// Unobserved (''), stale ('unknown') and unrecognised statuses are all
// 'unknown', never 'stopped'.
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

// cloudNodeIsRunning is not the input to the progressive label (see idleCloudAction).
export function cloudNodeIsRunning(status: string | undefined): boolean {
  return cloudNodeState(status) === 'running';
}
