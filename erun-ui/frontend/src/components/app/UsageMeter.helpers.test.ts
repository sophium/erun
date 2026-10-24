import assert from 'node:assert/strict';

import { test } from 'vitest';

import { usageSeverity } from './UsageMeter.helpers';

// `(percent ?? 0) >= warnAt` would pass every case here except the unmeasured ones.
test('an unmeasured percent is never classified as a measurement', () => {
  assert.equal(usageSeverity(undefined, 85), 'normal');
  assert.equal(usageSeverity(Number.NaN, 85), 'normal');
  assert.equal(usageSeverity(Number.POSITIVE_INFINITY, 85), 'normal');
});

// CPU has no warn threshold: bursting to quota is normal for a build.
test('a reading with no threshold to cross stays normal at any value', () => {
  assert.equal(usageSeverity(0, undefined), 'normal');
  assert.equal(usageSeverity(99.9, undefined), 'normal');
  assert.equal(usageSeverity(400, undefined), 'normal');
});

test('below the threshold is normal, at or above it is a warning', () => {
  assert.equal(usageSeverity(84.9, 85), 'normal');
  assert.equal(usageSeverity(85, 85), 'warning');
  assert.equal(usageSeverity(99.9, 85), 'warning');
});

test('at or above the limit is danger, whatever the warn threshold was', () => {
  assert.equal(usageSeverity(100, 85), 'danger');
  assert.equal(usageSeverity(140, 85), 'danger');
  assert.equal(usageSeverity(100, 99), 'danger');
});
