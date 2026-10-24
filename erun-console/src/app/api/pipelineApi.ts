// Read-only pipeline view: jobs and reviews joined server-side on owner/repo#number,
// so clients never re-derive the issue key.
import { asOptionalString, asString, isRecord, parseList, type PipelineRung } from 'erun-kit';

import { platformApi } from './platformApi';

export type PipelineIssueRefSource = 'DECLARED' | 'INFERRED' | '';

export interface PipelineJob {
  jobId: string;
  jobType: string;
  issueRef?: string;
  summary: string;
  status: string;
  actorKind: string;
  actorId: string;
  startedAt: string;
  endedAt?: string;
}

export interface PipelineReview {
  reviewId: string;
  repository?: string;
  name: string;
  targetBranch: string;
  sourceBranch: string;
  status: string;
}

export interface PipelineItem {
  issueKey: string;
  issueRef?: string;
  // Distinguishes a declared issue ref from one inferred from the branch name.
  issueRefSource: PipelineIssueRefSource;
  rung: PipelineRung;
  job?: PipelineJob;
  review?: PipelineReview;
}

export interface PipelineIssue {
  issueKey: string;
  items: PipelineItem[];
}

function asIssueRefSource(value: unknown): PipelineIssueRefSource {
  const raw = asString(value);
  return raw === 'DECLARED' || raw === 'INFERRED' ? raw : '';
}

function parseJob(raw: Record<string, unknown>): PipelineJob {
  return {
    jobId: asString(raw.jobId),
    jobType: asString(raw.jobType),
    issueRef: asOptionalString(raw.issueRef),
    summary: asString(raw.summary),
    status: asString(raw.status),
    actorKind: asString(raw.actorKind),
    actorId: asString(raw.actorId),
    startedAt: asString(raw.startedAt),
    endedAt: asOptionalString(raw.endedAt),
  };
}

function parseReview(raw: Record<string, unknown>): PipelineReview {
  return {
    reviewId: asString(raw.reviewId),
    repository: asOptionalString(raw.repository),
    name: asString(raw.name),
    targetBranch: asString(raw.targetBranch),
    sourceBranch: asString(raw.sourceBranch),
    status: asString(raw.status),
  };
}

// parseItem keeps the record kind: a job and a review at the same rung have different next actions.
function parseItem(raw: unknown): PipelineItem {
  if (!isRecord(raw)) {
    throw new Error('pipeline item was not in the expected shape');
  }
  const job = isRecord(raw.job) ? parseJob(raw.job) : undefined;
  const review = isRecord(raw.review) ? parseReview(raw.review) : undefined;
  if (job === undefined && review === undefined) {
    throw new Error('pipeline item carried neither a job nor a review');
  }
  return {
    issueKey: asString(raw.issueKey),
    issueRef: asOptionalString(raw.issueRef),
    issueRefSource: asIssueRefSource(raw.issueRefSource),
    rung: asString(raw.rung),
    job,
    review,
  };
}

function parseIssue(raw: unknown): PipelineIssue {
  if (!isRecord(raw)) {
    throw new Error('pipeline issue was not in the expected shape');
  }
  return {
    issueKey: asString(raw.issueKey),
    items: parseList(raw.items, parseItem),
  };
}

export const pipelineApi = platformApi.injectEndpoints({
  endpoints: (builder) => ({
    getPipeline: builder.query<PipelineIssue[], { token: string }>({
      query: ({ token }) => ({
        url: '/v1/pipeline',
        token,
        label: 'read pipeline',
      }),
      transformResponse: (raw: unknown) => parseList(raw, parseIssue),
      providesTags: ['Pipeline'],
    }),
  }),
});

export const { useGetPipelineQuery } = pipelineApi;
