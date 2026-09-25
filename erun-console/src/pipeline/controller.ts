import type { PipelineIssue } from '../app/api/pipelineApi';
import { useGetPipelineQuery } from '../app/api/pipelineApi';
import { queryErrorMessage } from '../app/queryError';

export type PipelineState =
  | { status: 'loading' }
  | { status: 'ready'; issues: PipelineIssue[] }
  | { status: 'error'; message: string };

// usePipelineController reads the tenant's whole pipeline -- jobs and reviews
// unioned on the issue each belongs to. Polls, for the same reason the job
// queue does: the view exists to answer "what is going on" while things are
// going on, and a rung that moved is only worth watching if the panel behind
// it is current. Same interval as the job and gate-run queues.
const POLL_INTERVAL_MS = 5000;

export function usePipelineController(token: string): PipelineState {
  const query = useGetPipelineQuery({ token }, { pollingInterval: POLL_INTERVAL_MS });
  if (query.error !== undefined) {
    return { status: 'error', message: queryErrorMessage(query.error) };
  }
  if (query.data !== undefined) {
    return { status: 'ready', issues: query.data };
  }
  return { status: 'loading' };
}
