import assert from 'node:assert/strict';
import { test } from 'node:test';

import {
  environmentTypeBuildsHere,
  environmentTypeBuildsHereLocally,
  environmentTypeIsHost,
  environmentTypeIsRemoteWorktree,
  environmentTypeIsRuntime,
} from './environmentType';

// The bug the issue names verbatim: environmentTypeIsRemoteWorktree used to be
// exclusion-shaped in spirit at the Go layer (RemoteWorktree() != local-agent),
// which would have made a host env's worktree report as remote. This module's
// version was already enumeration-shaped ('remote-agent' || 'runtime'), so it
// answers correctly for host without any change — this test pins that so it
// stays that way.
test('a host environment is not a remote worktree', () => {
  assert.equal(environmentTypeIsRemoteWorktree('host'), false);
});

test('a host environment builds here, like local-agent and remote-agent', () => {
  assert.equal(environmentTypeBuildsHere('host'), true);
  assert.equal(environmentTypeBuildsHere('local-agent'), true);
  assert.equal(environmentTypeBuildsHere('remote-agent'), true);
  assert.equal(environmentTypeBuildsHere('runtime'), false);
});

test('a host environment is not the local-agent build-and-deploy shape', () => {
  // This is the predicate that gates the desktop's "create & deploy new
  // version" action — a host env has no pod to deploy to at all, so it must
  // stay excluded even though it also builds locally.
  assert.equal(environmentTypeBuildsHereLocally('host'), false);
});

test('a host environment is not a runtime environment', () => {
  assert.equal(environmentTypeIsRuntime('host'), false);
});

test('environmentTypeIsHost recognizes only the host type', () => {
  assert.equal(environmentTypeIsHost('host'), true);
  assert.equal(environmentTypeIsHost('local-agent'), false);
  assert.equal(environmentTypeIsHost('remote-agent'), false);
  assert.equal(environmentTypeIsHost('runtime'), false);
  assert.equal(environmentTypeIsHost(undefined), false);
});
