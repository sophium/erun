import assert from 'node:assert/strict';
import { test } from 'node:test';

import {
  describeServicePorts,
  exposeFormPatchForService,
  publicLabelForService,
  soleServicePort,
} from './exposeServicePickController';

// The whole point of the picker: a repo-native Service keeps its own name as
// the Ingress backend. Deriving <tenant>-<label> here would route to a Service
// that does not exist, producing a hostname that resolves and an ingress that
// 503s.
test('a picked service routes to itself, not to a derived name', () => {
  const patch = exposeFormPatchForService(
    { name: 'validation-agent-backend-api', ports: [{ port: 8000 }] },
    'validationagent',
  );
  assert.equal(patch.backendService, 'validation-agent-backend-api');
  assert.equal(patch.service, 'validation-agent-backend-api');
  assert.equal(patch.port, '8000');
});

test('a service the tenant prefix produced keeps its clean public label', () => {
  const patch = exposeFormPatchForService({ name: 'frs-api', ports: [{ port: 80 }] }, 'frs');
  assert.equal(patch.service, 'api');
  assert.equal(patch.backendService, 'frs-api');
});

test('only a real prefix is stripped, never a coincidence of letters', () => {
  assert.equal(publicLabelForService('frsapi', 'frs'), 'frsapi');
  assert.equal(publicLabelForService('frs-', 'frs'), 'frs-');
  assert.equal(publicLabelForService('frs-api', ''), 'frs-api');
});

// Guessing between an app port and a metrics port is how internals get
// published by accident, so several ports means the operator picks.
test('the port is filled only when there is no choice to make', () => {
  assert.equal(soleServicePort({ name: 'api', ports: [{ port: 8080 }] }), '8080');
  assert.equal(
    soleServicePort({
      name: 'api',
      ports: [
        { name: 'http', port: 8000 },
        { name: 'metrics', port: 9090 },
      ],
    }),
    '',
  );
  assert.equal(soleServicePort({ name: 'headless' }), '');
});

test('ports are described the way a chart names them', () => {
  assert.equal(
    describeServicePorts({ name: 'api', ports: [{ name: 'http', port: 8000 }, { port: 9090 }] }),
    'http:8000, 9090',
  );
  assert.equal(describeServicePorts({ name: 'headless' }), 'no ports');
});
