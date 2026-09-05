import type { StartSessionResult, UISelection } from '@/types';

import { StartAISession } from '../../wailsjs/go/main/App';
import { readError } from './errors';
import { hideTerminalMessage } from './notificationThunks';
import {
  patchAIOccupancyPrompt,
  resetAIOccupancyPrompt,
  setAIOccupancyPrompt,
} from './slices/aiOccupancyPromptSlice';
import { setSelectedSessionForEnv, setSessionId } from './slices/terminalSlice';
import type { TerminalTabKind } from './state';
import type { AppThunk } from './store';
import { recordTab } from './tabsThunks';
import { selectionKey } from './versionSuggestions';

interface StartAITabParams {
  key: string;
  selection: UISelection;
  slot: number;
  cols: number;
  rows: number;
  label: string;
}

const AI_TAB_KIND: TerminalTabKind = 'ai';

// startAITabOrPrompt is the single AI-tab spawn chokepoint for every caller
// (initial env open, dead-tab respawn): it starts unconfirmed, and when the
// environment is already held by another job's activity lease, opens the
// occupancy prompt instead of recording a tab — starting a second agent stays
// a deliberate choice (erun#1221). Returns the started session, or null when
// the prompt took over and nothing was started yet.
export const startAITabOrPrompt =
  (params: StartAITabParams): AppThunk<Promise<StartSessionResult | null>> =>
  async (dispatch) => {
    const { key, selection, slot, cols, rows, label } = params;
    const result = (await StartAISession(selection, slot, cols, rows, false)) as StartSessionResult;
    if (result.occupancy && result.occupancy.length > 0) {
      dispatch(
        setAIOccupancyPrompt({
          open: true,
          leases: result.occupancy,
          pending: { key, selection: { ...selection }, slot, cols, rows, label },
          starting: false,
          error: '',
        }),
      );
      return null;
    }
    dispatch(recordTab(key, result.sessionId, result.slot ?? slot, AI_TAB_KIND, label));
    return result;
  };

export const cancelAIOccupancyPrompt = (): AppThunk => (dispatch) => {
  dispatch(resetAIOccupancyPrompt());
};

// confirmAIOccupancyPrompt is the explicit "start a second agent anyway": it
// retries the pending start with confirmed=true and, only on success, records
// the tab and (if the user is still looking at this environment) surfaces it.
export const confirmAIOccupancyPrompt =
  (): AppThunk<Promise<void>> => async (dispatch, getState) => {
    const state = getState().aiOccupancyPrompt;
    if (!state.open || !state.pending || state.starting) {
      return;
    }
    const { key, selection, slot, cols, rows, label } = state.pending;
    dispatch(patchAIOccupancyPrompt({ starting: true, error: '' }));
    try {
      const result = (await StartAISession(
        selection,
        slot,
        cols,
        rows,
        true,
      )) as StartSessionResult;
      dispatch(recordTab(key, result.sessionId, result.slot ?? slot, AI_TAB_KIND, label));
      const current = getState().selection.selected;
      if (current && selectionKey(current) === key) {
        dispatch(setSelectedSessionForEnv({ key, sessionId: result.sessionId }));
        dispatch(setSessionId(result.sessionId));
        dispatch(hideTerminalMessage());
      }
      dispatch(resetAIOccupancyPrompt());
    } catch (error: unknown) {
      dispatch(patchAIOccupancyPrompt({ starting: false, error: readError(error) }));
    }
  };
