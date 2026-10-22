// RTK Query endpoints for the pipeline view: every job and review the caller's
// tenant has, unioned on the issue each belongs to and labelled by the rung it
// stands on. It is the one read that answers "what is going on" without the
// operator joining the job queue and the merge queue by eye.
//
// Read-only, and the server owns the join. The canonical issue key is
// owner/repo#number; a job states one outright and a review's comes from its
// recorded repository joined to its own issueRef -- a spelling difference no
// client should be re-deriving, because two clients deriving it differently is
// exactly how one view of the pipeline stops being one view.
//
// The rung vocabulary itself is not here: it is erun-kit's pipelineRungs, so
// this section and the desktop's Pipeline tab name and tone a rung the same
// way. What is here is this transport's own parsing of the response.
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
  // issueRefSource travels with the link so an inferred one is never rendered
  // as a declared one. A job's reference is stated by the actor that claimed
  // it; a review's is what its author declared, or else the number its branch
  // names -- and only the second is a guess about the branch.
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

// parseItem keeps *which record the item came from* rather than flattening the
// two. A job and a review at the same rung are different things with different
// next actions, and a row that could not say which it was would send an
// operator looking for a branch that does not exist (a job) or a job that does
// (a review).
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
    // getPipeline reads the caller's own tenant's whole pipeline. Unfiltered
    // on purpose: the view's point is the whole picture, and a narrowed one
    // would be answering a different question.
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
