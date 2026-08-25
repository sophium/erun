import assert from 'node:assert/strict';
import { test } from 'node:test';

import { usageSeverity } from './UsageMeter.helpers';

// The fail-soft contract, which is the whole point of #1336: a reading that was
// never taken must not be rendered as a measurement. The tempting naive
// implementation -- `(percent ?? 0) >= warnAt` -- passes every case below
// EXCEPT the unmeasured ones, where it silently asserts "0%, comfortably fine"
// about a field the reader could not read at all.
test('an unmeasured percent is never classified as a measurement', () => {
  assert.equal(usageSeverity(undefined, 85), 'normal');
  assert.equal(usageSeverity(Number.NaN, 85), 'normal');
  assert.equal(usageSeverity(Number.POSITIVE_INFINITY, 85), 'normal');
});

// CPU has no named warn threshold in erun-common -- bursting to quota is normal
// for a build -- so it passes warnAt undefined and must never colour itself.
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

// At the limit is a different statement from approaching it, so it escalates
// past 'warning' regardless of where the warn threshold sat.
test('at or above the limit is danger, whatever the warn threshold was', () => {
  assert.equal(usageSeverity(100, 85), 'danger');
  assert.equal(usageSeverity(140, 85), 'danger');
  assert.equal(usageSeverity(100, 99), 'danger');
});
