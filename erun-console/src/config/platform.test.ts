import { afterEach, describe, expect, it, vi } from 'vitest';

import { fetchPlatformConfig } from './platform';

function jsonResponse(body: unknown, status = 200): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: () => Promise.resolve(body),
  } as unknown as Response;
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('fetchPlatformConfig', () => {
  it('parses every field of a full response', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() =>
        Promise.resolve(
          jsonResponse({
            issuer: 'https://auth.acme.example',
            apiUrl: 'https://console.acme.example',
            consoleUrl: 'https://console.acme.example',
            consoleClientId: 'console-client',
            cliClientId: 'cli-client',
            brand: 'Acme',
            docsUrl: 'https://docs.acme.example',
            tagline: 'Acme tagline',
            logoUrl: 'https://console.acme.example/logo.svg',
          }),
        ),
      ),
    );

    expect(await fetchPlatformConfig()).toEqual({
      issuer: 'https://auth.acme.example',
      apiUrl: 'https://console.acme.example',
      consoleUrl: 'https://console.acme.example',
      consoleClientId: 'console-client',
      cliClientId: 'cli-client',
      brand: 'Acme',
      docsUrl: 'https://docs.acme.example',
      tagline: 'Acme tagline',
      logoUrl: 'https://console.acme.example/logo.svg',
    });
  });

  it('returns undefined on a 404 (an older backend with no platform endpoint)', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.resolve(jsonResponse('not found', 404))),
    );
    expect(await fetchPlatformConfig()).toBeUndefined();
  });

  it('returns undefined when the backend is unreachable', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.reject(new Error('network error'))),
    );
    expect(await fetchPlatformConfig()).toBeUndefined();
  });

  it('defaults unset fields to empty strings', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.resolve(jsonResponse({ issuer: 'https://auth.acme.example' }))),
    );
    expect(await fetchPlatformConfig()).toEqual({
      issuer: 'https://auth.acme.example',
      apiUrl: '',
      consoleUrl: '',
      consoleClientId: '',
      cliClientId: '',
      brand: '',
      docsUrl: '',
      tagline: '',
      logoUrl: '',
    });
  });
});
