import assert from 'node:assert/strict';
import { test } from 'node:test';

import { orchestratorNudgeSummary } from './orchestratorNudgeSummary';

function fields(overrides: Partial<Parameters<typeof orchestratorNudgeSummary>[0]> = {}) {
  return {
    nudgeCount: 0,
    nudgeCapped: false,
    autoNudgeCount: 0,
    lastAutoNudgeAtUnix: 0,
    whipCount: 0,
    lastWhipAtUnix: 0,
    lastCappedAtUnix: 0,
    ...overrides,
  };
}

test('never nudged is distinct from capped', () => {
  const summary = orchestratorNudgeSummary(fields(), Date.now());
  assert.equal(summary, 'Not nudged');
});

test('a session nudged and then answered still reports having been nudged, not "Not nudged"', () => {
  const now = Date.now();
  const lastAutoNudgeAtUnix = Math.floor(now / 1000) - 65;
  // nudgeCount is 0 (the cap gauge rearmed on the reply); the cumulative
  // history is what must carry the fact that a nudge happened.
  const summary = orchestratorNudgeSummary(
    fields({ nudgeCount: 0, autoNudgeCount: 2, lastAutoNudgeAtUnix }),
    now,
  );
  assert.match(summary, /^Nudged 2x, last .+ ago$/);
  assert.notEqual(summary, 'Not nudged');
});

test('a capped session names the attempt count and the recovery, not just "quiet"', () => {
  const summary = orchestratorNudgeSummary(
    fields({ nudgeCount: 6, nudgeCapped: true, autoNudgeCount: 6 }),
    Date.now(),
  );
  assert.match(summary, /^Stopped nudging after 6 attempts/);
  assert.match(summary, /reply or restart/);
});

test('an explicit whip is reported distinctly from an automatic nudge', () => {
  const now = Date.now();
  const lastWhipAtUnix = Math.floor(now / 1000) - 40;
  const summary = orchestratorNudgeSummary(fields({ whipCount: 1, lastWhipAtUnix }), now);
  assert.match(summary, /^Whipped 1x, last .+ ago$/);
});

test('automatic nudges and explicit whips both appear when both happened', () => {
  const now = Date.now();
  const summary = orchestratorNudgeSummary(
    fields({
      autoNudgeCount: 3,
      lastAutoNudgeAtUnix: Math.floor(now / 1000) - 120,
      whipCount: 1,
      lastWhipAtUnix: Math.floor(now / 1000) - 40,
    }),
    now,
  );
  assert.match(summary, /^Nudged 3x, last .+ ago; Whipped 1x, last .+ ago$/);
});

test('a session that resumed after hitting the cap still names that it was capped', () => {
  const now = Date.now();
  const summary = orchestratorNudgeSummary(
    fields({
      autoNudgeCount: 6,
      lastAutoNudgeAtUnix: Math.floor(now / 1000) - 30,
      lastCappedAtUnix: Math.floor(now / 1000) - 60,
    }),
    now,
  );
  assert.match(summary, /previously capped .+ ago/);
  assert.notEqual(summary, 'Not nudged');
});

test('an unreadable persisted history is reported as unavailable, never as "Not nudged"', () => {
  const summary = orchestratorNudgeSummary(fields({ nudgeHistoryUnreadable: true }), Date.now());
  assert.equal(summary, 'Nudge history unavailable');
  assert.notEqual(summary, 'Not nudged');
});

test('a session with real nudges still reports them even if this restore happened to hit an unreadable file', () => {
  const now = Date.now();
  const summary = orchestratorNudgeSummary(
    fields({
      autoNudgeCount: 2,
      lastAutoNudgeAtUnix: Math.floor(now / 1000) - 10,
      nudgeHistoryUnreadable: true,
    }),
    now,
  );
  assert.match(summary, /^Nudged 2x, last .+ ago$/);
});
