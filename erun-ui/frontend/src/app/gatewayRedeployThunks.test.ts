import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { UIGatewayRouting } from '@/uiOpenRouterTypes';

import { checkGatewayRedeploy, loadManageGatewayRouting } from './gatewayRedeployThunks';
import { openGatewayRedeployDialog } from './slices/gatewayRedeploySlice';
import { patchManageDialog } from './slices/manageDialogSlice';
import { defaultManageDialog } from './state';
import type { AppDispatch, RootState } from './store';

const bindings = vi.hoisted(() => ({
  LoadGatewayRoutingDrift: vi.fn<() => Promise<UIGatewayRouting[]>>(),
  LoadEnvironmentGatewayRouting: vi.fn<() => Promise<UIGatewayRouting>>(),
  StartDeploySession: vi.fn(),
}));
vi.mock('../../wailsjs/go/main/App', () => bindings);

const stillOnTheGateway: UIGatewayRouting = {
  tenant: 'erun',
  environment: 'build',
  deployed: 'https://openrouter.ai/api',
  wanted: '',
};

function harness(manageDialog = defaultManageDialog()) {
  const dispatched: unknown[] = [];
  const dispatch = ((action: unknown) => {
    dispatched.push(action);
    return action;
  }) as AppDispatch;
  const getState = () => ({ manageDialog }) as unknown as RootState;
  return { dispatched, dispatch, getState };
}

beforeEach(() => {
  vi.clearAllMocks();
});

describe('checkGatewayRedeploy', () => {
  // A gateway toggle changes no running pod until a redeploy applies it.
  it('offers the environments still on the old routing', async () => {
    bindings.LoadGatewayRoutingDrift.mockResolvedValue([stillOnTheGateway]);
    const { dispatched, dispatch, getState } = harness();
    await checkGatewayRedeploy()(dispatch, getState, undefined as never);
    expect(dispatched).toContainEqual(openGatewayRedeployDialog([stillOnTheGateway]));
  });

  it('offers nothing when every pod already matches', async () => {
    bindings.LoadGatewayRoutingDrift.mockResolvedValue([]);
    const { dispatched, dispatch, getState } = harness();
    await checkGatewayRedeploy()(dispatch, getState, undefined as never);
    expect(dispatched).toEqual([]);
  });

  it('offers nothing when the clusters cannot be read', async () => {
    bindings.LoadGatewayRoutingDrift.mockRejectedValue(new Error('unreachable'));
    const { dispatched, dispatch, getState } = harness();
    await checkGatewayRedeploy()(dispatch, getState, undefined as never);
    expect(dispatched).toEqual([]);
  });
});

describe('loadManageGatewayRouting', () => {
  const selection = { tenant: 'erun', environment: 'build' };

  it('raises the pending-redeploy banner for a pod still on the old routing', async () => {
    bindings.LoadEnvironmentGatewayRouting.mockResolvedValue(stillOnTheGateway);
    const { dispatched, dispatch, getState } = harness({
      ...defaultManageDialog(),
      open: true,
      selection,
    });
    await loadManageGatewayRouting(selection)(dispatch, getState, undefined as never);
    expect(dispatched).toContainEqual(patchManageDialog({ pendingRedeploy: true }));
  });

  it('leaves the banner alone when the pod matches its saved configuration', async () => {
    bindings.LoadEnvironmentGatewayRouting.mockResolvedValue({
      tenant: '',
      environment: '',
      deployed: '',
      wanted: '',
    });
    const { dispatched, dispatch, getState } = harness({
      ...defaultManageDialog(),
      open: true,
      selection,
    });
    await loadManageGatewayRouting(selection)(dispatch, getState, undefined as never);
    expect(dispatched).toEqual([]);
  });

  it('does not mark a dialog that moved on to another environment', async () => {
    bindings.LoadEnvironmentGatewayRouting.mockResolvedValue(stillOnTheGateway);
    const { dispatched, dispatch, getState } = harness({
      ...defaultManageDialog(),
      open: true,
      selection: { tenant: 'erun', environment: 'ux' },
    });
    await loadManageGatewayRouting(selection)(dispatch, getState, undefined as never);
    expect(dispatched).toEqual([]);
  });
});
