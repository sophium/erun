import type { GateRun, GateRunFilter } from '../app/api/gateRunsApi';
import { useListGateRunsQuery } from '../app/api/gateRunsApi';
import { queryErrorMessage } from '../app/queryError';

export type GateRunsState =
  | { status: 'loading' }
  | { status: 'ready'; runs: GateRun[] }
  | { status: 'error'; message: string };

// useGateRunsController reads the caller's own tenant's gate runs, most
// recent first, narrowed by filter -- the console's counterpart to `erun
// gate list`. Polls so a RUNNING row's own eventual verdict shows up without
// a manual refresh, the same interval EnvironmentsPanel's deploy poll uses.
const POLL_INTERVAL_MS = 5000;

export function useGateRunsController(token: string, filter: GateRunFilter): GateRunsState {
  const query = useListGateRunsQuery({ token, filter }, { pollingInterval: POLL_INTERVAL_MS });
  if (query.error !== undefined) {
    return { status: 'error', message: queryErrorMessage(query.error) };
  }
  if (query.data !== undefined) {
    return { status: 'ready', runs: query.data };
  }
  return { status: 'loading' };
}
