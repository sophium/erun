import assert from 'node:assert/strict';
import { test } from 'node:test';

import {
  formatTranscriptSize,
  omittedSummary,
  resumingSummary,
  roleLabel,
} from './OrchestratorDialog.Conversations.helpers';

// The row labels are the operator's whole basis for choosing, so each role has
// to read as a different situation. "Stranded" in particular must not look like
// a healthy state: it is the row most likely to hold work nothing is resuming.
test('each role reads as its own situation, and stranded reads as a warning', () => {
  assert.equal(roleLabel('live').tone, 'success');
  assert.equal(roleLabel('attached').tone, 'success');
  assert.equal(roleLabel('stranded').tone, 'warning');
  assert.equal(roleLabel('derived').label, 'Default');
  assert.equal(roleLabel('unowned').label, 'Unclaimed');
  const labels = ['live', 'attached', 'stranded', 'derived', 'unowned'].map(
    (role) => roleLabel(role).label,
  );
  assert.equal(
    new Set(labels).size,
    labels.length,
    `roles must not share a label: ${labels.join(', ')}`,
  );
  // A role a later release adds still renders something rather than nothing.
  assert.ok(roleLabel('something-new').label !== '');
});

test('the summary says which conversation is being resumed and why', () => {
  assert.match(resumingSummary('attached'), /attached/);
  assert.match(resumingSummary('derived'), /name resolves to/);
  // A tracked conversation is never adopted on its own (erun#1696); any source
  // other than an explicit attachment reads as the derived anchor.
  assert.match(resumingSummary('tracked'), /name resolves to/);
});

// Size is one of the two facts that separate a conversation holding hours of
// work from one that stopped at its first turn, so it has to be readable at both
// ends of that range.
test('transcript size is rendered at the precision two of them are compared at', () => {
  assert.equal(formatTranscriptSize(0), 'not started');
  assert.equal(formatTranscriptSize(512), '512 B');
  assert.equal(formatTranscriptSize(64 * 1024), '64 KB');
  assert.equal(formatTranscriptSize(6 * 1024 * 1024), '6.0 MB');
});

// A short list must never read as "this machine has nothing on it": what was
// left out is stated, with the reason.
test('what the listing left out is named, or nothing is said at all', () => {
  assert.equal(omittedSummary(0, 0), '');
  assert.match(omittedSummary(2, 0), /belong to other orchestrators/);
  assert.match(omittedSummary(1, 0), /belongs to other orchestrators/);
  assert.match(omittedSummary(0, 5), /5 older ones not shown/);
  assert.match(omittedSummary(1, 1), /belongs.*older one not shown/);
});
