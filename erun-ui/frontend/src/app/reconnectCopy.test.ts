import assert from 'node:assert/strict';
import { test } from 'node:test';

import { mcpUnreachableKind, reachabilityCopy, stripMcpUnreachableMarker } from './reconnectCopy';

// #1230: the backend now names which of the two locally-observable
// reachability failures it hit, carried as one of two opaque marker prefixes.
// mcpUnreachableKind is the one place that decodes them; every other call site
// works from the returned kind, not the marker text.
test('mcpUnreachableKind recognizes the not-open marker', () => {
  const message = 'ERUN_MCP_UNREACHABLE_NOT_OPEN: mcp unreachable: no port-forward is listening';
  assert.equal(mcpUnreachableKind(message), 'not-open');
});

test('mcpUnreachableKind recognizes the stale-forward marker', () => {
  const message = 'ERUN_MCP_UNREACHABLE_STALE: mcp unreachable: not carrying traffic';
  assert.equal(mcpUnreachableKind(message), 'stale-forward');
});

// An ordinary diff-loading error (no reachability marker at all) must not be
// misread as one of the two reachability kinds -- that is the
// reconnectable=false path, which shows the raw message rather than the fixed
// reachability copy.
test('mcpUnreachableKind returns null for an unrelated error', () => {
  assert.equal(mcpUnreachableKind('git rev-parse failed: not a git repository'), null);
});

test('stripMcpUnreachableMarker removes only the matched marker, leaving the rest of the message', () => {
  const message = 'ERUN_MCP_UNREACHABLE_NOT_OPEN: mcp unreachable: no port-forward is listening';
  assert.equal(stripMcpUnreachableMarker(message), 'mcp unreachable: no port-forward is listening');
});

test('stripMcpUnreachableMarker leaves a message with no marker untouched', () => {
  assert.equal(stripMcpUnreachableMarker('some other error'), 'some other error');
});

// The two reachability kinds must render genuinely distinct treatments -- a
// stopped environment is informational ("Open"), a stale forward is a fault
// ("Reconnect…") -- so a caller that mixed them up would be caught here
// rather than only in a UI screenshot.
test('the two reachability kinds carry distinct action labels and titles', () => {
  assert.notEqual(reachabilityCopy['not-open'].action, reachabilityCopy['stale-forward'].action);
  assert.notEqual(
    reachabilityCopy['not-open'].errorTitle,
    reachabilityCopy['stale-forward'].errorTitle,
  );
  assert.equal(reachabilityCopy['not-open'].action, 'Open');
  assert.equal(reachabilityCopy['stale-forward'].action, 'Reconnect…');
});
