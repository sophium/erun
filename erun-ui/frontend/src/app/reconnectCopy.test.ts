import assert from 'node:assert/strict';

import { test } from 'vitest';

import { mcpUnreachableKind, reachabilityCopy, stripMcpUnreachableMarker } from './reconnectCopy';

test('mcpUnreachableKind recognizes the not-open marker', () => {
  const message = 'ERUN_MCP_UNREACHABLE_NOT_OPEN: mcp unreachable: no port-forward is listening';
  assert.equal(mcpUnreachableKind(message), 'not-open');
});

test('mcpUnreachableKind recognizes the stale-forward marker', () => {
  const message = 'ERUN_MCP_UNREACHABLE_STALE: mcp unreachable: not carrying traffic';
  assert.equal(mcpUnreachableKind(message), 'stale-forward');
});

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

test('the two reachability kinds carry distinct action labels and titles', () => {
  assert.notEqual(reachabilityCopy['not-open'].action, reachabilityCopy['stale-forward'].action);
  assert.notEqual(
    reachabilityCopy['not-open'].errorTitle,
    reachabilityCopy['stale-forward'].errorTitle,
  );
  assert.equal(reachabilityCopy['not-open'].action, 'Open');
  assert.equal(reachabilityCopy['stale-forward'].action, 'Reconnect…');
});

// `erun open --reconnect` never deploys or starts a stopped runtime.
test('the stale-forward dialog does not promise a redeployment the reconnect cannot perform', () => {
  const body = reachabilityCopy['stale-forward'].dialogBody;

  assert.ok(
    !body.includes('it will be redeployed'),
    `the dialog still promises a redeployment the reconnect cannot perform: ${body}`,
  );
  assert.match(body, /does not start a stopped environment or redeploy one/);
  assert.match(body, /`erun open`/);
});
