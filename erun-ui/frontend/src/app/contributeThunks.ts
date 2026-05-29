import type { UISelection } from '@/types';

import {
  GetContributeMode,
  IsContributeEligible,
  SetContributeMode,
  StartContributeAISession,
  StartContributeSession,
} from '../../wailsjs/go/main/App';
import {
  contributeEnvKey,
  type DiffSource,
  setContributeFlag,
  setDiffSource,
} from './slices/contributeSlice';
import type { AppDispatch, AppThunk, RootState } from './store';
import { recordTab, removeTab } from './tabsThunks';
import { selectionKey } from './versionSuggestions';

const CONTRIBUTE_ERUN_SLOT = 0;
const CONTRIBUTE_AI_SLOT = 0;
const DEFAULT_COLS = 120;
const DEFAULT_ROWS = 34;

export interface ToggleContributeResult {
  enabled: boolean;
  error?: string;
}

/** Toggle the Contribute switch for the currently-selected env. */
export const toggleContribute =
  (selection: UISelection): AppThunk<Promise<ToggleContributeResult>> =>
  async (dispatch, getState) => {
    const tenant = selection.tenant.trim();
    const environment = selection.environment.trim();
    if (!tenant || !environment) {
      return { enabled: false, error: 'No environment selected.' };
    }
    const flagKey = contributeEnvKey(tenant, environment);
    const currentlyOn = Boolean(getState().contribute.flagsByEnv[flagKey]);
    const nextOn = !currentlyOn;
    try {
      await SetContributeMode(selection, nextOn);
      dispatch(setContributeFlag({ key: flagKey, enabled: nextOn }));
      const tabsKey = selectionKey(selection);
      if (nextOn) {
        await spawnContributeTabs(dispatch, tabsKey, selection);
      } else {
        removeContributeTabsFromState(dispatch, getState, tabsKey);
      }
      return { enabled: nextOn };
    } catch (e) {
      const message = e instanceof Error ? e.message : String(e);
      return { enabled: currentlyOn, error: message };
    }
  };

async function spawnContributeTabs(
  dispatch: AppDispatch,
  key: string,
  selection: UISelection,
): Promise<void> {
  const erunResult = await StartContributeSession(
    selection,
    CONTRIBUTE_ERUN_SLOT,
    DEFAULT_COLS,
    DEFAULT_ROWS,
  );
  dispatch(
    recordTab(
      key,
      erunResult.sessionId,
      erunResult.slot ?? CONTRIBUTE_ERUN_SLOT,
      'contribute-erun',
      'ERun (contribute)',
    ),
  );
  const aiResult = await StartContributeAISession(
    selection,
    CONTRIBUTE_AI_SLOT,
    DEFAULT_COLS,
    DEFAULT_ROWS,
  );
  dispatch(
    recordTab(
      key,
      aiResult.sessionId,
      aiResult.slot ?? CONTRIBUTE_AI_SLOT,
      'contribute-ai',
      'AI (contribute)',
    ),
  );
}

function removeContributeTabsFromState(
  dispatch: AppDispatch,
  getState: () => RootState,
  key: string,
): void {
  const tabs = getState().terminal.tabsByEnv[key] ?? [];
  for (const tab of tabs) {
    if (tab.kind === 'contribute-erun' || tab.kind === 'contribute-ai') {
      dispatch(removeTab(key, tab.sessionId));
    }
  }
}

/** Sync slice with the persisted backend flag for the given selection. */
export const refreshContributeFlag =
  (selection: UISelection): AppThunk<Promise<boolean>> =>
  async (dispatch) => {
    const tenant = selection.tenant.trim();
    const environment = selection.environment.trim();
    if (!tenant || !environment) {
      return false;
    }
    const key = contributeEnvKey(tenant, environment);
    try {
      const enabled = await GetContributeMode(selection);
      dispatch(setContributeFlag({ key, enabled }));
      return enabled;
    } catch {
      return false;
    }
  };

/** Resolve eligibility from the backend so the UI can show or hide the toggle. */
export async function isContributeToggleEligible(selection: UISelection): Promise<boolean> {
  if (!selection.tenant.trim() || !selection.environment.trim()) {
    return false;
  }
  try {
    return await IsContributeEligible(selection);
  } catch {
    return false;
  }
}

/** Flip the diff source for the env's review panel. */
export const switchDiffSource =
  (selection: UISelection, source: DiffSource): AppThunk =>
  (dispatch) => {
    const tenant = selection.tenant.trim();
    const environment = selection.environment.trim();
    if (!tenant || !environment) return;
    dispatch(setDiffSource({ key: contributeEnvKey(tenant, environment), source }));
  };
