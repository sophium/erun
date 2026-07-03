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

// autoStartThunks own the first-time auto-start prompt flow:
//
//   - openAutoStartPrompt(selection) is fired by openSelection when the env
//     it was asked to navigate to is remote, has a stopped cloud context,
//     and has no AutoStart override on file yet (state.tenants[].autoStart
//     === undefined).
//   - confirmAutoStartPrompt(choice) persists the user's answer via
//     SetEnvironmentAutoStart, mirrors the tenants slice so subsequent
//     navigation no longer prompts, closes the dialog, and re-fires
//     openSelection so the just-saved policy is applied immediately (no
//     "click again to take effect" footgun, Nielsen #1/#3).
//   - cancelAutoStartPrompt closes the dialog without persisting; nothing
//     is spawned and the env is left untouched, matching what would have
//     happened if the user had not clicked the env in the first place.

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
      // openSelection is re-entered with the override already in place, so
      // the decision tree falls straight through to the matching branch
      // (always → spawn ERun, never → spawn Local only).
      await dispatch(openSelection(selection));
    } catch (error: unknown) {
      const message = readError(error);
      dispatch(patchAutoStartPrompt({ saving: false, error: message }));
      dispatch(showNotification('error', `Could not save auto-start preference: ${message}`));
    }
  };
