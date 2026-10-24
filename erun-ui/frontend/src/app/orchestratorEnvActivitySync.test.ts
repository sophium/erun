import assert from 'node:assert/strict';

import { configureStore } from '@reduxjs/toolkit';
import { test } from 'vitest';

import { environmentIndicator } from '@/components/app/Sidebar.helpers';

import { orchestratorEnvironmentLine } from './orchestratorEnvironmentActivity';
import envStatusReducer from './slices/envStatusSlice';
import orchestratorsReducer, { setOrchestrators } from './slices/orchestratorsSlice';
import type { AppDispatch } from './store';
import { selectionKey } from './versionSuggestions';
import { handleEnvActivity } from './wailsEventThunks';

// The sidebar row and the orchestrator card must both follow one env-activity event.
function buildStore(orchestratorId: string, tenant: string, environment: string) {
  const store = configureStore({
    reducer: { envStatus: envStatusReducer, orchestrators: orchestratorsReducer },
  });
  store.dispatch(
    setOrchestrators([
      {
        id: orchestratorId,
        name: orchestratorId,
        environments: [{ tenant, environment, directory: '/tmp/a', role: '' }],
        tenants: [tenant],
        directories: ['/tmp/a'],
        sessionId: 1,
        status: 'running',
        busy: false,
        transient: false,
        shellRunning: false,
        shellCommand: '',
        shellStartedAtUnix: 0,
        nudgeCount: 0,
        autoNudgeCount: 0,
        whipCount: 0,
        nudgeCapped: false,
        restartRequired: false,
        roleChanged: false,
        launchSettingsChanged: false,
      },
    ]),
  );
  return store;
}

function rowIsFailure(store: ReturnType<typeof buildStore>, key: string): boolean {
  const activity = store.getState().envStatus.activityByEnv[key];
  const indicator = environmentIndicator({
    name: key,
    envState: '',
    isOpen: true,
    reachable: activity?.reachable ?? false,
    outage: activity?.outage ?? false,
    busy: activity?.busy ?? false,
    detail: activity?.detail ?? '',
  });
  return indicator.dot === 'failed';
}

function cardIsFailure(store: ReturnType<typeof buildStore>, orchestratorId: string): boolean {
  const orchestrator = store
    .getState()
    .orchestrators.items.find((item) => item.id === orchestratorId);
  const ref = orchestrator?.environments[0];
  if (!ref) {
    throw new Error('expected a seeded env ref');
  }
  return orchestratorEnvironmentLine(ref).state === 'outage';
}

test('an env-activity event updates the orchestrator card, not just the sidebar row', () => {
  const tenant = 'frs';
  const environment = 'local';
  const store = buildStore('orch', tenant, environment);
  const dispatch = store.dispatch as unknown as AppDispatch;

  dispatch(
    handleEnvActivity({
      tenant,
      environment,
      reachable: false,
      observed: false,
      outage: true,
      busy: false,
    }),
  );
  assert.equal(cardIsFailure(store, 'orch'), true, 'card should show the outage once reported');

  dispatch(
    handleEnvActivity({
      tenant,
      environment,
      reachable: true,
      observed: true,
      outage: false,
      busy: false,
    }),
  );
  assert.equal(
    cardIsFailure(store, 'orch'),
    false,
    'card should clear the outage once the poller recovers',
  );
});

test('no combination of the poller observation makes the row and the card disagree', () => {
  const tenant = 'frs';
  const environment = 'local';
  const store = buildStore('orch', tenant, environment);
  const dispatch = store.dispatch as unknown as AppDispatch;
  const key = selectionKey({ tenant, environment });

  const booleans = [false, true];
  for (const reachable of booleans) {
    for (const observed of booleans) {
      for (const outage of booleans) {
        for (const busy of booleans) {
          dispatch(handleEnvActivity({ tenant, environment, reachable, observed, outage, busy }));
          assert.equal(
            rowIsFailure(store, key),
            cardIsFailure(store, 'orch'),
            `row and card disagreed for reachable=${String(reachable)} observed=${String(observed)} outage=${String(outage)} busy=${String(busy)}`,
          );
        }
      }
    }
  }
});

test('a clean boot seeds the card from the fetch alone, before any event arrives', () => {
  const store = configureStore({
    reducer: { envStatus: envStatusReducer, orchestrators: orchestratorsReducer },
  });
  store.dispatch(
    setOrchestrators([
      {
        id: 'orch',
        name: 'orch',
        environments: [
          {
            tenant: 'frs',
            environment: 'local',
            directory: '/tmp/a',
            role: '',
            activity: { reachable: false, observed: false, outage: true, busy: false },
          },
        ],
        tenants: ['frs'],
        directories: ['/tmp/a'],
        sessionId: 1,
        status: 'running',
        busy: false,
        transient: false,
        shellRunning: false,
        shellCommand: '',
        shellStartedAtUnix: 0,
        nudgeCount: 0,
        autoNudgeCount: 0,
        whipCount: 0,
        nudgeCapped: false,
        restartRequired: false,
        roleChanged: false,
        launchSettingsChanged: false,
      },
    ]),
  );

  assert.equal(cardIsFailure(store, 'orch'), true);
});
