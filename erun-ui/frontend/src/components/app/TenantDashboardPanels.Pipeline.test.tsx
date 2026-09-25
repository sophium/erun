import assert from 'node:assert/strict';

import { Tabs } from 'erun-kit';
import * as React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { Provider } from 'react-redux';
import { test } from 'vitest';

import { store } from '@/app/store';
import type { UITenantDashboard } from '@/types';

import { PipelinePanel } from './TenantDashboardPanels.Pipeline';

// The Pipeline tab is the one surface that shows a piece of work before any
// code exists: a planned job has an issue and no branch at all, so neither the
// reviews list nor the merge queue can show it. These render the real panel
// and assert on the markup an operator reads -- the rung label, the
// provenance marker, and the group that names no issue -- rather than on the
// props that produced it.

function dashboard(overrides: Partial<UITenantDashboard> = {}): UITenantDashboard {
  return {
    tenant: 'frs',
    reviews: [],
    panels: [],
    canCreateReview: false,
    canAdvanceMergeQueue: false,
    canOverrideMergeQueue: false,
    canCreateContext: false,
    canRegisterEnvironment: false,
    canDeployEnvironment: false,
    canStopEnvironment: false,
    canDeleteEnvironment: false,
    canApproveInviteRequests: false,
    canDeclineInviteRequests: false,
    ...overrides,
  };
}

function renderPipelineTab(data: UITenantDashboard): string {
  return renderToStaticMarkup(
    React.createElement(
      Provider,
      { store, children: null },
      React.createElement(
        Tabs,
        { defaultValue: 'pipeline', children: null },
        React.createElement(PipelinePanel, { data }),
      ),
    ),
  );
}

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

function pipelineFixture(): UITenantDashboard {
  return dashboard({
    panels: [{ tab: 'pipeline' }],
    pipeline: [
      {
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
            issueRef: '2683',
            issueRefSource: 'DECLARED',
            rung: 'REVIEW_OPEN',
            review: { ...inferredReview, reviewId: 'review-open', name: 'Add the view' },
          },
        ],
      },
      {
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
      },
    ],
  });
}

test('renders the union against the issue each piece of work belongs to', () => {
  const markup = renderPipelineTab(pipelineFixture());
  assert.ok(markup.includes('plan the pipeline view'), markup);
  assert.ok(markup.includes('Add the view'), markup);
  assert.ok(markup.includes('Add widget'), markup);
  // The rung is labelled by the shared vocabulary, not by this panel.
  assert.ok(markup.includes('Planned'), markup);
  assert.ok(markup.includes('Review open'), markup);
  // The canonical key is what the work is filed under, and every row of the
  // group carries it -- the table's grouping is the platform's order, said
  // out loud on each row rather than left to a layout idiom of its own.
  assert.equal(markup.split('sophium/erun#2683').length - 1, 2, markup);
});

test('shows a planned job with no branch or review behind it', () => {
  const markup = renderPipelineTab(pipelineFixture());
  // The job states what kind of work it is and who holds it. Nothing about a
  // branch is claimed for it.
  assert.ok(markup.includes('job · triage · erun/ideas'), markup);
  assert.ok(markup.includes('review ·'), markup);
  assert.ok(markup.includes('bug/2212-issue-ref-from-branch'), markup);
});

test('marks a branch-derived link as inferred', () => {
  const markup = renderPipelineTab(pipelineFixture());
  assert.ok(markup.includes('inferred from branch'), markup);
});

// The unlinked group is why the view shows work without an issue rather than
// guessing an issue for it. A blank cell would read as a row that failed to
// load; the panel says what the absence is.
test('shows work that names no issue without inventing one', () => {
  const markup = renderPipelineTab(
    dashboard({
      panels: [{ tab: 'pipeline' }],
      pipeline: [
        {
          issueKey: '',
          items: [{ issueKey: '', rung: 'FAILED', job: { ...plannedJob, jobId: 'job-2' } }],
        },
      ],
    }),
  );
  assert.ok(markup.includes('No issue'), markup);
  assert.ok(markup.includes('Failed'), markup);
});

// A rung this build has never heard of is the platform being ahead of the
// desktop. It renders under its own spelling rather than being folded into a
// rung it resembles, which would describe work the panel cannot see.
test('renders an unrecognised rung under its own spelling', () => {
  const markup = renderPipelineTab(
    dashboard({
      panels: [{ tab: 'pipeline' }],
      pipeline: [
        {
          issueKey: '',
          items: [{ issueKey: '', rung: 'QUARANTINED', job: { ...plannedJob, jobId: 'job-3' } }],
        },
      ],
    }),
  );
  assert.ok(markup.includes('QUARANTINED'), markup);
});

test('shows the empty state when the tenant has nothing in the pipeline', () => {
  const markup = renderPipelineTab(dashboard({ panels: [{ tab: 'pipeline' }] }));
  assert.ok(markup.includes('Nothing is in the pipeline'), markup);
});

// A failed read must not render as "nothing is in the pipeline" -- the
// conclusion an operator would act on by doing nothing.
test('reports a failed read rather than an empty pipeline', () => {
  const markup = renderPipelineTab(
    dashboard({
      panels: [
        { tab: 'pipeline', error: 'load tenant dashboard GET /v1/pipeline: 500 Internal Error' },
      ],
    }),
  );
  assert.ok(markup.includes('role="alert"'), markup);
  assert.ok(markup.includes('GET /v1/pipeline'), markup);
  assert.ok(!markup.includes('Nothing is in the pipeline'), markup);
});

// A caller who may not read the pipeline has no tab to open, so the panel is
// never rendered as an empty one -- but if it is rendered anyway, it says why.
test('names the missing read for a restricted caller', () => {
  const markup = renderPipelineTab(
    dashboard({ panels: [{ tab: 'pipeline', restricted: 'GET /v1/pipeline' }] }),
  );
  assert.ok(markup.includes('You do not have access to this panel'), markup);
  assert.ok(!markup.includes('Nothing is in the pipeline'), markup);
});
