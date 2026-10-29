import assert from 'node:assert/strict';

import { test } from 'vitest';

import type { UICloudProviderStatus, UITenant } from '@/types';

import {
  deriveEnvironmentRow,
  environmentCardActivityLabel,
  environmentIndicator,
  sidebarCloudAliases,
} from './Sidebar.helpers';

const base = {
  name: 'team / dev',
  envState: '',
  isOpen: false,
  reachable: false,
  outage: false,
  busy: false,
  detail: '',
};

test('a never-opened, unreachable environment renders no indicator', () => {
  const indicator = environmentIndicator(base);
  assert.equal(indicator.visible, false);
  assert.equal(indicator.opened, false);
  // A never-opened env has no pod to be idle.
  assert.equal(indicator.activity, 'Not open');
});

test('an environment reachable from the CLI is visible without being opened here', () => {
  const indicator = environmentIndicator({ ...base, reachable: true });
  assert.equal(indicator.visible, true);
  assert.equal(indicator.dot, 'running');
  assert.equal(indicator.opened, false);
  assert.match(indicator.condition, /in use elsewhere — not opened here/);
  assert.equal(indicator.activity, 'In use elsewhere — not opened here');
});

test('an environment the desktop opened is a close control, not a status light', () => {
  const indicator = environmentIndicator({ ...base, isOpen: true, reachable: true });
  assert.equal(indicator.opened, true);
  assert.equal(indicator.dot, 'running');
  assert.equal(indicator.condition, 'team / dev is running');
  assert.equal(indicator.activity, 'Idle');
});

test('a busy environment says what is keeping it busy', () => {
  const indicator = environmentIndicator({
    ...base,
    reachable: true,
    busy: true,
    detail: 'holding: gradle-build',
  });
  assert.equal(indicator.dot, 'busy');
  assert.equal(indicator.condition, 'team / dev is busy — holding: gradle-build');
  assert.equal(indicator.activity, 'Busy — holding: gradle-build');
});

test('a busy environment with no detail still reads busy', () => {
  const indicator = environmentIndicator({ ...base, reachable: true, busy: true });
  assert.equal(indicator.dot, 'busy');
  assert.equal(indicator.condition, 'team / dev is busy');
});

test('a stopped environment reads stopped even if its last observation was busy', () => {
  // A late poll result must not override the sticky stopped condition.
  for (const envState of ['stopped', 'runtime-stopped']) {
    const indicator = environmentIndicator({ ...base, envState, isOpen: true, busy: true });
    assert.equal(indicator.dot, 'stopped', envState);
    assert.match(indicator.condition, /is stopped — /, envState);
    assert.match(indicator.activity, /^Stopped — /, envState);
  }
});

test('an unhealthy environment reads failed, never busy', () => {
  const indicator = environmentIndicator({
    ...base,
    envState: 'failed',
    isOpen: true,
    busy: true,
    detail: 'holding: agent-run',
  });
  assert.equal(indicator.dot, 'failed');
  assert.match(indicator.condition, /deploy failed — /);
  assert.equal(indicator.activity, 'Deploy failed — recover from Activities');
});

test('a stopped sticky condition does not outlive the session that produced it', () => {
  for (const envState of ['stopped', 'runtime-stopped']) {
    const indicator = environmentIndicator({ ...base, envState });
    assert.equal(indicator.visible, false, envState);
  }
});

test('a failed deploy stays visible after its session closes, unless the environment is reachable again', () => {
  const stillBroken = environmentIndicator({ ...base, envState: 'failed' });
  assert.equal(stillBroken.visible, true);
  assert.equal(stillBroken.dot, 'failed');
  assert.equal(stillBroken.opened, false);
  assert.match(stillBroken.condition, /deploy failed — /);
  assert.equal(stillBroken.activity, 'Deploy failed — recover from Activities');

  const recovered = environmentIndicator({ ...base, envState: 'failed', reachable: true });
  assert.equal(recovered.dot, 'running');
});

test('a bound-but-dead forward is reported as an outage, not as a quiet row', () => {
  // The port-forward still holds its local port, so the listener looks reachable.
  const indicator = environmentIndicator({ ...base, reachable: true, outage: true });
  assert.equal(indicator.visible, true);
  assert.equal(indicator.dot, 'failed');
  assert.equal(
    indicator.condition,
    'team / dev is unreachable — its connection is dead; deploy it to bring the runtime back',
  );
  assert.equal(
    indicator.activity,
    'Unreachable — its connection is dead; deploy it to bring the runtime back',
  );
});

test('a dropped forward is an outage, not the blank row of an environment nobody opened', () => {
  const indicator = environmentIndicator({ ...base, reachable: false, outage: true });
  assert.equal(indicator.visible, true);
  assert.equal(indicator.dot, 'failed');
  assert.equal(
    indicator.condition,
    'team / dev is unreachable — its connection is dead; deploy it to bring the runtime back',
  );
  assert.equal(
    indicator.activity,
    'Unreachable — its connection is dead; deploy it to bring the runtime back',
  );
});

test('a reachable environment is reported on its own terms, not a sticky condition', () => {
  const indicator = environmentIndicator({ ...base, envState: 'failed', reachable: true });
  assert.equal(indicator.visible, true);
  assert.equal(indicator.dot, 'running');
  assert.equal(indicator.activity, 'In use elsewhere — not opened here');
});

function rowArgs(overrides: {
  isOpening?: boolean;
  runningCommand?: string;
  aiBusy?: boolean;
  reconnecting?: boolean;
  envBusy?: boolean;
  envBusyDetail?: string;
  envObserved?: boolean;
}) {
  return {
    isOpening: false,
    runningCommand: '',
    aiBusy: false,
    reconnecting: false,
    envBusy: false,
    envBusyDetail: '',
    envObserved: false,
    ...overrides,
  };
}

function row(overrides: Parameters<typeof rowArgs>[0]) {
  const input = rowArgs(overrides);
  return deriveEnvironmentRow(
    'team',
    'dev',
    null,
    [],
    input.isOpening,
    input.runningCommand,
    input.aiBusy,
    input.reconnecting,
    input.envBusy,
    input.envBusyDetail,
    input.envObserved,
  );
}

test('an environment working for reasons this desktop did not start still spins', () => {
  const derived = row({ envBusy: true, envBusyDetail: 'erun release 1.0.176' });
  assert.equal(derived.busy, true);
  assert.equal(derived.busyLabel, 'team / dev is busy — erun release 1.0.176');
});

test('an environment reporting itself busy without a detail still spins', () => {
  const derived = row({ envBusy: true });
  assert.equal(derived.busy, true);
  assert.equal(derived.busyLabel, 'team / dev is busy');
});

test('a quiet environment nobody is driving does not spin', () => {
  const derived = row({});
  assert.equal(derived.busy, false);
  assert.equal(derived.busyLabel, '');
});

test('a command this desktop started keeps its own more specific label', () => {
  const derived = row({ runningCommand: 'deploy', envBusy: true, envBusyDetail: 'a lease' });
  assert.equal(derived.busy, true);
  assert.match(derived.busyLabel, /team \/ dev$/);
  assert.doesNotMatch(derived.busyLabel, /a lease/);
});

test('each desktop-local reason still spins its own row on its own', () => {
  for (const overrides of [
    { isOpening: true },
    { runningCommand: 'deploy' },
    { aiBusy: true },
    { reconnecting: true },
  ]) {
    assert.equal(row(overrides).busy, true, `expected busy for ${JSON.stringify(overrides)}`);
  }
});

test('an environment that reports itself idle clears a stale desktop latch', () => {
  const stuckCommand = row({ runningCommand: 'deploy', envObserved: true, envBusy: false });
  assert.equal(stuckCommand.busy, false);

  const stuckAI = row({ aiBusy: true, envObserved: true, envBusy: false });
  assert.equal(stuckAI.busy, false);
});

test('an environment that gave no verdict keeps its desktop latch', () => {
  assert.equal(row({ runningCommand: 'deploy', envObserved: false }).busy, true);
  assert.equal(row({ aiBusy: true, envObserved: false }).busy, true);
});

test('an environment that reports work spins even when the desktop started nothing', () => {
  assert.equal(row({ envBusy: true, envObserved: true }).busy, true);
});

test("an idle report does not swallow this desktop's in-flight operation", () => {
  assert.equal(row({ isOpening: true, envObserved: true, envBusy: false }).busy, true);
  assert.equal(row({ reconnecting: true, envObserved: true, envBusy: false }).busy, true);
});

test('a label describing the environment is marked as the environment own', () => {
  const derived = row({ envBusy: true, envBusyDetail: 'holding: gradle-build', envObserved: true });
  assert.equal(derived.busyLabel, 'team / dev is busy — holding: gradle-build');
  assert.equal(derived.busyFromEnvironment, true);
});

test('a label describing an operation this desktop is running is not', () => {
  const withCommand = row({ runningCommand: 'build', envBusy: true, envObserved: true });
  assert.equal(withCommand.busyLabel, 'Building team / dev');
  assert.equal(withCommand.busyFromEnvironment, false);

  assert.equal(row({ isOpening: true, envBusy: true }).busyFromEnvironment, false);
  assert.equal(row({ reconnecting: true, envBusy: true }).busyFromEnvironment, false);
});

function indicatorDot(envState: string, busy: boolean) {
  return environmentIndicator({
    name: 'team / dev',
    envState,
    isOpen: true,
    reachable: false,
    outage: false,
    busy,
    detail: '',
  }).dot;
}

test('environmentCardActivityLabel repeats the row label unless the indicator already will', () => {
  assert.equal(environmentCardActivityLabel(false, false, 'unused', 'running'), '');
  assert.equal(
    environmentCardActivityLabel(true, false, 'Building team / dev', 'running'),
    'Building team / dev',
  );
  assert.equal(environmentCardActivityLabel(true, true, 'team / dev is busy', 'busy'), '');
  assert.equal(
    environmentCardActivityLabel(true, true, 'team / dev is busy — a lease', 'stopped'),
    'team / dev is busy — a lease',
  );
  assert.equal(
    environmentCardActivityLabel(true, true, 'team / dev is busy — a lease', 'failed'),
    'team / dev is busy — a lease',
  );
});

// envBusy and the sticky envState are set by separate pollers, so a fresh busy
// can land while envState still reads stopped/failed.
test('the card never asserts a bare Stopped/Failed while the row spins from a fresh envBusy', () => {
  for (const envState of ['stopped', 'runtime-stopped', 'failed']) {
    const derived = row({
      envBusy: true,
      envBusyDetail: 'holding: gradle-build',
      envObserved: true,
    });
    const dot = indicatorDot(envState, true);
    assert.equal(derived.busy, true, envState);
    const cardLabel = environmentCardActivityLabel(
      derived.busy,
      derived.busyFromEnvironment,
      derived.busyLabel,
      dot,
    );
    assert.equal(cardLabel, derived.busyLabel, envState);
    assert.doesNotMatch(cardLabel, /^(Stopped|Deploy failed)/, envState);
  }
});

test('a lingering isOpening/reconnecting flag never disagrees with the card', () => {
  for (const overrides of [{ isOpening: true }, { reconnecting: true }]) {
    const derived = row({ ...overrides, envObserved: true });
    const dot = indicatorDot('stopped', false);
    assert.equal(derived.busy, true, JSON.stringify(overrides));
    const cardLabel = environmentCardActivityLabel(
      derived.busy,
      derived.busyFromEnvironment,
      derived.busyLabel,
      dot,
    );
    assert.equal(cardLabel, derived.busyLabel, JSON.stringify(overrides));
  }
});

function tenant(overrides: Partial<UITenant> = {}): UITenant {
  return { name: 'acme', environments: [], ...overrides };
}

function erunProvider(overrides: Partial<UICloudProviderStatus> = {}): UICloudProviderStatus {
  return { alias: 'erun+api.acme.test@erun', provider: 'erun', status: 'active', ...overrides };
}

test('an unattached erun alias is surfaced even with no tenant selected', () => {
  const aliases = sidebarCloudAliases({
    tenants: [tenant()],
    cloudProviders: [erunProvider()],
    selected: null,
    dashboardTenant: '',
  });
  assert.deepEqual(aliases, ['erun+api.acme.test@erun']);
});

test('an unattached erun alias is surfaced alongside the selected tenant own alias', () => {
  const aliases = sidebarCloudAliases({
    tenants: [tenant({ cloudProviderAliases: ['acme+aws@aws'] })],
    cloudProviders: [{ alias: 'acme+aws@aws', provider: 'aws', status: 'active' }, erunProvider()],
    selected: { tenant: 'acme', environment: 'dev' },
    dashboardTenant: '',
  });
  assert.deepEqual(aliases.sort(), ['acme+aws@aws', 'erun+api.acme.test@erun'].sort());
});

test('an erun alias a tenant already attaches is not duplicated as unattached', () => {
  const aliases = sidebarCloudAliases({
    tenants: [tenant({ cloudProviderAliases: ['erun+api.acme.test@erun'] })],
    cloudProviders: [erunProvider()],
    selected: { tenant: 'acme', environment: 'dev' },
    dashboardTenant: '',
  });
  assert.deepEqual(aliases, ['erun+api.acme.test@erun']);
});

test('no tenants and no cloud providers renders no sidebar alias rows', () => {
  const aliases = sidebarCloudAliases({
    tenants: [],
    cloudProviders: [],
    selected: null,
    dashboardTenant: '',
  });
  assert.deepEqual(aliases, []);
});
