import assert from 'node:assert/strict';
import { test } from 'node:test';

import { versionNoticeMessage } from './versionSuggestions';

// A notice is the only thing the picker shows for a source that produced no
// versions, so the sign-in it names has to be the one that reaches that source's
// own registry — naming a different registry sends the operator to authenticate
// somewhere the image is not.

test('a private ghcr image names both ghcr sign-ins', () => {
  const message = versionNoticeMessage({ image: 'ghcr.io/sophium/pw-devops', kind: 'auth' });
  assert.match(message, /is private/);
  assert.match(message, /docker login ghcr\.io/);
  assert.match(message, /gh auth login/);
});

test('a private image on another registry names that registry, not ghcr', () => {
  const message = versionNoticeMessage({
    image: '020362606330.dkr.ecr.eu-west-2.amazonaws.com/pw-devops',
    kind: 'auth',
  });
  assert.match(message, /docker login 020362606330\.dkr\.ecr\.eu-west-2\.amazonaws\.com/);
  assert.doesNotMatch(message, /ghcr\.io/);
});

test('a registry named with a port is a host, not a Docker Hub namespace', () => {
  const message = versionNoticeMessage({ image: 'localhost:5000/pw-devops', kind: 'auth' });
  assert.match(message, /docker login localhost:5000/);
});

test('a Docker Hub image names Docker Hub', () => {
  const message = versionNoticeMessage({ image: 'sophium/pw-devops', kind: 'auth' });
  assert.match(message, /docker login docker\.io/);
});

test('an unreachable registry names the failure and offers no sign-in', () => {
  const message = versionNoticeMessage({
    image: 'ghcr.io/sophium/pw-devops',
    kind: 'unreachable',
  });
  assert.match(message, /couldn't reach the registry/);
  assert.doesNotMatch(message, /docker login/);
});
