import assert from 'node:assert/strict';
import { test } from 'node:test';

import { activatableUrl, TERMINAL_URL_REGEX } from './terminalUrlLinks';

test('allows http and https', () => {
  assert.ok(activatableUrl('http://example.com'));
  assert.ok(activatableUrl('https://example.com/path'));
});

test('allows mailto', () => {
  assert.ok(activatableUrl('mailto:ops@example.com'));
});

test('rejects javascript, data, and file schemes', () => {
  assert.equal(activatableUrl('javascript:alert(1)'), null);
  assert.equal(activatableUrl('data:text/html,<script>alert(1)</script>'), null);
  assert.equal(activatableUrl('file:///etc/hosts'), null);
});

test('rejects an unparsable value', () => {
  assert.equal(activatableUrl('not a url'), null);
});

test('the shared url regex matches http(s) and mailto', () => {
  const rex = new RegExp(TERMINAL_URL_REGEX.source, 'g');
  const line = 'see https://example.com/x and mailto:a@b.com now';
  assert.deepEqual(
    [...line.matchAll(rex)].map((m) => m[0]),
    ['https://example.com/x', 'mailto:a@b.com'],
  );
});

test('the shared url regex does not match javascript/data/file', () => {
  const rex = new RegExp(TERMINAL_URL_REGEX.source, 'g');
  assert.equal(
    [...'javascript:alert(1) data:text/plain,x file:///etc/hosts'.matchAll(rex)].length,
    0,
  );
});
