import assert from 'node:assert/strict';
import { test } from 'node:test';

import { findAbsolutePathMatches } from './terminalPathLinks';

test('matches a bare absolute path', () => {
  const matches = findAbsolutePathMatches('/etc/hosts');
  assert.equal(matches.length, 1);
  assert.equal(matches[0]?.text, '/etc/hosts');
});

test('matches the reported case: a path in agent prose', () => {
  const line = 'Done: "/Users/me/Downloads/chip-migration-audit.xlsx"';
  const matches = findAbsolutePathMatches(line);
  assert.equal(matches.length, 1);
  assert.equal(matches[0]?.text, '/Users/me/Downloads/chip-migration-audit.xlsx');
});

test('strips trailing sentence punctuation', () => {
  const matches = findAbsolutePathMatches('See /etc/hosts.');
  assert.equal(matches.length, 1);
  assert.equal(matches[0]?.text, '/etc/hosts');
});

test('matches every entry in a colon-separated PATH listing', () => {
  const matches = findAbsolutePathMatches('PATH=/usr/local/bin:/usr/bin:/bin');
  assert.deepEqual(
    matches.map((m) => m.text),
    ['/usr/local/bin', '/usr/bin', '/bin'],
  );
});

test('does not match a fraction or a date', () => {
  assert.equal(findAbsolutePathMatches('serves 3/4 of the room').length, 0);
  assert.equal(findAbsolutePathMatches('due 12/25').length, 0);
  assert.equal(findAbsolutePathMatches('and/or').length, 0);
});

test('does not match the path portion of a URL', () => {
  const matches = findAbsolutePathMatches('open https://example.com/foo/bar for details');
  assert.equal(matches.length, 0);
});

test('matches an absolute path alongside an unrelated URL on the same line', () => {
  const matches = findAbsolutePathMatches('see https://example.com/docs and /etc/hosts');
  assert.deepEqual(
    matches.map((m) => m.text),
    ['/etc/hosts'],
  );
});

test('matches an absolute Windows path', () => {
  const matches = findAbsolutePathMatches(String.raw`open C:\Users\me\report.xlsx now`);
  assert.equal(matches.length, 1);
  assert.equal(matches[0]?.text, String.raw`C:\Users\me\report.xlsx`);
});

test('does not match a bare root or an empty token', () => {
  assert.equal(findAbsolutePathMatches('cd / ').length, 0);
});

test('a pod-side path with no host equivalent is still a syntactic match', () => {
  // Matching is origin-agnostic; TerminalPathLinkProvider decides
  // resolvability. /etc/hosts is the canonical case #1354 calls out.
  const matches = findAbsolutePathMatches('/etc/hosts');
  assert.equal(matches.length, 1);
});

test('reports offsets that round-trip to the original text', () => {
  const line = 'error writing /tmp/out.log: permission denied';
  const [match] = findAbsolutePathMatches(line);
  assert.ok(match);
  assert.equal(line.slice(match.start, match.end), match.text);
});
