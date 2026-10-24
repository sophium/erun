// Read-only job queue endpoints; jobs are written by the actor doing the work.
import { asOptionalString, asString, isRecord, parseList } from 'erun-kit';

import { platformApi } from './platformApi';

export type JobStatus =
  | 'PLANNED'
  | 'RUNNING'
  | 'SUCCEEDED'
  | 'FAILED'
  | 'ABANDONED'
  | 'SUPERSEDED'
  | 'UNKNOWN';

const KNOWN_JOB_STATUSES: JobStatus[] = [
  'PLANNED',
  'RUNNING',
  'SUCCEEDED',
  'FAILED',
  'ABANDONED',
  'SUPERSEDED',
];

// An unknown status stays UNKNOWN rather than RUNNING: the client is behind the platform.
function asJobStatus(value: unknown): JobStatus {
  const raw = asString(value);
  return (KNOWN_JOB_STATUSES as string[]).includes(raw) ? (raw as JobStatus) : 'UNKNOWN';
}

export interface Job {
  jobId: string;
  // Absent for host-side orchestrator work.
  environmentId?: string;
  jobType: string;
  // owner/repo#number.
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
