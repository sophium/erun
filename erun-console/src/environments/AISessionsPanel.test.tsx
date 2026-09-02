import { cleanup, fireEvent, screen } from '@testing-library/react';
import type { Environment } from 'erun-kit';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { renderWithStore } from '../test/renderWithStore';
import { AISessionsPanel } from './AISessionsPanel';

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

const ENV: Environment = {
  environmentId: 'env-1',
  name: 'agent-1',
  type: 'remote-agent',
  status: 'running',
};

describe('AISessionsPanel', () => {
  it('renders an empty state with no environments', () => {
    renderWithStore(<AISessionsPanel token="dev-token" environments={[]} />);

    expect(screen.getByText('No environments to show AI sessions for')).toBeInTheDocument();
  });

  it('fetches and renders sessions only once expanded', async () => {
    let requested = false;
    vi.stubGlobal(
      'fetch',
      vi.fn((input: string | URL) => {
        requested = true;
        expect(String(input)).toContain('/v1/environments/env-1/ai-sessions');
        return Promise.resolve(
          jsonResponse([
            {
              sessionId: 'ai',
              tool: 'claude',
              state: 'awaiting-input',
              reason: 'finished its turn and is waiting for your next message',
              lastActivity: '2026-08-31T21:53:01Z',
            },
          ]),
        );
      }),
    );
    renderWithStore(<AISessionsPanel token="dev-token" environments={[ENV]} />);

    expect(requested).toBe(false);
    fireEvent.click(screen.getByRole('button', { name: 'Show AI sessions' }));

    expect(await screen.findByText('ai')).toBeInTheDocument();
    expect(screen.getByText('awaiting-input')).toBeInTheDocument();
    expect(
      screen.getByText('finished its turn and is waiting for your next message'),
    ).toBeInTheDocument();
  });

  it('renders an empty state when no sessions have been reported', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.resolve(jsonResponse([]))),
    );
    renderWithStore(<AISessionsPanel token="dev-token" environments={[ENV]} />);

    fireEvent.click(screen.getByRole('button', { name: 'Show AI sessions' }));

    expect(await screen.findByText('No AI sessions reported yet')).toBeInTheDocument();
  });

  it('renders an error when the read fails', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.resolve(jsonResponse({ message: 'forbidden' }, 403))),
    );
    renderWithStore(<AISessionsPanel token="dev-token" environments={[ENV]} />);

    fireEvent.click(screen.getByRole('button', { name: 'Show AI sessions' }));

    expect(await screen.findByText('Could not load AI sessions.')).toBeInTheDocument();
  });
});
