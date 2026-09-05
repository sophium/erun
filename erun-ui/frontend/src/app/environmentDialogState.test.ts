import assert from 'node:assert/strict';
import { test } from 'node:test';

import { missingRequiredFieldReason } from './environmentDialogState';
import { defaultEnvironmentDialog, type EnvironmentDialogState } from './state';

// A submittable baseline: every other required field filled so each test
// below isolates the one condition it is checking.
function submittableDialog(): EnvironmentDialogState {
  return {
    ...defaultEnvironmentDialog(),
    open: true,
    tenant: 'acme',
    environment: 'dev',
    kubernetesContext: 'orbstack',
    containerRegistry: 'ghcr.io/acme',
  };
}

test('selecting the hosted registry while it is still being checked blocks submit', () => {
  const dialog = { ...submittableDialog(), useErunRegistry: true, hostedRegistry: null };
  assert.equal(
    missingRequiredFieldReason(dialog),
    "erun's hosted registry is not available right now. Choose a different registry.",
  );
});

test('selecting the hosted registry when it does not resolve blocks submit with the reason', () => {
  const dialog = {
    ...submittableDialog(),
    useErunRegistry: true,
    hostedRegistry: {
      host: 'registry.erunpaas.com',
      available: false,
      reason: 'does not resolve',
      recovery: 'Choose a different registry instead.',
    },
  };
  assert.equal(
    missingRequiredFieldReason(dialog),
    "erun's hosted registry is not available right now. Choose a different registry.",
  );
});

test('selecting the hosted registry once it is confirmed available needs no container registry', () => {
  const dialog = {
    ...submittableDialog(),
    containerRegistry: '',
    useErunRegistry: true,
    hostedRegistry: { host: 'registry.erunpaas.com', available: true },
  };
  assert.equal(missingRequiredFieldReason(dialog), null);
});

test('not selecting the hosted registry still requires a container registry as before', () => {
  const dialog = { ...submittableDialog(), containerRegistry: '', hostedRegistry: null };
  assert.equal(missingRequiredFieldReason(dialog), 'Select a container registry.');
});
