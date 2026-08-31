import assert from 'node:assert/strict';
import { test } from 'node:test';

import { MIN_FIT_COLS, MIN_FIT_ROWS, safeFit } from './terminalFit';

function fakeFitAddon(proposed: { cols: number; rows: number } | undefined) {
  let fitCount = 0;
  return {
    fitCount: () => fitCount,
    proposeDimensions: () => proposed,
    fit: () => {
      fitCount += 1;
    },
  };
}

test('a zero-width container proposal is skipped, never applied', () => {
  const fitAddon = fakeFitAddon({ cols: 0, rows: 0 });
  const ran = safeFit(fitAddon as never);
  assert.equal(ran, false);
  assert.equal(fitAddon.fitCount(), 0);
});

test('a proposal below the column floor is skipped even with a healthy row count', () => {
  const fitAddon = fakeFitAddon({ cols: MIN_FIT_COLS - 1, rows: 40 });
  const ran = safeFit(fitAddon as never);
  assert.equal(ran, false);
  assert.equal(fitAddon.fitCount(), 0);
});

test('a proposal below the row floor is skipped even with a healthy column count', () => {
  const fitAddon = fakeFitAddon({ cols: 120, rows: MIN_FIT_ROWS - 1 });
  const ran = safeFit(fitAddon as never);
  assert.equal(ran, false);
  assert.equal(fitAddon.fitCount(), 0);
});

test('a proposal at exactly the floor is trusted and applied', () => {
  const fitAddon = fakeFitAddon({ cols: MIN_FIT_COLS, rows: MIN_FIT_ROWS });
  const ran = safeFit(fitAddon as never);
  assert.equal(ran, true);
  assert.equal(fitAddon.fitCount(), 1);
});

test('no proposal at all (element not yet in the DOM) is skipped', () => {
  const fitAddon = fakeFitAddon(undefined);
  const ran = safeFit(fitAddon as never);
  assert.equal(ran, false);
  assert.equal(fitAddon.fitCount(), 0);
});

test('a missing fit addon is a no-op rather than a throw', () => {
  assert.equal(safeFit(null), false);
  assert.equal(safeFit(undefined), false);
});

test('a skipped fit is retried and succeeds once the container is measurable again', () => {
  let proposed: { cols: number; rows: number } | undefined = { cols: 2, rows: 1 };
  const fitAddon = {
    fitCount: 0,
    proposeDimensions: () => proposed,
    fit() {
      this.fitCount += 1;
    },
  };

  // First attempt: the container is still mid-transition, near-zero.
  assert.equal(safeFit(fitAddon as never), false);
  assert.equal(fitAddon.fitCount, 0);

  // The transition settles at a real size; the next attempt (the same
  // resize path retrying on the next observed size change) fits normally.
  proposed = { cols: 132, rows: 42 };
  assert.equal(safeFit(fitAddon as never), true);
  assert.equal(fitAddon.fitCount, 1);
});
