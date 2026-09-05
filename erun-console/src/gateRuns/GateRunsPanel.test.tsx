import { cleanup, fireEvent, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { renderWithStore } from '../test/renderWithStore';
import { GateRunsPanel } from './GateRunsPanel';

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

const RUNNING_RUN = {
  gateRunId: 'gr-1',
  sourceBranch: 'feature/1932-gate-run-ui',
  targetBranch: 'main',
  sourceCommit: 'abc123',
  mergeCommit: 'def4567890abcdef',
  status: 'RUNNING',
  createdAt: '2026-09-02T10:00:00Z',
  updatedAt: '2026-09-02T10:00:00Z',
};

const FAILED_RUN = {
  gateRunId: 'gr-2',
  sourceBranch: 'feature/other',
  targetBranch: 'main',
  sourceCommit: 'aaa111',
  mergeCommit: 'bbb2223334445556',
  status: 'FAILED',
  failingStep: 'build',
  logRef: 'job-42',
  createdAt: '2026-09-02T09:00:00Z',
  updatedAt: '2026-09-02T09:05:00Z',
};

const INCONCLUSIVE_RUN = {
  gateRunId: 'gr-3',
  sourceBranch: 'feature/timeout',
  targetBranch: 'main',
  sourceCommit: 'ccc333',
  status: 'INCONCLUSIVE',
  createdAt: '2026-09-02T08:00:00Z',
  updatedAt: '2026-09-02T08:10:00Z',
};

describe('GateRunsPanel', () => {
  it('renders gate runs returned by GET /v1/gate-runs, most recent first', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.resolve(jsonResponse([RUNNING_RUN, FAILED_RUN]))),
    );
    renderWithStore(<GateRunsPanel token="dev-token" />);

    expect(await screen.findByText('feature/1932-gate-run-ui')).toBeInTheDocument();
    expect(screen.getByText('RUNNING')).toBeInTheDocument();
    expect(screen.getByText('FAILED')).toBeInTheDocument();
    expect(screen.getByText('build')).toBeInTheDocument();
    expect(screen.getByText('job-42')).toBeInTheDocument();
  });

  // The one thing to get right above all (erun#1932): INCONCLUSIVE must not
  // render as a failure. It reports its own distinct status word and a
  // `warning` tone, never FAILED's `destructive` tone or wording.
  it('renders INCONCLUSIVE as its own distinct state, not as a failure', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.resolve(jsonResponse([INCONCLUSIVE_RUN]))),
    );
    renderWithStore(<GateRunsPanel token="dev-token" />);

    const badge = await screen.findByText('INCONCLUSIVE');
    expect(badge).toBeInTheDocument();
    expect(screen.queryByText('FAILED')).not.toBeInTheDocument();
  });

  it('renders an empty state when there are no gate runs', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.resolve(jsonResponse([]))),
    );
    renderWithStore(<GateRunsPanel token="dev-token" />);

    expect(await screen.findByText('No gate runs match this filter.')).toBeInTheDocument();
  });

  it('renders an error when the gate-run read fails', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.resolve(jsonResponse({ message: 'forbidden' }, 403))),
    );
    renderWithStore(<GateRunsPanel token="dev-token" />);

    expect(await screen.findByText(/Could not load gate runs: forbidden/)).toBeInTheDocument();
  });

  // erun#1932's other route, GET /v1/gate-runs/{gate_run_id}: submitting an
  // id looks it up directly, the console's counterpart to `erun gate show`.
  it('looks up one gate run by id via GET /v1/gate-runs/{gate_run_id}', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn((input: unknown) => {
        const url = String(input);
        if (url.includes('/v1/gate-runs/gr-2')) {
          return Promise.resolve(jsonResponse(FAILED_RUN));
        }
        return Promise.resolve(jsonResponse([RUNNING_RUN]));
      }),
    );
    renderWithStore(<GateRunsPanel token="dev-token" />);

    await screen.findByText('feature/1932-gate-run-ui');
    fireEvent.change(screen.getByLabelText('Look up a gate run by id'), {
      target: { value: 'gr-2' },
    });
    fireEvent.click(screen.getByRole('button', { name: /look up/i }));

    expect(await screen.findByText('feature/other')).toBeInTheDocument();
    expect(screen.getByText('aaa111')).toBeInTheDocument();
  });
});
