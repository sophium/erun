import assert from 'node:assert/strict';
import { test } from 'node:test';

import { idleCloudContextAction } from '@/app/cloudContextState';
import { cloudNodeOperationFor } from '@/app/cloudNodeOperations';
import type { CloudNodeOperation } from '@/app/model';

import {
  idleCloudAction,
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

// The busy label must describe the operation actually in flight. Derived from
// the node's current state, a start against a node the poller has just seen
// come up announced "Stopping <node>" — a teardown that was not happening.
//
// assert.ok narrows first everywhere below: node:assert/strict's equal carries
// an `asserts actual is T` signature, so an optional chain after the first
// assertion is dead syntax the lint gate rejects.
test('a start in flight against a running node still labels Starting', () => {
  const action = idleCloudAction(
    idleStatus({ cloudContextName: 'node-a', cloudContextStatus: 'running' }),
    'start',
  );
  assert.ok(action);
  assert.equal(action.label, 'Starting node-a');
  assert.equal(action.action, 'start');
  assert.equal(action.busy, true);
});

test('a stop in flight against a running node labels Stopping', () => {
  const action = idleCloudAction(
    idleStatus({ cloudContextName: 'node-a', cloudContextStatus: 'running' }),
    'stop',
  );
  assert.ok(action);
  assert.equal(action.label, 'Stopping node-a');
  assert.equal(action.action, 'stop');
});

// The idle verb still comes from the node's state: a running node's button
// offers to stop it.
test('with nothing in flight the label offers the action the state calls for', () => {
  const running = idleCloudAction(
    idleStatus({ cloudContextName: 'node-a', cloudContextStatus: 'running' }),
    null,
  );
  assert.ok(running);
  assert.equal(running.label, 'Stop node-a');
  const stopped = idleCloudAction(
    idleStatus({ cloudContextName: 'node-a', cloudContextStatus: 'stopped' }),
    null,
  );
  assert.ok(stopped);
  assert.equal(stopped.label, 'Start node-a');
});

// An unreadable node state is not a stopped one, but the control still has to
// offer something — Start is the only move that can help, and the label says
// which node it acts on rather than asserting that node is down.
test('a node whose state is unknown still offers a start rather than nothing', () => {
  const action = idleCloudAction(
    idleStatus({ cloudContextName: 'node-a', cloudContextStatus: 'unknown' }),
    null,
  );
  assert.ok(action);
  assert.equal(action.action, 'start');
  assert.equal(action.busy, false);
});

// The cross-environment bleed: the widget showing node B must render B's idle
// label while an operation runs against node A. The scoping happens at the
// selector, so this asserts it end to end — an operation resolved for a
// different node arrives here as null.
test('an operation against another node leaves this widget on its idle label', () => {
  const operations: Record<string, CloudNodeOperation> = { 'node-a': 'start' };
  const shown = idleStatus({ cloudContextName: 'node-b', cloudContextStatus: 'running' });
  const inFlight = cloudNodeOperationFor(operations, shown.cloudContextName);
  assert.equal(inFlight, null);
  const action = idleCloudAction(shown, inFlight);
  assert.ok(action);
  assert.equal(action.busy, false);
  assert.equal(action.label, 'Stop node-b');
});

test('an operation against this node does reach its own label', () => {
  const operations: Record<string, CloudNodeOperation> = { 'node-a': 'start' };
  const shown = idleStatus({ cloudContextName: 'node-a', cloudContextStatus: 'running' });
  const action = idleCloudAction(shown, cloudNodeOperationFor(operations, shown.cloudContextName));
  assert.ok(action);
  assert.equal(action.label, 'Starting node-a');
});

// The two readings of the old global flag disagreed: one treated it as "no
// action available", the other as "my action is in progress". They now read the
// same per-node record and must agree on every input.
test('the label and the action behind it agree on the same input', () => {
  const shown = idleStatus({ cloudContextName: 'node-a', cloudContextStatus: 'running' });
  const idle = idleCloudContextAction(shown, null);
  assert.ok(idle);
  assert.equal(idle.operation, 'stop');
  const labelled = idleCloudAction(shown, null);
  assert.ok(labelled);
  assert.equal(labelled.action, idle.operation);

  // While an operation is in flight the control offers nothing, and the label
  // reports that same operation rather than a second click's worth of action.
  assert.equal(idleCloudContextAction(shown, 'start'), null);
  const busy = idleCloudAction(shown, 'start');
  assert.ok(busy);
  assert.equal(busy.busy, true);
});

// An unmanaged or unnamed node has no control at all, in both readings.
test('an environment with no managed node gets no control from either reading', () => {
  const unmanaged = idleStatus({ managedCloud: false, cloudContextName: 'node-a' });
  assert.equal(idleCloudAction(unmanaged, null), null);
  assert.equal(idleCloudContextAction(unmanaged, null), null);
  const unnamed = idleStatus({ cloudContextName: '' });
  assert.equal(idleCloudAction(unnamed, null), null);
  assert.equal(idleCloudContextAction(unnamed, null), null);
});
