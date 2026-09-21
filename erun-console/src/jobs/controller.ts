import type { Job, JobFilter } from '../app/api/jobsApi';
import { useListJobsQuery } from '../app/api/jobsApi';
import { queryErrorMessage } from '../app/queryError';

export type JobsState =
  | { status: 'loading' }
  | { status: 'ready'; jobs: Job[] }
  | { status: 'error'; message: string };

// useJobsController reads the caller's own tenant's job queue -- the
// console's counterpart to `erun job list`. Polls, because the whole point of
// the queue is watching work in flight: a job that starts, finishes, or is
// swept to ABANDONED has to show up without a manual refresh, and a RUNNING
// row's elapsed time is only honest if the list behind it is current. Same
// interval the gate-run queue polls at.
const POLL_INTERVAL_MS = 5000;

export function useJobsController(token: string, filter: JobFilter): JobsState {
  const query = useListJobsQuery({ token, filter }, { pollingInterval: POLL_INTERVAL_MS });
  if (query.error !== undefined) {
    return { status: 'error', message: queryErrorMessage(query.error) };
  }
  if (query.data !== undefined) {
    return { status: 'ready', jobs: query.data };
  }
  return { status: 'loading' };
}
