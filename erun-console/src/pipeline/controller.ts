import type { PipelineIssue } from '../app/api/pipelineApi';
import { useGetPipelineQuery } from '../app/api/pipelineApi';
import { queryErrorMessage } from '../app/queryError';

export type PipelineState =
  | { status: 'loading' }
  | { status: 'ready'; issues: PipelineIssue[] }
  | { status: 'error'; message: string };

// Polled at the job queue's interval so moved rungs show up.
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
