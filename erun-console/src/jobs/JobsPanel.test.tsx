import { cleanup, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { renderWithStore } from '../test/renderWithStore';
import { JobsPanel } from './JobsPanel';

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

const RUNNING_JOB = {
  jobId: 'job-1',
  jobType: 'fix',
  issueRef: 'sophium/erun#2109',
  summary: 'fixing the jobs claim race on the console queue',
  status: 'RUNNING',
  actorKind: 'agent',
  actorId: 'erun/code4',
  startedAt: '2026-09-21T12:00:00Z',
  updatedAt: '2026-09-21T12:00:00Z',
};

const RUNNING_GATE_JOB = {
  jobId: 'job-2',
  jobType: 'gate',
  summary: 'gating the prospective merge of the jobs branch',
  status: 'RUNNING',
  actorKind: 'orchestrator',
  actorId: 'erun',
  startedAt: '2026-09-21T12:05:00Z',
  updatedAt: '2026-09-21T12:05:00Z',
};

const ABANDONED_JOB = {
  jobId: 'job-3',
  jobType: 'fix',
  summary: 'a job whose actor went quiet',
  status: 'ABANDONED',
  actorKind: 'agent',
  actorId: 'erun/code9',
  startedAt: '2026-09-21T09:00:00Z',
  endedAt: '2026-09-21T09:30:00Z',
  updatedAt: '2026-09-21T09:30:00Z',
};

describe('JobsPanel', () => {
  it('renders the queue returned by GET /v1/jobs with summary, actor and elapsed time', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.resolve(jsonResponse([RUNNING_JOB]))),
    );
    renderWithStore(<JobsPanel token="dev-token" />);

    expect(
      await screen.findByText('fixing the jobs claim race on the console queue'),
    ).toBeInTheDocument();
    expect(screen.getByText('erun/code4')).toBeInTheDocument();
    expect(screen.getByText('agent')).toBeInTheDocument();
    // A running row is grouped under its job type rather than carrying a
    // status badge: everything in the live half is RUNNING by construction,
    // so repeating the word on every row would be noise.
    expect(screen.getByRole('heading', { name: /fix/ })).toBeInTheDocument();
  });

  it('links an issue_ref to the issue it names', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.resolve(jsonResponse([RUNNING_JOB]))),
    );
    renderWithStore(<JobsPanel token="dev-token" />);

    const link = await screen.findByRole('link', { name: 'sophium/erun#2109' });
    expect(link).toHaveAttribute('href', 'https://github.com/sophium/erun/issues/2109');
  });

  // The live queue leads and is grouped by job type, which is the grouping an
  // operator scans for "is anyone gating?". A job with no issue still has to
  // appear -- most work is not issue-driven.
  it('groups running jobs by job type', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.resolve(jsonResponse([RUNNING_JOB, RUNNING_GATE_JOB]))),
    );
    renderWithStore(<JobsPanel token="dev-token" />);

    await screen.findByText('fixing the jobs claim race on the console queue');
    expect(screen.getByRole('heading', { name: /fix/ })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: /gate/ })).toBeInTheDocument();
  });

  // ABANDONED is a job the platform swept because its actor stopped updating
  // it. It is not a failure and not a success, so it renders under its own
  // status word rather than being folded into either.
  it('renders ABANDONED as its own state, distinct from FAILED', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.resolve(jsonResponse([ABANDONED_JOB]))),
    );
    renderWithStore(<JobsPanel token="dev-token" />);

    expect(await screen.findByText('ABANDONED')).toBeInTheDocument();
    expect(screen.queryByText('FAILED')).not.toBeInTheDocument();
  });

  it('renders an empty state when there are no jobs', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.resolve(jsonResponse([]))),
    );
    renderWithStore(<JobsPanel token="dev-token" />);

    expect(await screen.findByText('No jobs match this filter.')).toBeInTheDocument();
  });

  it('renders an error when the job read fails', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.resolve(jsonResponse({ message: 'forbidden' }, 403))),
    );
    renderWithStore(<JobsPanel token="dev-token" />);

    expect(await screen.findByText(/Could not load jobs: forbidden/)).toBeInTheDocument();
  });

  // The panel must never render the command behind a job. A job's summary is
  // prose by contract, and the row has no field that could carry a command
  // line -- this pins that the pod-side technical detail stays out of the
  // queue.
  it('never renders a command line for a job', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() =>
        Promise.resolve(
          jsonResponse([
            {
              ...RUNNING_JOB,
              command: 'git -C /home/erun/git/erun rebase --onto main~1',
              localJobId: 'job-local-1',
            },
          ]),
        ),
      ),
    );
    renderWithStore(<JobsPanel token="dev-token" />);

    await screen.findByText('fixing the jobs claim race on the console queue');
    expect(screen.queryByText(/rebase --onto/)).not.toBeInTheDocument();
  });

  // A status this console has not been taught must be preserved as itself
  // rather than guessed at. Folding an unknown value into RUNNING would claim
  // the job is in progress, which is exactly what is in doubt.
  it('preserves an unrecognised status instead of guessing at it', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.resolve(jsonResponse([{ ...RUNNING_JOB, status: 'CANCELLED' }]))),
    );
    renderWithStore(<JobsPanel token="dev-token" />);

    expect(await screen.findByText('UNKNOWN')).toBeInTheDocument();
    expect(screen.queryByText('RUNNING')).not.toBeInTheDocument();
  });
});
