import { cleanup, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { renderWithStore } from '../test/renderWithStore';
import { QuotaPanel } from './QuotaPanel';

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

describe('QuotaPanel', () => {
  it('renders the caller tenant quota returned by GET /v1/quota', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() =>
        Promise.resolve(
          jsonResponse({
            tenantId: 'tn-1',
            maxEnvironments: 5,
            maxCpuMillicores: 500,
            maxMemoryMb: 512,
            maxStorageGb: 10,
            maxTotalCpuMillicores: 2000,
            maxTotalMemoryMb: 4096,
            maxTotalStorageGb: 100,
          }),
        ),
      ),
    );
    renderWithStore(<QuotaPanel token="dev-token" />);

    expect(await screen.findByText('500m')).toBeInTheDocument();
    expect(screen.getByText('512 MB')).toBeInTheDocument();
    expect(screen.getByText('10 GB')).toBeInTheDocument();
  });

  it('renders an error when the quota read fails', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.resolve(jsonResponse({ message: 'forbidden' }, 403))),
    );
    renderWithStore(<QuotaPanel token="dev-token" />);

    expect(await screen.findByText(/Could not load quota: forbidden/)).toBeInTheDocument();
  });
});
