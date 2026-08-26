import assert from 'node:assert/strict';
import { test } from 'node:test';

import {
  computeMaxReviewWidth,
  computeMaxSidebarWidth,
  effectiveSidebarWidth,
  MAX_SIDEBAR_WIDTH,
  MIN_DASHBOARD_PANE_WIDTH,
  MIN_SIDEBAR_WIDTH,
  nextSidebarHidden,
  SIDEBAR_COLLAPSE_BREAKPOINT,
  SIDEBAR_COLLAPSE_HYSTERESIS,
  SIDEBAR_HARD_COLLAPSE_WIDTH,
} from './state';

// The narrow-viewport shell defect (a 640px window left <main> at 292px) was
// arithmetic: the sidebar's own MIN_SIDEBAR_WIDTH plus the divider plus a
// usable <main> exceeds a 640px viewport once the sidebar is anywhere near
// its old default. These lock the arithmetic that replaces it.

test('a comfortably wide viewport lets the sidebar reach its own maximum', () => {
  assert.equal(computeMaxSidebarWidth(1440), MAX_SIDEBAR_WIDTH);
});

test('at the collapse breakpoint the sidebar is clamped to exactly its minimum', () => {
  assert.equal(computeMaxSidebarWidth(SIDEBAR_COLLAPSE_BREAKPOINT), MIN_SIDEBAR_WIDTH);
});

test('the max never drops below the sidebar minimum, even far below the breakpoint', () => {
  assert.equal(computeMaxSidebarWidth(100), MIN_SIDEBAR_WIDTH);
});

test('effectiveSidebarWidth is zero while collapsed regardless of the stored width', () => {
  assert.equal(effectiveSidebarWidth(true, 520, 1440), 0);
});

test('effectiveSidebarWidth reclamps a too-wide stored width to what the viewport allows', () => {
  // A viewport just above the collapse breakpoint still shows the sidebar
  // (narrower viewports auto-collapse it instead, see nextSidebarHidden), so
  // this is the narrowest case where the reclamp math alone — not the
  // collapse decision — must keep <main> from starving. The default 338px
  // stored width has to squeeze down for the 10px divider and
  // MIN_DASHBOARD_PANE_WIDTH to both fit in 800px.
  const width = effectiveSidebarWidth(false, 338, 800);
  const mainWidth = 800 - width - 10;
  assert.ok(
    mainWidth >= MIN_DASHBOARD_PANE_WIDTH,
    `expected <main> to keep >=${String(MIN_DASHBOARD_PANE_WIDTH)}px, got ${String(mainWidth)}`,
  );
});

test('effectiveSidebarWidth leaves a wide stored width untouched on a wide viewport', () => {
  assert.equal(effectiveSidebarWidth(false, 400, 1440), 400);
});

test('computeMaxReviewWidth no longer floors at its own minimum when nothing fits', () => {
  // Previously floored at MIN_REVIEW_WIDTH (420) even when fittable was
  // negative, forcing the review panel wider than the viewport had room for.
  const maxWidth = computeMaxReviewWidth(500, 300);
  assert.ok(
    maxWidth < 420,
    `expected a max below the panel's own minimum, got ${String(maxWidth)}`,
  );
  assert.ok(maxWidth >= 0, `expected a non-negative max, got ${String(maxWidth)}`);
});

test('computeMaxReviewWidth caps at the panel maximum however much room there is', () => {
  assert.equal(computeMaxReviewWidth(3000, 338), 1400);
});

test('nextSidebarHidden collapses below the breakpoint with no user override', () => {
  assert.equal(nextSidebarHidden(false, null, SIDEBAR_COLLAPSE_BREAKPOINT - 1), true);
});

test('nextSidebarHidden stays open at and above the breakpoint with no user override', () => {
  assert.equal(nextSidebarHidden(false, null, SIDEBAR_COLLAPSE_BREAKPOINT), false);
});

test('nextSidebarHidden holds the current state inside the hysteresis band', () => {
  const midBand = SIDEBAR_COLLAPSE_BREAKPOINT + Math.floor(SIDEBAR_COLLAPSE_HYSTERESIS / 2);
  assert.equal(nextSidebarHidden(true, null, midBand), true);
  assert.equal(nextSidebarHidden(false, null, midBand), false);
});

test('nextSidebarHidden reopens once the widen clears the hysteresis margin', () => {
  assert.equal(
    nextSidebarHidden(true, null, SIDEBAR_COLLAPSE_BREAKPOINT + SIDEBAR_COLLAPSE_HYSTERESIS),
    false,
  );
});

test('an explicit "shown" override keeps the sidebar open below the breakpoint', () => {
  assert.equal(nextSidebarHidden(true, 'shown', SIDEBAR_COLLAPSE_BREAKPOINT - 1), false);
});

test('an explicit "hidden" override keeps the sidebar collapsed on a wide window', () => {
  assert.equal(nextSidebarHidden(false, 'hidden', 1440), true);
});

test('no override survives the hard floor: too narrow for any <main> forces collapse', () => {
  assert.equal(nextSidebarHidden(false, 'shown', SIDEBAR_HARD_COLLAPSE_WIDTH - 1), true);
});
