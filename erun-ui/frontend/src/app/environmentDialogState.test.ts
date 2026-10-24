import assert from 'node:assert/strict';

import { test } from 'vitest';

import type {
  UIRuntimeResourceMetric,
  UIRuntimeResourceReading,
  UIRuntimeResourceStatus,
} from '@/types';

import {
  environmentDialogResourceLimitMessage,
  missingRequiredFieldReason,
} from './environmentDialogState';
import { defaultEnvironmentDialog, type EnvironmentDialogState } from './state';

// Every required field filled, so each test isolates one condition.
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

// A node with no request capacity left, so the capacity message is non-empty.
function schedulerExhaustedNodeStatus(): UIRuntimeResourceStatus {
  const metric = (unit: string): UIRuntimeResourceMetric => ({
    total: 0,
    used: 0,
    free: 0,
    unit,
    formatted: `0 ${unit}`,
    floored: true,
  });
  const reading = (): UIRuntimeResourceReading => ({ cpu: metric('cores'), memory: metric('GiB') });
  return {
    kubernetesContext: 'orbstack',
    available: true,
    floored: true,
    measuredUsage: true,
    schedulable: reading(),
    schedulableComplete: true,
    worstCase: reading(),
    nodes: [
      { name: 'node-1', schedulable: reading(), schedulableComplete: true, worstCase: reading() },
    ],
  };
}

test('the runtime-capacity requirement is asked of a pod-backed env', () => {
  const dialog: EnvironmentDialogState = {
    ...submittableDialog(),
    envType: 'local-agent',
    resourceStatus: schedulerExhaustedNodeStatus(),
  };
  assert.notEqual(environmentDialogResourceLimitMessage(dialog), '');
});

test('the runtime-capacity requirement is skipped for a host env', () => {
  // A host env has no pod to size, so the gate and submit must both leave this empty.
  const dialog: EnvironmentDialogState = {
    ...submittableDialog(),
    envType: 'host',
    resourceStatus: schedulerExhaustedNodeStatus(),
  };
  assert.equal(environmentDialogResourceLimitMessage(dialog), '');
});
