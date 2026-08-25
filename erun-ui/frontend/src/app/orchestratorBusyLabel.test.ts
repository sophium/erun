import assert from 'node:assert/strict';
import { test } from 'node:test';

import { orchestratorBusyLabel } from './orchestratorBusyLabel';

const NOW = Date.UTC(2026, 7, 25, 12, 0, 0);
const at = (secondsAgo: number): number => Math.floor(NOW / 1000) - secondsAgo;

// This is the half #1228 promised and did not deliver: its label was
// `${name} is working`, which restates the spinner. A duration is the thing the
// operator cannot get any other way.
test('a timestamped report says how long it has been working', () => {
  assert.equal(orchestratorBusyLabel('erun-prod', at(252), NOW), 'erun-prod is working, for 4m12s');
});

// Fail-soft: no timestamp is not zero seconds. An orchestrator that reported
// busy before busyAtUnix existed must degrade to the bare statement rather than
// claim it started working exactly now.
test('a report with no timestamp degrades instead of inventing a duration', () => {
  assert.equal(orchestratorBusyLabel('erun-prod', undefined, NOW), 'erun-prod is working');
  assert.equal(orchestratorBusyLabel('erun-prod', 0, NOW), 'erun-prod is working');
});

test('a fresh turn reads in seconds rather than an empty duration', () => {
  assert.equal(orchestratorBusyLabel('petios-qa', at(3), NOW), 'petios-qa is working, for 3s');
});

test('a long turn does not collapse to minutes only', () => {
  assert.equal(
    orchestratorBusyLabel('erun-prod', at(3 * 3600 + 25 * 60), NOW),
    'erun-prod is working, for 3h25m',
  );
});
