import assert from 'node:assert/strict';

import { test } from 'vitest';

import { DECILE_COUNT, decileFillCount, decileIsAlert } from './usageDecileStrip';

// The encoding contract the strip renders from. Two properties here are the
// reason the module exists rather than an inline expression in the component:
// a non-zero reading must never render as an empty strip, and a zero must. If
// both of those collapse into "no filled segments", the card is back to being
// unreadable at a glance, which is the whole point of the strip.

test('a zero reading fills nothing', () => {
  assert.equal(decileFillCount(0), 0);
});

test('a non-zero reading always fills at least one segment', () => {
  // The floor case the encoding must not round away: 0.1% CPU is busy-but-idle,
  // and a strip that renders it identically to 0% is the defect.
  for (const percent of [0.1, 0.2, 0.4, 2.6, 9.9]) {
    assert.equal(decileFillCount(percent), 1, `${String(percent)}% should fill one segment`);
  }
});

test('segments are ceil(pct/10) so a crossed decile fills', () => {
  assert.equal(decileFillCount(10), 1);
  assert.equal(decileFillCount(10.1), 2);
  assert.equal(decileFillCount(68.4), 7);
  assert.equal(decileFillCount(82), 9);
  assert.equal(decileFillCount(100), 10);
});

test('the fill saturates at the strip width and never goes negative', () => {
  assert.equal(decileFillCount(1000), DECILE_COUNT);
  assert.equal(decileFillCount(-5), 0);
});

test('a non-finite reading fills nothing rather than a full or partial strip', () => {
  assert.equal(decileFillCount(Number.NaN), 0);
});

test('the alert threshold is strictly above 80%', () => {
  assert.equal(decileIsAlert(80), false);
  assert.equal(decileIsAlert(80.1), true);
  assert.equal(decileIsAlert(82), true);
  assert.equal(decileIsAlert(0), false);
});

test('an unreadable reading is never flagged alert', () => {
  assert.equal(decileIsAlert(Number.NaN), false);
});
