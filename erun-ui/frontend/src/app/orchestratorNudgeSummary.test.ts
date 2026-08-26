import assert from 'node:assert/strict';
import { test } from 'node:test';

import { orchestratorNudgeSummary } from './orchestratorNudgeSummary';

test('never nudged is distinct from capped', () => {
  const summary = orchestratorNudgeSummary(
    { nudgeCount: 0, nudgeCapped: false, lastNudgeAtUnix: 0 },
    Date.now(),
  );
  assert.equal(summary, 'Not nudged');
});

test('a nudge count with no cap names the count and how long ago', () => {
  const now = Date.now();
  const lastNudgeAtUnix = Math.floor(now / 1000) - 65;
  const summary = orchestratorNudgeSummary(
    { nudgeCount: 2, nudgeCapped: false, lastNudgeAtUnix },
    now,
  );
  assert.match(summary, /^Nudged 2x, last .+ ago$/);
});

test('a capped session names the attempt count and the recovery, not just "quiet"', () => {
  const summary = orchestratorNudgeSummary(
    { nudgeCount: 6, nudgeCapped: true, lastNudgeAtUnix: 0 },
    Date.now(),
  );
  assert.match(summary, /^Stopped nudging after 6 attempts/);
  assert.match(summary, /reply or restart/);
});
