import type { StartSessionResult, UISelection } from '@/types';

import {
  StartCreateVersionSession,
  StartDeploySession,
  StartInitialDeploySession,
  StartInitSession,
  StartLocalSession,
  StartSession,
} from '../../wailsjs/go/main/App';
import { startAITabOrPrompt } from './aiOccupancyThunks';
import { resolveAutoStartGate } from './autoStartGate';
import { environmentTypeIsHost } from './environmentType';
import { readError } from './errors';
import { hideTerminalMessage, showTerminalError, showTerminalMessage } from './notificationThunks';
import { reattachRemoteTerminalTabs } from './remoteSessionTabsThunks';
import { loadReviewDiff } from './reviewThunks';
import {
  selectActiveSlotForSelection,
  selectEnvironmentExists,
  selectEnvironmentType,
} from './selectors';
import { createIsCurrentSelection, isStaleDefaultLandingOpen } from './sessionOpenGuards';
import { isNewSessionSelection } from './sessionSelection';
import { setAutoStartPrompt } from './slices/autoStartPromptSlice';
import { setIdleStatus } from './slices/idleSlice';
import { setEnvReviewCommit, setEnvReviewScope, setSelectedDiffPath } from './slices/reviewSlice';
import { setSelected } from './slices/selectionSlice';
import {
  clearEnvOpening,
  markEnvOpening,
  resetEnvOpening,
  trackOpenSession,
} from './slices/sessionsSlice';
import { resetTenantDashboard } from './slices/tenantDashboardSlice';
import { setSelectedSessionForEnv, setSessionId } from './slices/terminalSlice';
import { setTerminalCopyOutput, setTerminalCopyStatus } from './slices/terminalStatusSlice';
import type { TerminalTab, TerminalTabKind } from './state';
import type { AppThunk } from './store';
import { recordTab } from './tabsThunks';
import { closeTerminalTab, selectTerminalTab } from './terminalTabThunks';
import { requireController } from './thunkExtra';
import { selectionKey } from './versionSuggestions';

const spawnDefaultTab =
  (
    key: string,
    runSelection: UISelection,
    kind: 'local' | 'ai',
    label: string,
    cols: number,
    rows: number,
  ): AppThunk<Promise<void>> =>
  async (dispatch) => {
    try {
      if (kind === 'ai') {
        await dispatch(
          startAITabOrPrompt({ key, selection: runSelection, slot: 0, cols, rows, label }),
        );
        return;
      }
      const result = (await StartLocalSession(runSelection, 0, cols, rows)) as StartSessionResult;
      dispatch(recordTab(key, result.sessionId, result.slot ?? 0, kind, label));
    } catch {
      // Tool unavailable; future env opens will retry.
    }
  };

// Starts an ERun tab without stealing the active session.
const spawnERunTabPassive =
  (key: string, runSelection: UISelection, cols: number, rows: number): AppThunk<Promise<void>> =>
  async (dispatch) => {
    try {
      const result = (await StartSession(runSelection, 0, cols, rows)) as StartSessionResult;
      dispatch(trackOpenSession({ key, sessionId: result.sessionId, selection: runSelection }));
      dispatch(recordTab(key, result.sessionId, result.slot ?? 0, 'erun', 'ERun'));
    } catch {
      // ERun failed to spawn; future env opens will retry.
    }
  };

const spawnDefaultKind =
  (
    key: string,
    runSelection: UISelection,
    kind: TerminalTabKind,
    label: string,
    cols: number,
    rows: number,
  ): AppThunk<Promise<void>> =>
  async (dispatch) => {
    if (kind === 'erun') {
      await dispatch(spawnERunTabPassive(key, runSelection, cols, rows));
    } else if (kind === 'local' || kind === 'ai') {
      await dispatch(spawnDefaultTab(key, runSelection, kind, label, cols, rows));
    }
  };

// Repoints the remembered tab at the respawned session so restoreSelectedTabForEnv finds it.
const repointRememberedAfterRespawn =
  (key: string, previous: TerminalTab, kind: TerminalTabKind): AppThunk =>
  (dispatch, getState) => {
    const replacement = (getState().terminal.tabsByEnv[key] ?? []).find(
      (tab) => tab.kind === kind && tab.slot === previous.slot,
    );
    if (replacement && replacement.sessionId !== previous.sessionId) {
      dispatch(setSelectedSessionForEnv({ key, sessionId: replacement.sessionId }));
    }
  };

// AI/Local tabs register no openSelection, so their dead tab must be replaced here.
const ensureLiveDefaultTab =
  (
    key: string,
    runSelection: UISelection,
    kind: TerminalTabKind,
    label: string,
    cols: number,
    rows: number,
  ): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    const existing = (getState().terminal.tabsByEnv[key] ?? []).find((tab) => tab.kind === kind);
    const dead = existing ? !!getState().sessions.exitReasons[existing.sessionId] : false;
    if (existing && !dead) {
      return;
    }
    const remembered =
      existing && getState().terminal.selectedSessionByEnv[key] === existing.sessionId
        ? existing
        : null;
    await dispatch(spawnDefaultKind(key, runSelection, kind, label, cols, rows));
    if (remembered) {
      dispatch(repointRememberedAfterRespawn(key, remembered, kind));
    }
  };

export const ensureDefaultEnvTabs =
  (runSelection: UISelection, key: string, cols: number, rows: number): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    // A host env has no pod, so only the Local tab applies, matching resolveAutoStartGate.
    const envType = selectEnvironmentType(
      getState(),
      runSelection.tenant,
      runSelection.environment,
    );
    if (environmentTypeIsHost(envType)) {
      await dispatch(ensureLiveDefaultTab(key, runSelection, 'local', 'Local', cols, rows));
      return;
    }
    await dispatch(ensureLiveDefaultTab(key, runSelection, 'erun', 'ERun', cols, rows));
    await dispatch(ensureLiveDefaultTab(key, runSelection, 'local', 'Local', cols, rows));
    await dispatch(ensureLiveDefaultTab(key, runSelection, 'ai', 'AI', cols, rows));
  };

// The renderer drops PTY writes that don't match the current sessionId.
const surfaceEnvSession =
  (key: string): AppThunk =>
  (dispatch, getState) => {
    const state = getState();
    const tabs = state.terminal.tabsByEnv[key] ?? [];
    const remembered = state.terminal.selectedSessionByEnv[key] ?? 0;
    const next = tabs.some((tab) => tab.sessionId === remembered)
      ? remembered
      : (tabs.find((tab) => tab.kind === 'local')?.sessionId ?? 0);
    dispatch(setSessionId(next));
  };

// Records session bookkeeping even after the user navigates away, without
// stealing the terminal.
const trackOpenSessionMetadata =
  (key: string, result: StartSessionResult, runSelection: UISelection): AppThunk =>
  (dispatch) => {
    dispatch(trackOpenSession({ key, sessionId: result.sessionId, selection: runSelection }));
    const slot = result.slot ?? 0;
    const kind: TerminalTabKind = slot === 0 ? 'erun' : 'extra';
    const label = kind === 'erun' ? 'ERun' : `Terminal ${String(slot)}`;
    dispatch(recordTab(key, result.sessionId, slot, kind, label));
  };

export const registerOpenSessionResult =
  (key: string, result: StartSessionResult, runSelection: UISelection): AppThunk =>
  (dispatch, getState) => {
    dispatch(trackOpenSessionMetadata(key, result, runSelection));
    dispatch(setSessionId(result.sessionId));
    // Only seed the tab selection when nothing valid is remembered.
    const state = getState();
    const remembered = state.terminal.selectedSessionByEnv[key];
    const liveTabs = state.terminal.tabsByEnv[key] ?? [];
    const rememberedIsLive = remembered && liveTabs.some((tab) => tab.sessionId === remembered);
    if (!rememberedIsLive) {
      dispatch(setSelectedSessionForEnv({ key, sessionId: result.sessionId }));
    }
  };

const prepareOpenSelection =
  (selection: UISelection, previousSessionId: number, previousKnownSessionId: number): AppThunk =>
  (dispatch, getState) => {
    const state = getState();
    const previousKey = state.selection.selected ? selectionKey(state.selection.selected) : '';
    const newKey = selectionKey(selection);
    if (newKey !== previousKey) {
      // Range is per-env; reset only the env being opened.
      const envKey = `${selection.tenant}/${selection.environment}`;
      dispatch(setEnvReviewScope({ envKey, scope: 'current' }));
      dispatch(setEnvReviewCommit({ envKey, commit: '' }));
      dispatch(setSelectedDiffPath(''));
    }
    // selectionSyncMiddleware reconciles terminal.sessionId on every setSelected.
    dispatch(setSelected(selection));
    dispatch(setIdleStatus(null));
    if (!isNewSessionSelection(previousSessionId, previousKnownSessionId)) {
      return;
    }
    dispatch(setTerminalCopyOutput(''));
    dispatch(setTerminalCopyStatus(''));
    dispatch(
      showTerminalMessage(`Opening ${selection.tenant} / ${selection.environment}...`, true),
    );
  };

const restoreSelectedTabForEnv =
  (key: string): AppThunk =>
  (dispatch, getState) => {
    const state = getState();
    const tabs = state.terminal.tabsByEnv[key] ?? [];
    const remembered = state.terminal.selectedSessionByEnv[key];
    if (!remembered || !tabs.some((tab) => tab.sessionId === remembered)) {
      return;
    }
    if (remembered === state.terminal.sessionId) {
      return;
    }
    dispatch(selectTerminalTab(remembered));
  };

const showOpenSelectionStatus =
  (sessionId: number, selection: UISelection): AppThunk =>
  (dispatch, getState, extra) => {
    const controller = requireController(extra);
    const state = getState();
    const exitReason = state.sessions.exitReasons[sessionId] ?? '';
    if (exitReason) {
      dispatch(setTerminalCopyOutput(state.sessions.exitOutputs[sessionId] ?? ''));
      dispatch(setTerminalCopyStatus(''));
      dispatch(showTerminalMessage(exitReason));
      return;
    }
    if (controller.sessions.displayBuffer(sessionId).length > 0) {
      dispatch(hideTerminalMessage());
      return;
    }
    dispatch(
      showTerminalMessage(`Opening ${selection.tenant} / ${selection.environment}...`, true),
    );
  };

export const openSelection =
  (
    selection: UISelection,
    options: { isDefaultLandingOpen?: boolean } = {},
  ): AppThunk<Promise<void>> =>
  async (dispatch, getState, extra) => {
    if (isStaleDefaultLandingOpen(getState, options)) {
      return;
    }
    const controller = requireController(extra);
    dispatch(resetTenantDashboard());
    const runSelection = { ...selection };
    const key = selectionKey(runSelection);
    const previousSessionId = getState().terminal.sessionId;
    const previousKnownSessionId = getState().sessions.selectionToSessionId[key] ?? 0;
    // Captured so a failed StartSession can roll the sidebar back.
    const previousSelected = getState().selection.selected;

    const verdict = await resolveAutoStartGate(selection, getState);
    // A focus change during the gate (e.g. an orchestrator session) already won;
    // this stale call must not reclaim it.
    const stateAfterGate = getState();
    if (
      stateAfterGate.terminal.sessionId !== previousSessionId ||
      stateAfterGate.selection.selected !== previousSelected
    ) {
      return;
    }
    if (verdict === 'prompt') {
      dispatch(
        setAutoStartPrompt({
          open: true,
          selection: { ...selection },
          saving: false,
          error: '',
        }),
      );
      return;
    }
    const shouldSpawnERun = verdict !== 'skip-erun';

    const isCurrentSelection = createIsCurrentSelection(
      getState,
      selection,
      options.isDefaultLandingOpen,
    );

    dispatch(resetEnvOpening());
    dispatch(prepareOpenSelection(selection, previousSessionId, previousKnownSessionId));
    controller.fitTerminal();
    const { cols, rows } = controller.terminalSize();

    dispatch(markEnvOpening({ tenant: selection.tenant, environment: selection.environment }));
    try {
      // Local first so later spawns can log into it.
      await dispatch(ensureLiveDefaultTab(key, runSelection, 'local', 'Local', cols, rows));
      if (!isCurrentSelection()) {
        return;
      }
      dispatch(surfaceEnvSession(key));

      if (!shouldSpawnERun) {
        // autoStart=never: Local only.
        dispatch(hideTerminalMessage());
        controller.focusTerminalSoon();
        controller.queueTerminalResize();
        return;
      }

      const slot = selectActiveSlotForSelection(getState(), runSelection);
      const result = (await StartSession(runSelection, slot, cols, rows)) as StartSessionResult;
      await dispatch(
        finishOpenSession(key, result, runSelection, selection, cols, rows, isCurrentSelection),
      );
    } catch (error: unknown) {
      if (isCurrentSelection()) {
        dispatch(setSelected(previousSelected));
        dispatch(showTerminalError(readError(error)));
      }
      throw error;
    } finally {
      dispatch(clearEnvOpening({ tenant: selection.tenant, environment: selection.environment }));
    }
  };

// A session opened after the user navigated away is recorded but not shown.
const finishOpenSession =
  (
    key: string,
    result: StartSessionResult,
    runSelection: UISelection,
    selection: UISelection,
    cols: number,
    rows: number,
    isCurrentSelection: () => boolean,
  ): AppThunk<Promise<void>> =>
  async (dispatch, getState, extra) => {
    const controller = requireController(extra);
    if (!isCurrentSelection()) {
      dispatch(trackOpenSessionMetadata(key, result, runSelection));
      return;
    }
    dispatch(registerOpenSessionResult(key, result, runSelection));
    dispatch(showOpenSelectionStatus(result.sessionId, selection));
    const claimedSessionId = result.sessionId;

    await dispatch(ensureDefaultEnvTabs(runSelection, key, cols, rows));
    void dispatch(reattachRemoteTerminalTabs(runSelection, key, cols, rows));
    if (!isCurrentSelection()) {
      return;
    }
    // Don't re-point a pane the user moved to another tab of this env; the
    // renderer drops writes for any other session, leaving it blank.
    if (getState().terminal.sessionId === claimedSessionId) {
      dispatch(restoreSelectedTabForEnv(key));
    }

    if (getState().layout.reviewOpen) {
      await dispatch(loadReviewDiff());
    }
    if (!isCurrentSelection()) {
      return;
    }
    controller.focusTerminalSoon();
    controller.queueTerminalResize();
  };

// activateLocalAfterCommand activates the Local session after init/deploy.
export const activateLocalAfterCommand =
  (selection: UISelection, result: StartSessionResult): AppThunk<Promise<void>> =>
  async (dispatch, getState, extra) => {
    const controller = requireController(extra);
    if (result.orchestrated) {
      // Agent-env deploy runs in the background with no PTY to attach.
      dispatch(hideTerminalMessage());
      return;
    }
    const runSelection = { ...selection };
    const key = selectionKey(runSelection);
    dispatch(recordTab(key, result.sessionId, result.slot ?? 0, 'local', 'Local'));
    if (selectEnvironmentExists(getState(), selection.tenant, selection.environment)) {
      const { cols, rows } = controller.terminalSize();
      await dispatch(ensureDefaultEnvTabs(runSelection, key, cols, rows));
    }
    dispatch(setSessionId(result.sessionId));
    dispatch(setSelectedSessionForEnv({ key, sessionId: result.sessionId }));
    dispatch(hideTerminalMessage());
    controller.focusTerminalSoon();
    controller.queueTerminalResize();
  };

// No terminalMessage for init: the modal is still open and the message would
// only flash (erun-ui/AGENTS.md "UX Impact Review Checklist" item 3).
export const startInitSelection =
  (selection: UISelection): AppThunk<Promise<void>> =>
  async (dispatch, _getState, extra) => {
    const controller = requireController(extra);
    const runSelection = { ...selection };
    dispatch(setSelected(selection));
    dispatch(setTerminalCopyOutput(''));
    dispatch(setTerminalCopyStatus(''));
    controller.fitTerminal();
    const { cols, rows } = controller.terminalSize();
    const result = (await StartInitSession(runSelection, cols, rows)) as StartSessionResult;
    await dispatch(activateLocalAfterCommand(selection, result));
  };

const runDeploySessionSelection =
  (
    selection: UISelection,
    start: (runSelection: UISelection, cols: number, rows: number) => Promise<unknown>,
    message: string,
  ): AppThunk<Promise<void>> =>
  async (dispatch, _getState, extra) => {
    const controller = requireController(extra);
    const runSelection = { ...selection };
    dispatch(setSelected(selection));
    dispatch(setTerminalCopyOutput(''));
    dispatch(setTerminalCopyStatus(''));
    dispatch(showTerminalMessage(message, true));
    controller.fitTerminal();
    const { cols, rows } = controller.terminalSize();
    const result = (await start(runSelection, cols, rows)) as StartSessionResult;
    await dispatch(activateLocalAfterCommand(selection, result));
  };

// startDeploySelection installs an already-published version; it never builds.
export const startDeploySelection = (selection: UISelection): AppThunk<Promise<void>> =>
  runDeploySessionSelection(
    selection,
    StartDeploySession,
    `Deploying runtime for ${selection.tenant} / ${selection.environment}...`,
  );

// startInitialDeploySelection performs a new env's first deploy.
export const startInitialDeploySelection = (selection: UISelection): AppThunk<Promise<void>> =>
  runDeploySessionSelection(
    selection,
    StartInitialDeploySession,
    `Deploying runtime for ${selection.tenant} / ${selection.environment}...`,
  );

// startCreateVersionSelection builds, pushes and deploys a new version (local-agent only).
export const startCreateVersionSelection = (selection: UISelection): AppThunk<Promise<void>> =>
  runDeploySessionSelection(
    selection,
    StartCreateVersionSession,
    `Building & deploying a new version for ${selection.tenant} / ${selection.environment}...`,
  );

export const addTerminalTab = (): AppThunk<Promise<void>> => async (dispatch, getState, extra) => {
  const controller = requireController(extra);
  const state = getState();
  const selection = state.selection.selected;
  if (!selection) {
    return;
  }
  const runSelection = { ...selection };
  const key = selectionKey(runSelection);
  const tabs = state.terminal.tabsByEnv[key] ?? [];
  const nextSlot = tabs.reduce((max, tab) => (tab.slot >= max ? tab.slot + 1 : max), 0);
  try {
    const size = controller.terminalSize();
    const result = (await StartSession(
      runSelection,
      nextSlot,
      size.cols,
      size.rows,
    )) as StartSessionResult;
    dispatch(registerOpenSessionResult(key, result, runSelection));
    controller.focusTerminalSoon();
    controller.queueTerminalResize();
  } catch (error: unknown) {
    dispatch(showTerminalError(readError(error)));
  }
};

export { closeEnvironment } from './closeEnvironmentThunks';
export { closeTerminalTab, selectTerminalTab };
