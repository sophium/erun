import assert from 'node:assert/strict';
import { test } from 'node:test';

import { exposedServiceStatus } from './exposedServiceStatus';

test('http scheme is never treated as pending or ready TLS', () => {
  assert.equal(
    exposedServiceStatus({ service: 'api', hostname: 'api.test', scheme: 'http' }),
    'http',
  );
  assert.equal(
    exposedServiceStatus({
      service: 'api',
      hostname: 'api.test',
      scheme: 'http',
      tlsReady: true,
    }),
    'http',
  );
});

// The whole point: an Ingress carrying a tls block is not the same as a
// certificate that has actually issued -- cert-manager writes the block
// before issuance completes.
test('https with no confirmed-ready certificate is pending, not ready', () => {
  assert.equal(
    exposedServiceStatus({ service: 'web', hostname: 'web.test', scheme: 'https' }),
    'https-pending',
  );
  assert.equal(
    exposedServiceStatus({
      service: 'web',
      hostname: 'web.test',
      scheme: 'https',
      tlsReady: false,
      tlsNotReadyReason: 'Issuing: waiting for order to complete',
    }),
    'https-pending',
  );
});

test('https with a confirmed-ready certificate is ready', () => {
  assert.equal(
    exposedServiceStatus({ service: 'web', hostname: 'web.test', scheme: 'https', tlsReady: true }),
    'https-ready',
  );
});
