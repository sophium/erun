import type { Job, JobFilter } from '../app/api/jobsApi';
import { useListJobsQuery } from '../app/api/jobsApi';
import { queryErrorMessage } from '../app/queryError';

export type JobsState =
  | { status: 'loading' }
  | { status: 'ready'; jobs: Job[] }
  | { status: 'error'; message: string };

// Polled so started, finished or swept jobs appear and elapsed times stay honest.
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
