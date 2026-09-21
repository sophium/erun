// RTK Query endpoints for the job queue: what agents and orchestrators are
// working on right now, and what they recently finished. A job is recorded
// *before* the work starts, which is the half builds and gate runs do not
// have -- they report outcomes after the fact.
//
// Read-only on purpose. The queue is written by the actor doing the work
// (`erun job` / the MCP job tools), the same self-report shape as
// POST /v1/reviews/{review_id}/builds: an operator's part is watching the
// queue, not editing someone else's row.
import { asOptionalString, asString, isRecord, parseList } from 'erun-kit';

import { platformApi } from './platformApi';

export type JobStatus = 'RUNNING' | 'SUCCEEDED' | 'FAILED' | 'ABANDONED' | 'SUPERSEDED' | 'UNKNOWN';

const KNOWN_JOB_STATUSES: JobStatus[] = [
  'RUNNING',
  'SUCCEEDED',
  'FAILED',
  'ABANDONED',
  'SUPERSEDED',
];

// An unrecognised status is preserved as UNKNOWN rather than folded into
// RUNNING. A status this console does not know about means the platform is
// ahead of the client, and rendering it as "in progress" would state
// something definite about a job whose state is exactly what is in doubt.
function asJobStatus(value: unknown): JobStatus {
  const raw = asString(value);
  return (KNOWN_JOB_STATUSES as string[]).includes(raw) ? (raw as JobStatus) : 'UNKNOWN';
}

export interface Job {
  jobId: string;
  // environmentId is absent for host-side orchestrator work that never enters
  // an environment -- a real case, not a gap.
  environmentId?: string;
  jobType: string;
  // issueRef is the issue this work belongs to, in owner/repo#number form,
  // absent when the work is not issue-driven.
  issueRef?: string;
  summary: string;
  status: JobStatus;
  actorKind: string;
  actorId: string;
  scope?: string;
  localJobId?: string;
  startedAt: string;
  endedAt?: string;
  updatedAt: string;
}

function parseJob(raw: Record<string, unknown>): Job {
  return {
    jobId: asString(raw.jobId),
    environmentId: asOptionalString(raw.environmentId),
    jobType: asString(raw.jobType),
    issueRef: asOptionalString(raw.issueRef),
    summary: asString(raw.summary),
    status: asJobStatus(raw.status),
    actorKind: asString(raw.actorKind),
    actorId: asString(raw.actorId),
    scope: asOptionalString(raw.scope),
    localJobId: asOptionalString(raw.localJobId),
    startedAt: asString(raw.startedAt),
    endedAt: asOptionalString(raw.endedAt),
    updatedAt: asString(raw.updatedAt),
  };
}

export interface JobFilter {
  status?: JobStatus;
  environmentId?: string;
  issueRef?: string;
  scope?: string;
  actorId?: string;
}

// queryString omits an absent or empty filter rather than sending it as an
// empty value: the server treats an absent filter as "no narrowing", and a
// blank one would have to mean the same thing for no gain.
function queryString(filter: JobFilter): string {
  const params = new URLSearchParams();
  const candidates: [string, string | undefined][] = [
    ['status', filter.status],
    ['environmentId', filter.environmentId],
    ['issueRef', filter.issueRef],
    ['scope', filter.scope],
    ['actorId', filter.actorId],
  ];
  for (const [name, value] of candidates) {
    if (value !== undefined && value !== '') {
      params.set(name, value);
    }
  }
  const encoded = params.toString();
  return encoded === '' ? '' : `?${encoded}`;
}

export const jobsApi = platformApi.injectEndpoints({
  endpoints: (builder) => ({
    // listJobs reads the caller's own tenant's queue, RUNNING first -- the
    // console's counterpart to `erun job list`.
    listJobs: builder.query<Job[], { token: string; filter?: JobFilter }>({
      query: ({ token, filter }) => ({
        url: `/v1/jobs${queryString(filter ?? {})}`,
        token,
        label: 'list jobs',
      }),
      transformResponse: (raw: unknown) => parseList(raw, parseJob),
      providesTags: ['Jobs'],
    }),

    getJob: builder.query<Job, { token: string; jobId: string }>({
      query: ({ token, jobId }) => ({
        url: `/v1/jobs/${encodeURIComponent(jobId)}`,
        token,
        label: 'get job',
      }),
      transformResponse: (raw: unknown) => {
        if (!isRecord(raw)) {
          throw new Error('job response was not in the expected shape');
        }
        return parseJob(raw);
      },
    }),
  }),
});

export const { useListJobsQuery, useGetJobQuery } = jobsApi;
