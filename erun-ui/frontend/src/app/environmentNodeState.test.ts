import assert from 'node:assert/strict';

import { test } from 'vitest';

import type { UIEnvironmentNodeSnapshot } from '@/uiEnvironmentNodeTypes';

import { cloudNodeState } from './cloudNodeStatus';
import { environmentNodeIndicator, environmentNodeLabel } from './environmentNodeState';

function node(overrides: Partial<UIEnvironmentNodeSnapshot> = {}): UIEnvironmentNodeSnapshot {
  return { name: 'erun-001', label: 'erun-001-eu-west-2', status: 'running', ...overrides };
}

test('a stopped node is the one state that always shows on the row', () => {
  const indicator = environmentNodeIndicator({
    node: node({ status: 'stopped' }),
    environmentIndicatorVisible: true,
  });
  assert.equal(indicator.visible, true);
  assert.equal(indicator.state, 'stopped');
});

test('a stopped node explains an otherwise blank row', () => {
  const indicator = environmentNodeIndicator({
    node: node({ status: 'stopped' }),
    environmentIndicatorVisible: false,
  });
  assert.equal(indicator.visible, true);
  assert.match(indicator.condition, /^Cloud node erun-001-eu-west-2 is stopped/);
});

// The label is read out of context, so it must name the node.
test("the indicator's label names the node and the way out of the state", () => {
  const indicator = environmentNodeIndicator({
    node: node({ status: 'stopped' }),
    environmentIndicatorVisible: false,
  });
  assert.match(indicator.condition, /Cloud node/);
  assert.match(indicator.condition, /start it from the titlebar/);
});

test('an environment with no node reports nothing about a node', () => {
  const indicator = environmentNodeIndicator({
    node: undefined,
    environmentIndicatorVisible: false,
  });
  assert.equal(indicator.visible, false);
  assert.notEqual(indicator.state, 'stopped');
});

test('an unobserved node reads as unknown, not stopped', () => {
  const indicator = environmentNodeIndicator({
    node: node({ status: '' }),
    environmentIndicatorVisible: false,
  });
  assert.equal(indicator.state, 'unknown');
  assert.equal(indicator.visible, true);
  assert.match(indicator.condition, /could not be checked/);
});

test('a stale node reading reads as unknown, not stopped', () => {
  const indicator = environmentNodeIndicator({
    node: node({ status: 'unknown' }),
    environmentIndicatorVisible: false,
  });
  assert.equal(indicator.state, 'unknown');
  assert.match(indicator.condition, /unknown/);
});

test('an undetermined node stays quiet on a row that already reports something', () => {
  const indicator = environmentNodeIndicator({
    node: node({ status: 'unknown' }),
    environmentIndicatorVisible: true,
  });
  assert.equal(indicator.visible, false);
});

test('a running node adds no row indicator', () => {
  const indicator = environmentNodeIndicator({
    node: node({ status: 'running' }),
    environmentIndicatorVisible: false,
  });
  assert.equal(indicator.state, 'running');
  assert.equal(indicator.detail, 'erun-001-eu-west-2 — running');
});

test('a starting node says it is starting', () => {
  const indicator = environmentNodeIndicator({
    node: node({ status: 'pending' }),
    environmentIndicatorVisible: true,
  });
  assert.equal(indicator.visible, true);
  assert.equal(indicator.state, 'pending');
  assert.match(indicator.condition, /is starting/);
});

test('the node label falls back to the node name when no label was resolved', () => {
  assert.equal(environmentNodeLabel(node({ label: '' })), 'erun-001');
  assert.equal(environmentNodeLabel(node({ label: undefined })), 'erun-001');
  assert.equal(environmentNodeLabel(undefined), '');
});

test('the node-state vocabulary maps every known status and refuses to guess', () => {
  assert.equal(cloudNodeState('running'), 'running');
  assert.equal(cloudNodeState(' RUNNING '), 'running');
  assert.equal(cloudNodeState('pending'), 'pending');
  assert.equal(cloudNodeState('stopped'), 'stopped');
  assert.equal(cloudNodeState('unknown'), 'unknown');
  assert.equal(cloudNodeState(''), 'unknown');
  assert.equal(cloudNodeState(undefined), 'unknown');
  assert.equal(cloudNodeState('shutting-down'), 'unknown');
});
