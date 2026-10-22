import { cleanup, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { renderWithStore } from '../test/renderWithStore';
import { PipelinePanel } from './PipelinePanel';

function jsonResponse(body: unknown, status = 200): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: () => Promise.resolve(body),
  } as unknown as Response;
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

// plannedJob is work that exists only as a recorded intent: no branch, no
// commit, no review.
const plannedJob = {
  jobId: 'job-planned',
  jobType: 'triage',
  issueRef: 'sophium/erun#2683',
  summary: 'plan the pipeline view',
  status: 'PLANNED',
  actorKind: 'orchestrator',
  actorId: 'erun/ideas',
  startedAt: '2026-09-25T09:00:00Z',
};

// inferredReview is a review whose only link is the number its branch names.
const inferredReview = {
  reviewId: 'review-inferred',
  name: 'Add widget',
  targetBranch: 'main',
  sourceBranch: 'bug/2212-issue-ref-from-branch',
  status: 'OPEN',
};

const PLANNED_ISSUE = {
  issueKey: 'sophium/erun#2683',
  items: [
    {
      issueKey: 'sophium/erun#2683',
      issueRef: 'sophium/erun#2683',
      issueRefSource: 'DECLARED',
      rung: 'PLANNED',
      job: plannedJob,
    },
    {
      issueKey: 'sophium/erun#2683',
      issueRef: 'sophium/erun#2683',
      issueRefSource: 'DECLARED',
      rung: 'REVIEW_OPEN',
      review: { ...inferredReview, reviewId: 'review-open', name: 'Add the view' },
    },
  ],
};

const INFERRED_ISSUE = {
  issueKey: 'sophium/erun#2212',
  items: [
    {
      issueKey: 'sophium/erun#2212',
      issueRef: '2212',
      issueRefSource: 'INFERRED',
      rung: 'REVIEW_OPEN',
      review: inferredReview,
    },
  ],
};

describe('PipelinePanel', () => {
  it('renders every rung the union returned, against its issue', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.resolve(jsonResponse([PLANNED_ISSUE]))),
    );
    renderWithStore(<PipelinePanel token="dev-token" />);

    expect(await screen.findByText('plan the pipeline view')).toBeInTheDocument();
    expect(screen.getByText('Add the view')).toBeInTheDocument();
    expect(screen.getByText('Planned')).toBeInTheDocument();
    expect(screen.getByText('Review open')).toBeInTheDocument();
    // The canonical key is what the work is filed under, and both items share
    // it -- that is the union the panel exists to show.
    expect(screen.getAllByText('sophium/erun#2683')).toHaveLength(3);
  });

  // The whole point of scope 1: a unit of work is visible against its issue
  // before any code exists. Nothing here has a branch, and the row still has
  // to say so plainly rather than inventing one.
  it('shows a planned job with no branch or review behind it', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.resolve(jsonResponse([PLANNED_ISSUE]))),
    );
    renderWithStore(<PipelinePanel token="dev-token" />);

    expect(await screen.findByText('plan the pipeline view')).toBeInTheDocument();
    expect(screen.getByText('job · triage · erun/ideas')).toBeInTheDocument();
    // Nothing about a branch is claimed for it: the row names the job, and
    // the review beside it in the same group is a separate record.
    expect(screen.getByText('review · bug/2212-issue-ref-from-branch → main')).toBeInTheDocument();
  });

  // The provenance rule, rendered: a link parsed out of a branch name must not
  // read as one its author declared.
  it('marks a branch-derived link as inferred', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.resolve(jsonResponse([INFERRED_ISSUE]))),
    );
    renderWithStore(<PipelinePanel token="dev-token" />);

    expect(await screen.findByText('inferred from branch')).toBeInTheDocument();
    expect(screen.getByText('2212')).toBeInTheDocument();
  });

  it('shows the empty state when the tenant has nothing in the pipeline', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.resolve(jsonResponse([]))),
    );
    renderWithStore(<PipelinePanel token="dev-token" />);

    expect(await screen.findByText('Nothing is in the pipeline.')).toBeInTheDocument();
  });

  // A rung this console has never heard of is the platform being ahead of the
  // client. It renders under its own spelling rather than being folded into a
  // rung it resembles, which would describe work the console cannot see.
  it('renders an unrecognised rung under its own spelling', async () => {
    const unknownRung = {
      issueKey: 'sophium/erun#1',
      items: [
        {
          issueKey: 'sophium/erun#1',
          issueRef: '',
          issueRefSource: '',
          rung: 'QUARANTINED',
          job: { ...plannedJob, jobId: 'job-unknown', summary: 'work in an unknown state' },
        },
      ],
    };
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.resolve(jsonResponse([unknownRung]))),
    );
    renderWithStore(<PipelinePanel token="dev-token" />);

    expect(await screen.findByText('work in an unknown state')).toBeInTheDocument();
    expect(screen.getByText('QUARANTINED')).toBeInTheDocument();
  });

  // A failed read must not render as "nothing is in the pipeline" -- the
  // conclusion an operator would act on by doing nothing.
  it('reports a failed read rather than an empty pipeline', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.resolve(jsonResponse({ code: 'BOOM', message: 'nope' }, 500))),
    );
    renderWithStore(<PipelinePanel token="dev-token" />);

    expect(await screen.findByRole('alert')).toBeInTheDocument();
    expect(screen.queryByText('Nothing is in the pipeline.')).not.toBeInTheDocument();
  });
});
