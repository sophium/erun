// RTK Query endpoint for the gate-run queue surface (erun#1932): GET
// /v1/gate-runs, every tenant's own read of what is being gated right now
// and what recent gates decided (see erun-backend-api/AGENTS.md's "Gate
// Runs"). POST/PATCH stay internal permanently -- the environment driving
// the gate reports its own attempt, never something an operator clicks.
import { asOptionalString, asString, isRecord, parseList } from 'erun-kit';

import { platformApi } from './platformApi';

export type GateRunStatus = 'RUNNING' | 'PASSED' | 'FAILED' | 'INCONCLUSIVE';

export interface GateRun {
  gateRunId: string;
  sourceBranch: string;
  targetBranch: string;
  sourceCommit: string;
  // mergeCommit is the prospective squash-merge commit this run actually
  // tested; absent only for a run that failed before that commit existed at
  // all (e.g. a squash conflict).
  mergeCommit?: string;
  reviewId?: string;
  reviewName?: string;
  status: GateRunStatus;
  // failingStep names which gate step produced a FAILED verdict. Absent for
  // every other status, including INCONCLUSIVE -- the whole point of that
  // status is that the gate never reached a real verdict to name a step for.
  failingStep?: string;
  logRef?: string;
  createdAt: string;
  updatedAt: string;
}

function asGateRunStatus(value: unknown): GateRunStatus {
  return value === 'RUNNING' || value === 'PASSED' || value === 'FAILED' || value === 'INCONCLUSIVE'
    ? value
    : 'RUNNING';
}

function parseGateRun(raw: Record<string, unknown>): GateRun {
  return {
    gateRunId: asString(raw.gateRunId),
    sourceBranch: asString(raw.sourceBranch),
    targetBranch: asString(raw.targetBranch),
    sourceCommit: asString(raw.sourceCommit),
    mergeCommit: asOptionalString(raw.mergeCommit),
    reviewId: asOptionalString(raw.reviewId),
    reviewName: asOptionalString(raw.reviewName),
    status: asGateRunStatus(raw.status),
    failingStep: asOptionalString(raw.failingStep),
    logRef: asOptionalString(raw.logRef),
    createdAt: asString(raw.createdAt),
    updatedAt: asString(raw.updatedAt),
  };
}

export interface GateRunFilter {
  targetBranch?: string;
  sourceBranch?: string;
  status?: GateRunStatus;
}

function queryString(filter: GateRunFilter): string {
  const params = new URLSearchParams();
  if (filter.targetBranch !== undefined && filter.targetBranch !== '') {
    params.set('targetBranch', filter.targetBranch);
  }
  if (filter.sourceBranch !== undefined && filter.sourceBranch !== '') {
    params.set('sourceBranch', filter.sourceBranch);
  }
  if (filter.status !== undefined) {
    params.set('status', filter.status);
  }
  const encoded = params.toString();
  return encoded === '' ? '' : `?${encoded}`;
}

export const gateRunsApi = platformApi.injectEndpoints({
  endpoints: (builder) => ({
    // listGateRuns reads the caller's own tenant's gate runs, most recent
    // first -- the same shape `erun gate list` renders, narrowed by the same
    // filters.
    listGateRuns: builder.query<GateRun[], { token: string; filter?: GateRunFilter }>({
      query: ({ token, filter }) => ({
        url: `/v1/gate-runs${queryString(filter ?? {})}`,
        token,
        label: 'list gate runs',
      }),
      transformResponse: (raw: unknown) => parseList(raw, parseGateRun),
      providesTags: ['GateRuns'],
    }),

    // getGateRun looks up one gate run by id -- the same lookup `erun gate
    // show` does, for a caller who already has an id (from a `logRef`, a
    // CLI/MCP report, or a linked review) and wants that one record without
    // scanning the whole queue.
    getGateRun: builder.query<GateRun, { token: string; gateRunId: string }>({
      query: ({ token, gateRunId }) => ({
        url: `/v1/gate-runs/${encodeURIComponent(gateRunId)}`,
        token,
        label: 'get gate run',
      }),
      transformResponse: (raw: unknown) => {
        if (!isRecord(raw)) {
          throw new Error('gate run response was not in the expected shape');
        }
        return parseGateRun(raw);
      },
    }),
  }),
});

export const { useListGateRunsQuery, useGetGateRunQuery } = gateRunsApi;
