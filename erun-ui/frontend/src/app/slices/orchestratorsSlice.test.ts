import assert from 'node:assert/strict';
import { test } from 'node:test';

import reducer, {
  type OrchestratorInfo,
  type OrchestratorsState,
  setEnvActivityForOrchestratorEnvs,
  setOrchestrators,
} from './orchestratorsSlice';

function orchestrator(overrides: Partial<OrchestratorInfo> = {}): OrchestratorInfo {
  return {
    id: 'orch',
    name: 'orch',
    environments: [],
    tenants: [],
    directories: [],
    sessionId: 0,
    status: 'stopped',
    busy: false,
    transient: false,
    shellRunning: false,
    shellCommand: '',
    shellStartedAtUnix: 0,
    nudgeCount: 0,
    nudgeCapped: false,
    restartRequired: false,
    roleChanged: false,
    ...overrides,
  };
}

function stateWith(items: OrchestratorInfo[]): OrchestratorsState {
  return reducer(undefined, setOrchestrators(items));
}

test('a live env-activity patch replaces a stale snapshot on the matching ref', () => {
  const state = stateWith([
    orchestrator({
      id: 'orch-a',
      environments: [
        {
          tenant: 'frs',
          environment: 'local',
          directory: '/tmp/a',
          role: '',
          activity: { reachable: false, observed: false, outage: true, busy: false },
        },
      ],
    }),
  ]);

  const next = reducer(
    state,
    setEnvActivityForOrchestratorEnvs({
      tenant: 'frs',
      environment: 'local',
      activity: { reachable: true, observed: true, outage: false, busy: false },
    }),
  );

  assert.deepEqual(next.items[0]?.environments[0]?.activity, {
    reachable: true,
    observed: true,
    outage: false,
    busy: false,
  });
});

test('a patch for a different environment leaves an unrelated ref untouched', () => {
  const staleActivity = { reachable: false, observed: false, outage: true, busy: false };
  const state = stateWith([
    orchestrator({
      id: 'orch-a',
      environments: [
        {
          tenant: 'frs',
          environment: 'local',
          directory: '/tmp/a',
          role: '',
          activity: staleActivity,
        },
      ],
    }),
  ]);

  const next = reducer(
    state,
    setEnvActivityForOrchestratorEnvs({
      tenant: 'frs',
      environment: 'other',
      activity: { reachable: true, observed: true, outage: false, busy: false },
    }),
  );

  assert.deepEqual(next.items[0]?.environments[0]?.activity, staleActivity);
});

test('one environment linked from two orchestrators updates on both refs', () => {
  const state = stateWith([
    orchestrator({
      id: 'orch-a',
      environments: [
        {
          tenant: 'frs',
          environment: 'local',
          directory: '/tmp/a',
          role: '',
          activity: { reachable: false, observed: false, outage: true, busy: false },
        },
      ],
    }),
    orchestrator({
      id: 'orch-b',
      environments: [
        {
          tenant: 'frs',
          environment: 'local',
          directory: '/tmp/b',
          role: '',
          activity: { reachable: false, observed: false, outage: true, busy: false },
        },
      ],
    }),
  ]);

  const next = reducer(
    state,
    setEnvActivityForOrchestratorEnvs({
      tenant: 'frs',
      environment: 'local',
      activity: { reachable: true, observed: true, outage: false, busy: false },
    }),
  );

  assert.equal(next.items[0]?.environments[0]?.activity?.outage, false);
  assert.equal(next.items[1]?.environments[0]?.activity?.outage, false);
});

// A clean fetch (setOrchestrators) is the build-time join and must keep
// carrying whatever activity the read model resolved, independent of the live
// patch reducer above — this is what makes a page reload correct without
// waiting on the next poller transition.
test('a fresh fetch still reflects the joined snapshot with no event required', () => {
  const state = stateWith([
    orchestrator({
      environments: [
        {
          tenant: 'frs',
          environment: 'local',
          directory: '/tmp/a',
          role: '',
          activity: {
            reachable: true,
            observed: true,
            outage: false,
            busy: true,
            detail: 'holding: x',
          },
        },
      ],
    }),
  ]);

  assert.deepEqual(state.items[0]?.environments[0]?.activity, {
    reachable: true,
    observed: true,
    outage: false,
    busy: true,
    detail: 'holding: x',
  });
});
