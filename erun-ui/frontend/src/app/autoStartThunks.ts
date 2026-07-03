import type { UIEnvironmentConfig, UISelection } from '@/types';

import { SetEnvironmentAutoStart } from '../../wailsjs/go/main/App';
import { readError } from './errors';
import { showNotification } from './notificationThunks';
import { openSelection } from './sessionThunks';
import {
  patchAutoStartPrompt,
  resetAutoStartPrompt,
  setAutoStartPrompt,
} from './slices/autoStartPromptSlice';
import { patchTenantEnvironmentAutoStart } from './slices/tenantsSlice';
import type { AppThunk } from './store';

// The first-time auto-start prompt: openSelection raises it when a remote env
// with a stopped cloud context has no saved AutoStart choice yet. Confirming a
// choice re-runs openSelection so it takes effect immediately, without making
// the operator click the env a second time.

export type AutoStartChoice = 'always' | 'never';

export const openAutoStartPrompt =
  (selection: UISelection): AppThunk =>
  (dispatch) => {
    dispatch(
      setAutoStartPrompt({
        open: true,
        selection: { ...selection },
        saving: false,
        error: '',
      }),
    );
  };

export const cancelAutoStartPrompt = (): AppThunk => (dispatch) => {
  dispatch(resetAutoStartPrompt());
};

export const confirmAutoStartPrompt =
  (choice: AutoStartChoice): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    const state = getState().autoStartPrompt;
    if (!state.open || !state.selection || state.saving) {
      return;
    }
    const selection: UISelection = { ...state.selection };
    dispatch(patchAutoStartPrompt({ saving: true, error: '' }));
    try {
      const saved = (await SetEnvironmentAutoStart(selection, choice)) as UIEnvironmentConfig;
      dispatch(
        patchTenantEnvironmentAutoStart({
          tenant: selection.tenant,
          environment: selection.environment,
          autoStart: saved.autoStart,
        }),
      );
      dispatch(resetAutoStartPrompt());
      await dispatch(openSelection(selection));
    } catch (error: unknown) {
      const message = readError(error);
      dispatch(patchAutoStartPrompt({ saving: false, error: message }));
      dispatch(showNotification('error', `Could not save auto-start preference: ${message}`));
    }
  };
