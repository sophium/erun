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

// checkGatewayRedeploy asks which environments' running pods still route where
// they were last deployed to, and offers to redeploy them.
//
// The gateway is rendered into each runtime at deploy time, so switching it in
// ERun settings changes no pod by itself — before this the switch looked like it
// did nothing. It runs after the save rather than before closing the dialog
// because it reads every cluster; nothing is offered when every pod already
// matches, and a read that fails offers nothing rather than guessing.
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

// confirmGatewayRedeploy redeploys each listed environment at the version it
// already runs, in its own Local shell, the way Upgrade all does: in parallel,
// with each env's output and failure kept on that env, and without selecting it
// or launching its AI tab as a side effect.
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

// loadManageGatewayRouting raises the Manage dialog's pending-redeploy banner
// when this environment's running pod routes somewhere its saved configuration
// no longer asks for. The banner is otherwise only raised by a save made in the
// same dialog session, so a gateway switched in ERun settings — or an opt-out
// saved before the dialog was last closed — left a pod on the old routing with
// nothing saying so.
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
