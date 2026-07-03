import type { UISelection } from '@/types';

import { CloseEnvironmentSessions } from '../../wailsjs/go/main/App';
import { readError } from './errors';
import { showTerminalMessage } from './notificationThunks';
import { setSelected } from './slices/selectionSlice';
import { clearEnvOpening } from './slices/sessionsSlice';
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
    try {
      await CloseEnvironmentSessions({ tenant, environment });
    } catch (error: unknown) {
      dispatch(showTerminalMessage(readError(error)));
      return;
    }
    const key = selectionKey({ tenant, environment });
    dispatch(clearTabsForEnv(key));
    dispatch(clearSelectedSessionForEnv(key));
    dispatch(clearEnvOpening({ tenant, environment }));
    const current = getState().selection.selected;
    if (current?.tenant === tenant && current.environment === environment) {
      dispatch(setSelected(null));
      dispatch(setSessionId(0));
    }
  };
