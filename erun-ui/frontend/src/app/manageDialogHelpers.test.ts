import assert from 'node:assert/strict';
import { test } from 'node:test';

import type { UIContainerRegistryEntry, UIEnvironmentConfig } from '@/types';

import { versionSourceSignature } from './manageDialogHelpers';

// The signature decides whether a save re-queries the version picker. It has to
// move for every edit that changes which registries the picker lists from, and
// stay put for every edit that does not — a false negative leaves the operator
// looking at a list that predates their own change.

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
  // An env that marks no registries of its own resolves them from the project
  // config at this path, so repointing it changes what the picker lists.
  const before = config([], '/repo');
  const after = config([], '/elsewhere');
  assert.notEqual(versionSourceSignature(before), versionSourceSignature(after));
});

test('changing only a role leaves the signature put', () => {
  // Discovery reads the hosts, not what each one is for, so a role edit cannot
  // change the offered versions and must not spend a registry round-trip.
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
