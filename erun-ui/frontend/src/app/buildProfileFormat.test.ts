import assert from 'node:assert/strict';
import { test } from 'node:test';

import {
  buildProfileViewButtonLabel,
  cgroupIsUsable,
  cpuLabel,
  formatBytes,
  formatDurationSeconds,
  ioLabel,
  throttleRatioLabel,
} from './buildProfileFormat';

test('formatDurationSeconds renders sub-second durations in milliseconds', () => {
  assert.equal(formatDurationSeconds(0.25), '250ms');
});

test('formatDurationSeconds renders sub-minute durations with one decimal of seconds', () => {
  assert.equal(formatDurationSeconds(12.34), '12.3s');
});

test('formatDurationSeconds renders minute+ durations as minutes and seconds', () => {
  assert.equal(formatDurationSeconds(90), '1m 30s');
});

test('formatDurationSeconds renders a dash for a negative or non-finite value', () => {
  assert.equal(formatDurationSeconds(-1), '—');
  assert.equal(formatDurationSeconds(Number.NaN), '—');
});

test('formatBytes renders sub-MiB counts in KiB', () => {
  assert.equal(formatBytes(2048), '2 KiB');
});

test('formatBytes renders sub-GiB counts in MiB', () => {
  assert.equal(formatBytes(5 * 1024 * 1024), '5.0 MiB');
});

test('formatBytes renders GiB-scale counts in GiB', () => {
  assert.equal(formatBytes(2 * 1024 * 1024 * 1024), '2.00 GiB');
});

test('formatBytes renders a dash for an undefined or negative value', () => {
  assert.equal(formatBytes(undefined), '—');
  assert.equal(formatBytes(-5), '—');
});

test('cgroupIsUsable is false for an undefined cgroup', () => {
  assert.equal(cgroupIsUsable(undefined), false);
});

test('cgroupIsUsable is false when the cgroup exists but its read failed', () => {
  assert.equal(
    cgroupIsUsable({ available: false, unavailable: 'counters were not readable' }),
    false,
  );
});

test('cgroupIsUsable is true when the cgroup was actually read', () => {
  assert.equal(cgroupIsUsable({ available: true, cpuSeconds: 1 }), true);
});

test('cpuLabel renders seconds with a quota percentage when available', () => {
  assert.equal(
    cpuLabel({ available: true, cpuSeconds: 12, cpuPercentOfQuota: 42.4 }),
    '12.0s (42% of quota)',
  );
});

test('cpuLabel renders bare seconds when no quota percentage is available', () => {
  assert.equal(cpuLabel({ available: true, cpuSeconds: 12 }), '12.0s');
});

test('throttleRatioLabel renders the throttled/total periods ratio -- the starved-vs-busy signal, not a bare percentage', () => {
  assert.equal(
    throttleRatioLabel({ available: true, totalPeriods: 10, throttledPeriods: 3 }),
    'throttled 3/10 periods',
  );
});

test('throttleRatioLabel is undefined when there is no period data to ratio', () => {
  assert.equal(throttleRatioLabel({ available: true }), undefined);
});

test('ioLabel renders read and written bytes together', () => {
  assert.equal(
    ioLabel({ available: true, ioReadBytes: 1024, ioWriteBytes: 2048 }),
    '1 KiB read / 2 KiB written',
  );
});

test('ioLabel is undefined when there was no I/O to report', () => {
  assert.equal(ioLabel({ available: true }), undefined);
});

test('buildProfileViewButtonLabel names the build id so each row announces which build it opens', () => {
  assert.equal(buildProfileViewButtonLabel('build-123'), 'View build profile for build-123');
});
