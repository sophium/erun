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

  it('names the scoped tenant and reads its quota via ?tenantId= when a scope is set', async () => {
    let requestedUrl = '';
    vi.stubGlobal(
      'fetch',
      vi.fn((input: unknown) => {
        requestedUrl = String(input);
        return Promise.resolve(
          jsonResponse({
            tenantId: 'tn-2',
            maxEnvironments: 9,
            maxCpuMillicores: 900,
            maxMemoryMb: 1024,
            maxStorageGb: 20,
            maxTotalCpuMillicores: 3000,
            maxTotalMemoryMb: 8192,
            maxTotalStorageGb: 200,
          }),
        );
      }),
    );
    renderWithStore(
      <QuotaPanel
        token="dev-token"
        tenants={[
          { tenantId: 'tn-1', name: 'Own Co', type: 'COMPANY', createdAt: '', updatedAt: '' },
          { tenantId: 'tn-2', name: 'Other Co', type: 'COMPANY', createdAt: '', updatedAt: '' },
        ]}
        scopeTenantId="tn-2"
      />,
    );

    expect(await screen.findByText('900m')).toBeInTheDocument();
    expect(screen.getByText('Other Co')).toBeInTheDocument();
    expect(requestedUrl).toContain('tenantId=tn-2');
  });

  it('reports a scoped read failure rather than falling back to the caller tenant', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.resolve(jsonResponse({ message: 'forbidden' }, 403))),
    );
    renderWithStore(
      <QuotaPanel
        token="dev-token"
        tenants={[
          { tenantId: 'tn-2', name: 'Other Co', type: 'COMPANY', createdAt: '', updatedAt: '' },
        ]}
        scopeTenantId="tn-2"
      />,
    );

    expect(await screen.findByText(/Could not load quota: forbidden/)).toBeInTheDocument();
    expect(screen.queryByText(/Environments/)).not.toBeInTheDocument();
  });
});
