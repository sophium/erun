import assert from 'node:assert/strict';

import { test } from 'vitest';

import type { OrchestratorEnvRole } from '@/app/slices/orchestratorsSlice';

import {
  candidateDirectoryLabel,
  type EnvCandidate,
  orchestratorEnvRoleOptions,
} from './OrchestratorDialog.Environments.helpers';

// The role picker's option list is the frontend half of the same gate
// eruncommon.OrchestratorEnvRoleAllowed applies on the Go side, and the two
// must agree: a host environment is now linkable, and a host env has
// no pod, so the runtime role -- "operated directly: deploy, pin, observe" --
// is the one pairing the Go gate refuses for it. Offering it here would let an
// operator pick a role whose every verb (deploy, pin, observe, open, upgrade)
// refuses a host environment outright, and the failure would surface only
// later, in the orchestrator session, as a tool that never works.

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
  // The roles that DO work must still be there: a host env is a real directory
  // on this machine, so code and build both apply, and a picker that offered
  // nothing would be indistinguishable from refusing host links outright.
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
  // Radix's Select.Item rejects an empty-string value, so undeclared is the
  // 'none' sentinel here and translated back at the boundary. A label lookup
  // that missed a value would render a blank row rather than fail loudly.
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
  // A config.yaml edited by hand -- or written before the gate refused this
  // pairing -- can still hold runtime against a host env. The picker must show
  // what is actually stored: no item matching the value renders a blank Radix
  // trigger, and the operator would have no way to select it back or away.
  const host = candidate({ environmentType: 'host' });
  const options = orchestratorEnvRoleOptions(host, 'runtime');

  assert.deepEqual(optionValues(host, 'runtime'), ['none', 'code', 'build', 'runtime']);
  // Appended once, at the end, so the three roles that work keep their places
  // and the stored value is what the trigger reads back.
  assert.equal(options.filter((option) => option.value === 'runtime').length, 1);
  assert.deepEqual(options[options.length - 1], { value: 'runtime', label: 'Runtime' });
});

test('an undeclared role is never appended as a second empty option', () => {
  // '' is the stored form of undeclared and 'none' is how the control says it,
  // so undeclared must not widen the list -- only a real off-list role may.
  assert.deepEqual(optionValues(candidate({ environmentType: 'host' }), ''), [
    'none',
    'code',
    'build',
  ]);
});

test('the directory row names a host environment a directory, not a worktree', () => {
  // A worktree in this dialog is a pod's, hostPath-mounted into it; a host env
  // has no pod at all, so the same word would describe two different
  // relationships and an operator could not tell which one a row meant.
  assert.equal(
    candidateDirectoryLabel(candidate({ environmentType: 'host' }), false),
    'directory on this machine',
  );
  assert.equal(
    candidateDirectoryLabel(candidate({ environmentType: 'local-agent' }), false),
    'worktree on this machine',
  );
  // Mirrored is the payload's own flag, not a second reading of the type:
  // ListOrchestratorEnvCandidates sets it from orchestratorReviewDirectory,
  // which reports true for exactly the remote-agent branch. The type is only
  // consulted for the non-mirrored case, where it separates a host env's own
  // directory from a local-agent env's pod-mounted worktree.
  assert.equal(
    candidateDirectoryLabel(candidate({ environmentType: 'remote-agent', mirrored: true }), false),
    'synced mirror',
  );
});

test('an operated-directly link says so ahead of every other directory label', () => {
  // The runtime role is the one link with no review directory at all, so its
  // label must win over the type-shaped ones -- naming where code lives
  // implies a directory the link does not carry.
  for (const environmentType of ['host', 'local-agent', 'remote-agent']) {
    assert.equal(
      candidateDirectoryLabel(candidate({ environmentType }), true),
      'operated directly — no review directory',
    );
  }
});
