import assert from 'node:assert/strict';

import { test } from 'vitest';

import type { OrchestratorEnvRole } from '@/app/slices/orchestratorsSlice';

import {
  candidateDirectoryLabel,
  type EnvCandidate,
  orchestratorEnvRoleOptions,
} from './OrchestratorDialog.Environments.helpers';

// Role options must match eruncommon.OrchestratorEnvRoleAllowed: a host env takes no runtime role.

function candidate(overrides: Partial<EnvCandidate> = {}): EnvCandidate {
  return {
    tenant: 'pw',
    environment: 'alpha',
    environmentType: 'local-agent',
    eligible: true,
    defaultDirectory: '/repo',
    mirrored: false,
    ineligibleReason: '',
    ...overrides,
  };
}

function optionValues(candidateUnderTest: EnvCandidate, role: OrchestratorEnvRole = ''): string[] {
  return orchestratorEnvRoleOptions(candidateUnderTest, role).map((option) => option.value);
}

test('a host environment is offered no runtime role', () => {
  const values = optionValues(candidate({ environmentType: 'host' }));

  assert.equal(values.includes('runtime'), false);
  assert.deepEqual(values, ['none', 'code', 'build']);
});

test('every other linkable type is still offered the runtime role', () => {
  for (const environmentType of ['local-agent', 'remote-agent']) {
    assert.equal(
      optionValues(candidate({ environmentType })).includes('runtime'),
      true,
      `${environmentType} must still be offerable the runtime role`,
    );
  }
});

test('the sentinel values and their operator-facing words stay paired', () => {
  assert.deepEqual(orchestratorEnvRoleOptions(candidate({ environmentType: 'host' }), ''), [
    { value: 'none', label: 'Not declared' },
    { value: 'code', label: 'Code' },
    { value: 'build', label: 'Build' },
  ]);
  assert.deepEqual(orchestratorEnvRoleOptions(candidate({ environmentType: 'local-agent' }), ''), [
    { value: 'none', label: 'Not declared' },
    { value: 'code', label: 'Code' },
    { value: 'build', label: 'Build' },
    { value: 'runtime', label: 'Runtime' },
  ]);
});

test('a role the config already holds is offered even when the gate would refuse it', () => {
  // A hand-edited config can still hold runtime for a host env; it must stay visible.
  const host = candidate({ environmentType: 'host' });
  const options = orchestratorEnvRoleOptions(host, 'runtime');

  assert.deepEqual(optionValues(host, 'runtime'), ['none', 'code', 'build', 'runtime']);
  assert.equal(options.filter((option) => option.value === 'runtime').length, 1);
  assert.deepEqual(options[options.length - 1], { value: 'runtime', label: 'Runtime' });
});

test('an undeclared role is never appended as a second empty option', () => {
  // '' is undeclared and must not widen the list.
  assert.deepEqual(optionValues(candidate({ environmentType: 'host' }), ''), [
    'none',
    'code',
    'build',
  ]);
});

test('the directory row names a host environment a directory, not a worktree', () => {
  assert.equal(
    candidateDirectoryLabel(candidate({ environmentType: 'host' }), false),
    'directory on this machine',
  );
  assert.equal(
    candidateDirectoryLabel(candidate({ environmentType: 'local-agent' }), false),
    'worktree on this machine',
  );
  // Mirrored comes from the payload, not the type.
  assert.equal(
    candidateDirectoryLabel(candidate({ environmentType: 'remote-agent', mirrored: true }), false),
    'synced mirror',
  );
});

test('an operated-directly link says so ahead of every other directory label', () => {
  for (const environmentType of ['host', 'local-agent', 'remote-agent']) {
    assert.equal(
      candidateDirectoryLabel(candidate({ environmentType }), true),
      'operated directly — no review directory',
    );
  }
});
