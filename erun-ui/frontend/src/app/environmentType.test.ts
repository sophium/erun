import assert from 'node:assert/strict';

import { test } from 'vitest';

import {
  environmentTypeBuildsHere,
  environmentTypeBuildsHereLocally,
  environmentTypeIsHost,
  environmentTypeIsRemoteWorktree,
  environmentTypeIsRuntime,
} from './environmentType';

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
  // A host env has no pod to deploy to, though it builds locally.
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
