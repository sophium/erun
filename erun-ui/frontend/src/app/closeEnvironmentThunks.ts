import type { UISelection } from '@/types';

import { CloseEnvironmentSessions } from '../../wailsjs/go/main/App';
import { readError } from './errors';
import { showTerminalError } from './notificationThunks';
import { setEnvActivityForEnv } from './slices/envStatusSlice';
import { setSelected } from './slices/selectionSlice';
import { clearEnvClosing, clearEnvOpening, markEnvClosing } from './slices/sessionsSlice';
import { clearSelectedSessionForEnv, clearTabsForEnv, setSessionId } from './slices/terminalSlice';
import type { AppThunk } from './store';
import { selectionKey } from './versionSuggestions';

// closeEnvironment tears down an env's desktop tabs and session
// state (the sidebar's "open env" dot). Desktop-only — it does NOT
// touch cloud state, unlike the cloud-context Stop button.
// Per-session bookkeeping is unwound separately by handleTerminalExit
// as each closed session fires its exit event; this thunk owns only
// the env-scoped state that chain leaves behind.
export const closeEnvironment =
  (selection: UISelection): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    const tenant = selection.tenant.trim();
    const environment = selection.environment.trim();
    if (!tenant || !environment) {
      return;
    }
    const key = selectionKey({ tenant, environment });
    // Marked before the RPC even starts, so it is in place before any of this
    // close's own terminal-exit events can possibly arrive. Those events land
    // asynchronously over the SSE stream and race clearTabsForEnv below — an
    // ERun-tab exit processed first (while the AI tab is still nominally
    // present) would otherwise read the tear-down as an unexpected death and
    // respawn a sibling default tab moments after the user closed it.
    dispatch(markEnvClosing(key));
    try {
      await CloseEnvironmentSessions({ tenant, environment });
    } catch (error: unknown) {
      dispatch(clearEnvClosing(key));
      dispatch(showTerminalError(readError(error)));
      return;
    }
    dispatch(clearTabsForEnv(key));
    dispatch(clearEnvClosing(key));
    // Close is a definitive teardown of the desktop view; clear the observed
    // activity (including the AI badge) so the sidebar row stops spinning
    // even if the environment-activity poller's next tick is still 20s away.
    dispatch(
      setEnvActivityForEnv({
        key,
        activity: { reachable: false, observed: false, outage: false, busy: false, detail: '' },
      }),
    );
    dispatch(clearSelectedSessionForEnv(key));
    dispatch(clearEnvOpening({ tenant, environment }));
    const current = getState().selection.selected;
    if (current?.tenant === tenant && current.environment === environment) {
      dispatch(setSelected(null));
      dispatch(setSessionId(0));
    }
  };
