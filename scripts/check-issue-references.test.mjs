// Self-test for check-issue-references.mjs, the TypeScript half of the
// issue-reference gate (see that file's header for the design rationale).
// Run with `node --test scripts/check-issue-references.test.mjs`. Every
// example below uses a fake org/repo/number; none corresponds to a real
// GitHub issue.

import assert from 'node:assert/strict';
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { test } from 'node:test';

import { checkBaseline, findIssueReferenceHits, issueReferencePattern, matchIssueReference } from './check-issue-references.mjs';

test('issueReferencePattern catches the shape, not one phrasing', () => {
  const cases = [
    ['#8888', true],
    ['see #8888 for context', true],
    ['issue #8888', true],
    ['Issue #8888', true],
    ['issue#8888', true],
    ['acme/widgets#8888', true],
    ['https://github.com/acme/widgets/issues/8888', true],
    ['https://github.com/acme/widgets/pull/8888', true],
    ['# Overview', false],
    ['C# is a language', false],
    ['no hash sign here at all', false],
  ];
  for (const [value, want] of cases) {
    assert.equal(issueReferencePattern.test(value), want, `pattern.test(${JSON.stringify(value)})`);
  }
});

test('matchIssueReference strips the boundary character from the bare-hash shape', () => {
  assert.equal(matchIssueReference('see #8888 for context'), '#8888');
  assert.equal(matchIssueReference('issue #8888'), 'issue #8888');
  assert.equal(matchIssueReference('acme/widgets#8888'), 'acme/widgets#8888');
  assert.equal(matchIssueReference('no reference here'), null);
});

test('findIssueReferenceHits reads real comments and ignores string-literal content', () => {
  const dir = mkdtempSync(join(tmpdir(), 'issue-ref-test-'));
  try {
    writeFileSync(
      join(dir, 'production.ts'),
      [
        '// FIXTURE: deliberately-constructed violation for this test only.',
        "// see #8889's precedent for how this used to resolve.",
        'export function doWork(): void {',
        '  // this string is not a comment reference: "acme/widgets#8890"',
        '  const message = "acme/widgets#8890";',
        '  console.log(message);',
        '}',
        '',
      ].join('\n'),
    );
    const hits = findIssueReferenceHits([dir]);
    // production.ts has exactly one real comment matching the pattern; the
    // second comment line quotes the reference to explain why the string on
    // the next line is NOT a hit (string literals are out of scope), so it
    // legitimately matches too -- both are real comments.
    assert.equal(hits.length, 2);
    assert.ok(hits.every((h) => h.file.endsWith('production.ts')));
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('checkBaseline enforces zero tolerance for an unbaselined file', () => {
  const hits = [{ file: 'new-file.ts', value: '#8891' }];
  const { violations, staleEntries } = checkBaseline(hits, {});
  assert.equal(violations.length, 1);
  assert.equal(staleEntries.length, 0);
});

test('checkBaseline allows exactly the baselined count and rejects one more', () => {
  const hits = [
    { file: 'existing.ts', value: '#8892' },
    { file: 'existing.ts', value: '#8893' },
    { file: 'existing.ts', value: '#8894' },
  ];
  const { violations } = checkBaseline(hits, { 'existing.ts': 2 });
  assert.equal(violations.length, 1);
  assert.equal(violations[0].value, '#8894');
});

test('checkBaseline fails a stale baseline entry that claims more than actually remains', () => {
  const hits = [{ file: 'cleaned-up.ts', value: '#8895' }];
  const { violations, staleEntries } = checkBaseline(hits, { 'cleaned-up.ts': 3 });
  assert.equal(violations.length, 0);
  assert.equal(staleEntries.length, 1);
  assert.deepEqual(staleEntries[0], { file: 'cleaned-up.ts', allowed: 3, actual: 1 });
});
