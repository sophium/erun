import assert from 'node:assert/strict';
import { test } from 'node:test';

import {
  type IdleStatus,
  idleStatusAccessibleLabel,
  idleStatusTooltipLines,
} from './Titlebar.helpers';

function idleStatus(overrides: Partial<IdleStatus> = {}): IdleStatus {
  return {
    timeoutSeconds: 300,
    secondsUntilStop: 120,
    stopEligible: false,
    outsideWorkingHours: false,
    managedCloud: true,
    fromPod: true,
    ...overrides,
  };
}

// Regression for erun#1216 bug 3: a reading the pod never confirmed must not
// render with the same confidence as a live one.
test('a host-only idle reading leads its tooltip with a provenance caveat', () => {
  const lines = idleStatusTooltipLines(idleStatus({ fromPod: false }));
  assert.equal(
    lines[0],
    'Not confirmed with the pod: showing the last known state; it may be stale.',
  );
});

test('a pod-confirmed idle reading carries no provenance caveat', () => {
  const lines = idleStatusTooltipLines(idleStatus({ fromPod: true }));
  assert.ok(!lines.some((line) => line.includes('Not confirmed with the pod')));
  assert.equal(lines[0], 'Idle timeout: 300s');
});

test('the accessible label names the same caveat for a host-only reading', () => {
  const label = idleStatusAccessibleLabel(idleStatus({ fromPod: false }));
  assert.ok(label.startsWith('not confirmed with the pod'));
});

test('the accessible label omits the caveat for a pod-confirmed reading', () => {
  const label = idleStatusAccessibleLabel(idleStatus({ fromPod: true }));
  assert.ok(!label.includes('not confirmed with the pod'));
});
