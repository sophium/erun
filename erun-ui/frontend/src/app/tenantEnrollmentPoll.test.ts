import assert from 'node:assert/strict';
import { test } from 'node:test';

import {
  buildInviteAcceptLink,
  enrollmentApproved,
  nextEnrollmentPollingInterval,
  TENANT_ENROLLMENT_POLL_INTERVAL_MS,
} from './tenantEnrollmentPoll';

// These pure functions carry the sidebar tenant-enrollment icon's poll gate
// and its transition-detection logic. Playwright cannot drive the
// pending/declined/enrolled transitions without a real
// backend or a stubbed Wails bridge response (no precedent for the latter in
// this suite's headless harness -- see the spec's own comment), so this is
// the coverage for that state-classification logic itself.

test('local-only is never polled', () => {
  assert.equal(nextEnrollmentPollingInterval('local-only'), 0);
});

test('pending and declined are polled at the moderate fixed interval', () => {
  assert.equal(nextEnrollmentPollingInterval('pending'), TENANT_ENROLLMENT_POLL_INTERVAL_MS);
  assert.equal(nextEnrollmentPollingInterval('declined'), TENANT_ENROLLMENT_POLL_INTERVAL_MS);
});

test('enrolled stops polling permanently', () => {
  assert.equal(nextEnrollmentPollingInterval('enrolled'), 0);
});

test('no status yet observed (undefined) is not polled', () => {
  assert.equal(nextEnrollmentPollingInterval(undefined), 0);
});

test('a genuine round-trip failure (unknown) keeps polling rather than going silent', () => {
  assert.equal(nextEnrollmentPollingInterval('unknown'), TENANT_ENROLLMENT_POLL_INTERVAL_MS);
});

test('unknown -> enrolled is an approval worth notifying about (a failure that resolved)', () => {
  assert.equal(enrollmentApproved('unknown', 'enrolled'), true);
});

test('pending -> enrolled is an approval worth notifying about', () => {
  assert.equal(enrollmentApproved('pending', 'enrolled'), true);
});

test('declined -> enrolled is also an approval (a retried, later-approved request)', () => {
  assert.equal(enrollmentApproved('declined', 'enrolled'), true);
});

test('local-only -> enrolled is not a transition this poll ever produces, and is not treated as one', () => {
  assert.equal(enrollmentApproved('local-only', 'enrolled'), false);
});

test('the very first observation (no previous state) never notifies', () => {
  assert.equal(enrollmentApproved(undefined, 'enrolled'), false);
});

test('pending -> pending is not a transition', () => {
  assert.equal(enrollmentApproved('pending', 'pending'), false);
});

test('buildInviteAcceptLink derives the console origin from the hosted api origin', () => {
  const link = buildInviteAcceptLink('tok-abc');
  assert.ok(link);
  assert.match(link, /^https:\/\/console\./);
  assert.match(link, /\/accept-invite\?token=tok-abc$/);
});
