import type { StartSessionResult, UISelection } from '@/types';

import {
  EndAISessions,
  StartAISession,
  StartContributeAISession,
  StartLocalSession,
  StartSession,
} from '../../wailsjs/go/main/App';
import { startAITabOrPrompt } from './aiOccupancyThunks';
import { readError } from './errors';
import { hideTerminalMessage, showTerminalError, showTerminalMessage } from './notificationThunks';
import { selectEnvHasFailedDeploy } from './selectors';
import { trackOpenSession } from './slices/sessionsSlice';
import { setSelectedSessionForEnv, setSessionId } from './slices/terminalSlice';
import { setTerminalCopyOutput, setTerminalCopyStatus } from './slices/terminalStatusSlice';
import type { TerminalTab, TerminalTabKind } from './state';
import type { AppThunk } from './store';
import { recordTab } from './tabsThunks';
import { requireController } from './thunkExtra';
import { selectionKey } from './versionSuggestions';

const respawnableDefaultKinds: ReadonlySet<TerminalTabKind> = new Set(['ai', 'local', 'erun']);

// maybeRespawnDeadDefaultTab returns true when it handled the click; the respawn
// completes asynchronously.
export const maybeRespawnDeadDefaultTab =
  (sessionId: number): AppThunk<boolean> =>
  (dispatch, getState, extra) => {
    const controller = requireController(extra);
    const state = getState();
    if (!state.sessions.exitReasons[sessionId]) {
      return false;
    }
    const selection = state.selection.selected;
    if (!selection) {
      return false;
    }
    const runSelection = { ...selection };
    const key = selectionKey(runSelection);
    const tab = state.terminal.tabsByEnv[key]?.find((t) => t.sessionId === sessionId);
    if (!tab || !respawnableDefaultKinds.has(tab.kind)) {
      return false;
    }
    // Respawning a failed-deploy env would re-deploy it in a loop.
    if (selectEnvHasFailedDeploy(state, selection.tenant, selection.environment)) {
      return false;
    }
    if (sessionId !== state.terminal.sessionId) {
      dispatch(setSessionId(sessionId));
    }
    dispatch(setTerminalCopyOutput(''));
    dispatch(setTerminalCopyStatus(''));
    dispatch(
      showTerminalMessage(
        `Reopening ${respawnLabelForKind(tab.kind)} for ${selection.tenant} / ${selection.environment}...`,
        true,
      ),
    );
    const { cols, rows } = controller.terminalSize();
    void dispatch(respawnDefaultTab(runSelection, tab, key, cols, rows));
    return true;
  };

function respawnLabelForKind(kind: TerminalTabKind): string {
  switch (kind) {
    case 'ai':
      return 'AI session';
    case 'local':
      return 'Local shell';
    case 'erun':
      return 'ERun session';
    default:
      return 'session';
  }
}

const respawnDefaultTab =
  (
    runSelection: UISelection,
    tab: TerminalTab,
    key: string,
    cols: number,
    rows: number,
  ): AppThunk<Promise<void>> =>
  async (dispatch, _getState, extra) => {
    const controller = requireController(extra);
    let result: StartSessionResult | null;
    try {
      if (tab.kind === 'ai') {
        result = await dispatch(
          startAITabOrPrompt({
            key,
            selection: runSelection,
            slot: tab.slot,
            cols,
            rows,
            label: tab.label,
          }),
        );
      } else {
        result = await startSessionForKind(runSelection, tab, cols, rows);
      }
    } catch (error: unknown) {
      dispatch(showTerminalError(readError(error)));
      return;
    }
    if (!result) {
      dispatch(hideTerminalMessage());
      return;
    }
    if (tab.kind === 'erun') {
      dispatch(trackOpenSession({ key, sessionId: result.sessionId, selection: runSelection }));
    }
    dispatch(recordTab(key, result.sessionId, result.slot ?? tab.slot, tab.kind, tab.label));
    dispatch(setSelectedSessionForEnv({ key, sessionId: result.sessionId }));
    dispatch(setSessionId(result.sessionId));
    dispatch(hideTerminalMessage());
    controller.focusTerminalSoon();
    controller.queueTerminalResize();
  };

// startSessionForKind skips the occupancy gate: a launch-flag relaunch restarts
// an existing agent, not a second one.
async function startSessionForKind(
  runSelection: UISelection,
  tab: TerminalTab,
  cols: number,
  rows: number,
): Promise<StartSessionResult> {
  switch (tab.kind) {
    case 'ai':
      return await StartAISession(runSelection, tab.slot, cols, rows, true);
    case 'local':
      return await StartLocalSession(runSelection, tab.slot, cols, rows);
    case 'erun':
      return await StartSession(runSelection, tab.slot, cols, rows);
    case 'contribute-ai':
      return await StartContributeAISession(runSelection, tab.slot, cols, rows);
    default:
      throw new Error(`cannot respawn tab kind ${tab.kind}`);
  }
}

function isAITabKind(tab: TerminalTab): boolean {
  return tab.kind === 'ai' || tab.kind === 'contribute-ai';
}

// relaunchAISessionsForLaunchChange ends and respawns AI sessions, since launch
// flags are only read when a pod session is created.
export const relaunchAISessionsForLaunchChange =
  (selection: UISelection): AppThunk<Promise<void>> =>
  async (dispatch, getState, extra) => {
    const controller = requireController(extra);
    const state = getState();
    const runSelection = { ...selection };
    const key = selectionKey(runSelection);
    const aiTabs = (state.terminal.tabsByEnv[key] ?? []).filter(isAITabKind);
    const hasTabs = aiTabs.length > 0;
    if (hasTabs) {
      dispatch(
        showTerminalMessage(
          `Reopening AI session for ${selection.tenant} / ${selection.environment}...`,
          true,
        ),
      );
    }
    try {
      const ended = await EndAISessions(runSelection);
      if (ended) {
        for (const tab of aiTabs) {
          await dispatch(respawnRelaunchedAITab(runSelection, key, tab, controller.terminalSize()));
        }
      }
      if (hasTabs) {
        dispatch(hideTerminalMessage());
        controller.queueTerminalResize();
      }
    } catch (error: unknown) {
      dispatch(showTerminalError(readError(error)));
    }
  };

const respawnRelaunchedAITab =
  (
    runSelection: UISelection,
    key: string,
    tab: TerminalTab,
    size: { cols: number; rows: number },
  ): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    const result = await startSessionForKind(runSelection, tab, size.cols, size.rows);
    const wasDisplayed = getState().terminal.sessionId === tab.sessionId;
    const wasSelectedForEnv = getState().terminal.selectedSessionByEnv[key] === tab.sessionId;
    dispatch(recordTab(key, result.sessionId, result.slot ?? tab.slot, tab.kind, tab.label));
    if (wasSelectedForEnv) {
      dispatch(setSelectedSessionForEnv({ key, sessionId: result.sessionId }));
    }
    if (wasDisplayed) {
      dispatch(setSessionId(result.sessionId));
    }
  };
