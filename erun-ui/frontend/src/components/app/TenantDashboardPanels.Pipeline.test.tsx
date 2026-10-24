import assert from 'node:assert/strict';

import { Tabs } from 'erun-kit';
import * as React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { Provider } from 'react-redux';
import { test } from 'vitest';

import { store } from '@/app/store';
import type { UITenantDashboard } from '@/types';

import { PipelinePanel } from './TenantDashboardPanels.Pipeline';

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
  assert.ok(markup.includes('Planned'), markup);
  assert.ok(markup.includes('Review open'), markup);
  assert.equal(markup.split('sophium/erun#2683').length - 1, 2, markup);
});

test('shows a planned job with no branch or review behind it', () => {
  const markup = renderPipelineTab(pipelineFixture());
  assert.ok(markup.includes('job · triage · erun/ideas'), markup);
  assert.ok(markup.includes('review ·'), markup);
  assert.ok(markup.includes('bug/2212-issue-ref-from-branch'), markup);
});

test('marks a branch-derived link as inferred', () => {
  const markup = renderPipelineTab(pipelineFixture());
  assert.ok(markup.includes('inferred from branch'), markup);
});

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

test('names the missing read for a restricted caller', () => {
  const markup = renderPipelineTab(
    dashboard({ panels: [{ tab: 'pipeline', restricted: 'GET /v1/pipeline' }] }),
  );
  assert.ok(markup.includes('You do not have access to this panel'), markup);
  assert.ok(!markup.includes('Nothing is in the pipeline'), markup);
});
