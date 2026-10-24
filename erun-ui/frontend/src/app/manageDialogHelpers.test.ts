import assert from 'node:assert/strict';

import { test } from 'vitest';

import type { UIContainerRegistryEntry, UIEnvironmentConfig } from '@/types';

import { versionSourceSignature } from './manageDialogHelpers';

function config(
  containerRegistries: UIContainerRegistryEntry[],
  localRepoPath = '/repo',
): UIEnvironmentConfig {
  return { containerRegistries, localRepoPath } as UIEnvironmentConfig;
}

test('adding a registry moves the signature', () => {
  const before = config([{ registry: 'ghcr.io/sophium', roles: ['build', 'deploy'] }]);
  const after = config([
    { registry: 'ghcr.io/sophium', roles: ['build', 'deploy'] },
    { registry: '020362606330.dkr.ecr.eu-west-2.amazonaws.com', roles: ['build', 'deploy'] },
  ]);
  assert.notEqual(versionSourceSignature(before), versionSourceSignature(after));
});

test('retargeting the local repo path moves the signature', () => {
  const before = config([], '/repo');
  const after = config([], '/elsewhere');
  assert.notEqual(versionSourceSignature(before), versionSourceSignature(after));
});

test('changing only a role leaves the signature put', () => {
  const before = config([{ registry: 'ghcr.io/sophium', roles: ['build', 'deploy'] }]);
  const after = config([{ registry: 'ghcr.io/sophium', roles: ['from', 'deploy'] }]);
  assert.equal(versionSourceSignature(before), versionSourceSignature(after));
});

test('an in-cluster registry names no host, so it leaves the signature put', () => {
  const before = config([
    {
      registry: '',
      cluster: {
        service: 'erun-registry',
        namespace: 'kube-system',
        port: 5000,
        insecure: true,
        label: 'erun-registry.kube-system:5000',
      },
      roles: ['build', 'deploy'],
    },
  ]);
  const after = config([]);
  assert.equal(versionSourceSignature(before), versionSourceSignature(after));
});
