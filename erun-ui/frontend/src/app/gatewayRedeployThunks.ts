import type { StartSessionResult, UISelection } from '@/types';
import type { UIGatewayRouting } from '@/uiOpenRouterTypes';

import {
  LoadEnvironmentGatewayRouting,
  LoadGatewayRoutingDrift,
  StartDeploySession,
} from '../../wailsjs/go/main/App';
import { showNotification } from './notificationThunks';
import {
  closeGatewayRedeployDialog,
  openGatewayRedeployDialog,
} from './slices/gatewayRedeploySlice';
import { patchManageDialog } from './slices/manageDialogSlice';
import type { AppThunk } from './store';
import { recordTab } from './tabsThunks';
import { requireController } from './thunkExtra';
import { selectionKey } from './versionSuggestions';

// checkGatewayRedeploy offers to redeploy environments whose pods still route to
// the old gateway, which is rendered at deploy time. A failed read offers nothing.
export const checkGatewayRedeploy = (): AppThunk<Promise<void>> => async (dispatch) => {
  let drift: UIGatewayRouting[];
  try {
    drift = await LoadGatewayRoutingDrift();
  } catch {
    return;
  }
  if (drift.length > 0) {
    dispatch(openGatewayRedeployDialog(drift));
  }
};

export { closeGatewayRedeployDialog };

// confirmGatewayRedeploy redeploys each env at its current version, in parallel,
// without selecting it.
export const confirmGatewayRedeploy =
  (): AppThunk<Promise<void>> => async (dispatch, getState, extra) => {
    const items = getState().gatewayRedeploy.items;
    dispatch(closeGatewayRedeployDialog());
    if (items.length === 0) {
      return;
    }
    const controller = requireController(extra);
    controller.fitTerminal();
    const { cols, rows } = controller.terminalSize();
    const outcomes = await Promise.allSettled(
      items.map(async (item) => {
        const selection: UISelection = { tenant: item.tenant, environment: item.environment };
        const result = (await StartDeploySession(selection, cols, rows)) as StartSessionResult;
        dispatch(
          recordTab(selectionKey(selection), result.sessionId, result.slot ?? 0, 'local', 'Local'),
        );
      }),
    );
    const failed = items
      .filter((_, index) => outcomes[index]?.status === 'rejected')
      .map((item) => `${item.tenant}/${item.environment}`);
    if (failed.length > 0) {
      dispatch(
        showNotification(
          'error',
          `Could not start the redeploy for ${failed.join(', ')} — see the Local tab and Activities.`,
        ),
      );
      return;
    }
    dispatch(
      showNotification(
        'info',
        items.length === 1
          ? `Redeploying ${items[0]?.tenant ?? ''} / ${items[0]?.environment ?? ''} to apply the gateway change — follow progress in its Local tab.`
          : `Redeploying ${String(items.length)} environments to apply the gateway change — follow progress in their Local tabs.`,
      ),
    );
  };

// loadManageGatewayRouting raises the pending-redeploy banner when the running pod
// routes somewhere the saved configuration no longer asks for.
export const loadManageGatewayRouting =
  (selection: UISelection): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    let drift: UIGatewayRouting;
    try {
      drift = await LoadEnvironmentGatewayRouting(selection);
    } catch {
      return;
    }
    const current = getState().manageDialog;
    if (
      drift.environment === '' ||
      !current.open ||
      current.selection?.tenant !== selection.tenant ||
      current.selection.environment !== selection.environment
    ) {
      return;
    }
    dispatch(patchManageDialog({ pendingRedeploy: true }));
  };
