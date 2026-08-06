import assert from 'node:assert/strict';
import { test } from 'node:test';

import { environmentIndicator } from './Sidebar.helpers';

// One derived row state from three inputs: the sticky condition the desktop set,
// what the environment reports about itself, and whether the desktop owns tabs.
// The cases below are the five an operator has to be able to tell apart —
// stopped, unhealthy, busy, reachable-but-not-opened, and opened-and-quiet —
// plus the never-opened row, which must stay blank.

const base = {
  name: 'team / dev',
  envState: '',
  isOpen: false,
  reachable: false,
  busy: false,
  detail: '',
};

test('a never-opened, unreachable environment renders no indicator', () => {
  const indicator = environmentIndicator(base);
  assert.equal(indicator.visible, false);
  assert.equal(indicator.opened, false);
  // The hover card still has a row to fill, and it must not claim the
  // environment is idle — a never-opened env has no pod to be idle.
  assert.equal(indicator.activity, 'Not open');
});

test('an environment reachable from the CLI is visible without being opened here', () => {
  // The half of the bug that made a CLI-driven env render blank: its forwards
  // are up and its edge answers, but the desktop owns no tabs for it.
  const indicator = environmentIndicator({ ...base, reachable: true });
  assert.equal(indicator.visible, true);
  assert.equal(indicator.dot, 'running');
  assert.equal(indicator.opened, false);
  assert.match(indicator.condition, /in use elsewhere — not opened here/);
  assert.equal(indicator.activity, 'In use elsewhere — not opened here');
});

test('an environment the desktop opened is a close control, not a status light', () => {
  const indicator = environmentIndicator({ ...base, isOpen: true, reachable: true });
  assert.equal(indicator.opened, true);
  assert.equal(indicator.dot, 'running');
  assert.equal(indicator.condition, 'team / dev is running');
  assert.equal(indicator.activity, 'Idle');
});

test('a busy environment says what is keeping it busy', () => {
  const indicator = environmentIndicator({
    ...base,
    reachable: true,
    busy: true,
    detail: 'holding: gradle-build',
  });
  assert.equal(indicator.dot, 'busy');
  // The indicator's label is read out of context and names the env; the hover
  // card's row sits under a heading that already does.
  assert.equal(indicator.condition, 'team / dev is busy — holding: gradle-build');
  assert.equal(indicator.activity, 'Busy — holding: gradle-build');
});

test('a busy environment with no detail still reads busy', () => {
  const indicator = environmentIndicator({ ...base, reachable: true, busy: true });
  assert.equal(indicator.dot, 'busy');
  assert.equal(indicator.condition, 'team / dev is busy');
});

test('a stopped environment reads stopped even if its last observation was busy', () => {
  // The observation is a poll result that can arrive after the stop; the sticky
  // condition has to win or a stopped row would animate as if it were working.
  for (const envState of ['stopped', 'runtime-stopped']) {
    const indicator = environmentIndicator({ ...base, envState, isOpen: true, busy: true });
    assert.equal(indicator.dot, 'stopped', envState);
    assert.match(indicator.condition, /is stopped — /, envState);
    assert.match(indicator.activity, /^Stopped — /, envState);
  }
});

test('an unhealthy environment reads failed, never busy', () => {
  const indicator = environmentIndicator({
    ...base,
    envState: 'failed',
    isOpen: true,
    busy: true,
    detail: 'holding: agent-run',
  });
  assert.equal(indicator.dot, 'failed');
  assert.match(indicator.condition, /deploy failed — /);
  assert.equal(indicator.activity, 'Deploy failed — recover from Activities');
});

test('a sticky condition does not outlive the session that produced it', () => {
  // Closing an environment can leave a last-known condition behind. With no
  // tabs there is no session for it to describe and no way to act on it, so the
  // row goes quiet rather than flying a stale stopped ring or failure triangle.
  for (const envState of ['stopped', 'runtime-stopped', 'failed']) {
    const indicator = environmentIndicator({ ...base, envState });
    assert.equal(indicator.visible, false, envState);
  }
});

test('a reachable environment is reported on its own terms, not a stale condition', () => {
  const indicator = environmentIndicator({ ...base, envState: 'failed', reachable: true });
  assert.equal(indicator.visible, true);
  assert.equal(indicator.dot, 'running');
  assert.equal(indicator.activity, 'In use elsewhere — not opened here');
});
