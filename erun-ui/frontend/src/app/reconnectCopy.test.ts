import assert from 'node:assert/strict';

import { test } from 'vitest';

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

// The stale-forward confirmation is reached *from* the unreachable state, so
// it is shown exactly when the environment may be unreachable for a reason the
// action cannot address -- and it is the moment an operator is most likely to
// press it repeatedly. Its body used to promise that a runtime which is not
// currently running "will be redeployed". That promise is not merely
// unchecked: the action behind the button is `erun open --reconnect`, which
// verifies the runtime is already deployed, refuses outright when it is
// stopped, and never deploys anything -- so an operator pressing it again
// because the copy said a redeploy was coming had nothing to wait for.
test('the stale-forward dialog does not promise a redeployment the reconnect cannot perform', () => {
  const body = reachabilityCopy['stale-forward'].dialogBody;

  assert.ok(
    !body.includes('it will be redeployed'),
    `the dialog still promises a redeployment the reconnect cannot perform: ${body}`,
  );
  // And it states the limit rather than leaving the operator to infer it, so
  // a repeated press reads as an action that cannot help rather than one that
  // has not worked yet.
  assert.match(body, /does not start a stopped environment or redeploy one/);
  // It still names the command it runs: the dialog describes an action, not
  // an outcome.
  assert.match(body, /`erun open`/);
});
