import assert from 'node:assert/strict';

import { test } from 'vitest';

import type { UIRuntimeResourceMetric, UIRuntimeResourceStatus } from '@/types';

import {
  environmentDialogResourceLimitMessage,
  missingRequiredFieldReason,
} from './environmentDialogState';
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

// A reading whose only node is fully committed, which is what makes the
// capacity message non-empty: no node can fit the dialog's runtime pod.
function fullyCommittedNodeStatus(): UIRuntimeResourceStatus {
  const metric = (unit: string): UIRuntimeResourceMetric => ({
    total: 0,
    used: 0,
    free: 0,
    unit,
    formatted: `0 ${unit}`,
    floored: true,
  });
  return {
    kubernetesContext: 'orbstack',
    available: true,
    floored: true,
    measuredUsage: true,
    cpu: metric('cores'),
    memory: metric('GiB'),
    nodes: [{ name: 'node-1', cpu: metric('cores'), memory: metric('GiB') }],
  };
}

test('the runtime-capacity requirement is asked of a pod-backed env', () => {
  const dialog: EnvironmentDialogState = {
    ...submittableDialog(),
    envType: 'local-agent',
    resourceStatus: fullyCommittedNodeStatus(),
  };
  assert.notEqual(environmentDialogResourceLimitMessage(dialog), '');
});

test('the runtime-capacity requirement is skipped for a host env', () => {
  // The submit gate enables Create for a host env by skipping the cluster-shaped
  // blockers, so this has to stay empty for the same env. A host env has no pod
  // to size and createHostEnvConfig records no runtime resources at all, so a
  // message here would refuse a Create that the host form renders no field to
  // clear -- a dead end whose only exit is closing the dialog. Both the gate and
  // submit call this helper, which is what keeps them from disagreeing again.
  const dialog: EnvironmentDialogState = {
    ...submittableDialog(),
    envType: 'host',
    resourceStatus: fullyCommittedNodeStatus(),
  };
  assert.equal(environmentDialogResourceLimitMessage(dialog), '');
});
