import type { StartSessionResult, UISelection } from '@/types';

import {
  CloseSession,
  StartAISession,
  StartCreateVersionSession,
  StartDeploySession,
  StartInitialDeploySession,
  StartInitSession,
  StartLocalSession,
  StartSession,
} from '../../wailsjs/go/main/App';
import { resolveAutoStartGate } from './autoStartGate';
import { readError } from './errors';
import { hideTerminalMessage, showTerminalMessage } from './notificationThunks';
import { reattachRemoteTerminalTabs } from './remoteSessionTabsThunks';
import { loadReviewDiff } from './reviewThunks';
import { selectActiveSlotForSelection, selectEnvironmentExists } from './selectors';
import { isNewSessionSelection } from './sessionSelection';
import { setAutoStartPrompt } from './slices/autoStartPromptSlice';
import { setIdleStatus } from './slices/idleSlice';
import {
  setSelectedDiffPath,
  setSelectedReviewCommit,
  setSelectedReviewScope,
} from './slices/reviewSlice';
import { setSelected } from './slices/selectionSlice';
import {
  clearEnvOpening,
  markEnvOpening,
  resetEnvOpening,
  trackOpenSession,
} from './slices/sessionsSlice';
import { setTenantDashboard } from './slices/tenantDashboardSlice';
import { setSelectedSessionForEnv, setSessionId } from './slices/terminalSlice';
import { setTerminalCopyOutput, setTerminalCopyStatus } from './slices/terminalStatusSlice';
import type { TerminalTab, TerminalTabKind } from './state';
import type { AppThunk } from './store';
import { maybeRespawnDeadDefaultTab } from './tabRespawnThunks';
import { recordTab, rememberSelectedTab, removeTab } from './tabsThunks';
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
    const start = kind === 'local' ? StartLocalSession : StartAISession;
    try {
      const result = (await start(runSelection, 0, cols, rows)) as StartSessionResult;
      dispatch(recordTab(key, result.sessionId, result.slot ?? 0, kind, label));
    } catch {
      // Tool unavailable; future env opens will retry.
    }
  };

// Starts an ERun tab without flipping the active session; the bookkeeping
// is inlined rather than reusing registerOpenSessionResult, which would
// flip the active session and steal the user's current view.
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

// Repoints the remembered selection from the dead session to the respawned
// tab; otherwise restoreSelectedTabForEnv sees a stale id that matches no
// live tab, bails, and never surfaces the live PTY.
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

// The dead-tab respawn branch keeps the "stopped reconnecting … click the
// environment in the sidebar to retry" marker from being a false promise:
// AI/Local tabs register no openSelection, so their zombie tab lingers in
// tabsByEnv after exit and would otherwise never be replaced with a live PTY.
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
  async (dispatch) => {
    await dispatch(ensureLiveDefaultTab(key, runSelection, 'erun', 'ERun', cols, rows));
    await dispatch(ensureLiveDefaultTab(key, runSelection, 'local', 'Local', cols, rows));
    await dispatch(ensureLiveDefaultTab(key, runSelection, 'ai', 'AI', cols, rows));
  };

// Repoints the visible terminal at the new env's session so the previous
// env's output stops painting while the new env's slower StartSession is in
// flight — the renderer drops PTY writes that don't match the current sessionId.
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

// Records the session bookkeeping that must fire even after the user
// navigates away, so a later sidebar click reuses the session instead of
// double-spawning. The stale-selection path calls only this, not
// registerOpenSessionResult, so an abandoned long-running open never steals
// the terminal from the env the user has moved to.
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
    // Preserve the user's prior tab choice for this env across re-opens
    // (Nielsen #4: consistency / user control): only seed the selection
    // when nothing valid is remembered.
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
      dispatch(setSelectedReviewScope('current'));
      dispatch(setSelectedReviewCommit(''));
      dispatch(setSelectedDiffPath(''));
    }
    // setSelected is observed by selectionSyncMiddleware, which reconciles
    // state.terminal.sessionId with the new env's tabs in the same tick.
    // Every setSelected dispatch — including re-clicks where newKey ===
    // previousKey but the terminal happens to point at a stale session
    // from another env — flows through the listener, so no caller has to
    // remember to pair setSelected with a manual surface.
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
  (selection: UISelection): AppThunk<Promise<void>> =>
  async (dispatch, getState, extra) => {
    const controller = requireController(extra);
    dispatch(
      setTenantDashboard({ tenant: '', tab: 'users', loading: false, error: '', data: null }),
    );
    const runSelection = { ...selection };
    const key = selectionKey(runSelection);
    const previousSessionId = getState().terminal.sessionId;
    const previousKnownSessionId = getState().sessions.selectionToSessionId[key] ?? 0;
    // Capture the prior selection so a failed StartSession can roll the
    // sidebar back: prepareOpenSelection already moved it to the new env, and
    // without rollback the sidebar and terminal would show different envs.
    const previousSelected = getState().selection.selected;

    const verdict = await resolveAutoStartGate(selection, getState);
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

    const isCurrentSelection = createIsCurrentSelection(getState, selection);

    // The previous click's openSelection is still in flight; reset before the
    // new selection paints its spinner so the sidebar spinner does not linger
    // on the env the user has navigated away from.
    dispatch(resetEnvOpening());
    dispatch(prepareOpenSelection(selection, previousSessionId, previousKnownSessionId));
    controller.fitTerminal();
    const { cols, rows } = controller.terminalSize();

    dispatch(markEnvOpening({ tenant: selection.tenant, environment: selection.environment }));
    try {
      // Spawn (or respawn if dead) Local first so subsequent ERun/AI
      // spawns can log into it, and so the marker-promised sidebar-click
      // recovery actually swaps a zombie Local for a live PTY.
      await dispatch(ensureLiveDefaultTab(key, runSelection, 'local', 'Local', cols, rows));
      if (!isCurrentSelection()) {
        return;
      }
      // prepareOpenSelection cleared sessionId to 0 on an env change; fill it
      // back with the just-spawned Local so the user sees their new env's
      // terminal while ERun cold-starts.
      dispatch(surfaceEnvSession(key));

      if (!shouldSpawnERun) {
        // autoStart=never path: navigation completes with Local only. The
        // user can flip the policy from the manage-env dialog or click the
        // titlebar Play button to start the cloud context on demand, which
        // will surface "running" on the next idle poll and lets a follow-up
        // sidebar click spawn the ERun tab.
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
        dispatch(showTerminalMessage(readError(error)));
      }
      throw error;
    } finally {
      dispatch(clearEnvOpening({ tenant: selection.tenant, environment: selection.environment }));
    }
  };

// Returns a predicate that post-await dispatches poll to decide whether the
// user is still on this env or has navigated away. It reads getState()
// afresh each call so it tracks setSelected dispatches that fire between awaits.
function createIsCurrentSelection(
  getState: () => import('./store').RootState,
  selection: UISelection,
): () => boolean {
  return () => {
    const current = getState().selection.selected;
    if (current === null) {
      return false;
    }
    return current.tenant === selection.tenant && current.environment === selection.environment;
  };
}

// When the user has navigated away (isCurrentSelection() === false), the
// spawned session is recorded for later reuse but not promoted to the visible
// terminal — so a long-running cold EC2 open in env A no longer paints
// "Opening A..." over env B that the user has since clicked into.
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

    await dispatch(ensureDefaultEnvTabs(runSelection, key, cols, rows));
    // Fire-and-forget: rebuilding tabs for pod sessions another window
    // created must not block the open flow, and trackOpenSessionMetadata
    // records them safely even if the user has navigated away meanwhile.
    void dispatch(reattachRemoteTerminalTabs(runSelection, key, cols, rows));
    if (!isCurrentSelection()) {
      return;
    }
    dispatch(restoreSelectedTabForEnv(key));

    if (getState().layout.reviewOpen) {
      await dispatch(loadReviewDiff());
    }
    if (!isCurrentSelection()) {
      return;
    }
    controller.focusTerminalSoon();
    controller.queueTerminalResize();
  };

// activateLocalAfterCommand promotes the freshly-spawned Local session as
// the active tab once an init/deploy command finishes. Init does not
// spawn dependent tabs because the env config does not yet exist; the
// environment-initialized handler reopens the env once the backend
// confirms creation.
export const activateLocalAfterCommand =
  (selection: UISelection, result: StartSessionResult): AppThunk<Promise<void>> =>
  async (dispatch, getState, extra) => {
    const controller = requireController(extra);
    if (result.orchestrated) {
      // Agent-env deploy is a background build -> push -> deploy orchestration
      // with no foreground PTY: there is no Local tab to attach, and recording
      // one against the zero session id would create a dead tab. Status comes
      // from the activity queue instead, so this branch only drops the
      // transient "Deploying…" overlay before it lingers over a deploy this
      // thunk no longer owns.
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

// Visibility of system status (Nielsen #1) for `erun init` is provided by
// three persistent surfaces: the sidebar placeholder row, the activity
// drawer init entry, and the live `erun init` output in the Local tab.
// Setting state.terminalMessage would flash a busy overlay for ~150 ms
// inside the still-open modal and then be cleared by
// activateLocalAfterCommand before the user could register it — see
// erun-ui/AGENTS.md § "UX Impact Review Checklist" item 3.
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

// runDeploySessionSelection is the shared body for the deploy-family thunks:
// surface the deploy status, size the terminal, invoke the given Start*Session
// binding, then activate Local. Only the Wails method and the status message
// differ across the three.
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

// startDeploySelection installs an already-published version by reference — the
// Deploy button. It never builds.
export const startDeploySelection = (selection: UISelection): AppThunk<Promise<void>> =>
  runDeploySessionSelection(
    selection,
    StartDeploySession,
    `Deploying runtime for ${selection.tenant} / ${selection.environment}...`,
  );

// startInitialDeploySelection stands a freshly-created env up: build+push+deploy
// for a builds-here env, install-by-reference for a runtime env (the env-create
// flow's first deploy — the explicit act of producing the env's first version).
export const startInitialDeploySelection = (selection: UISelection): AppThunk<Promise<void>> =>
  runDeploySessionSelection(
    selection,
    StartInitialDeploySession,
    `Deploying runtime for ${selection.tenant} / ${selection.environment}...`,
  );

// startCreateVersionSelection is the explicit "create & deploy new version":
// build this env's working tree into a fresh version, push it, and deploy it —
// local-agent envs only.
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
    dispatch(showTerminalMessage(readError(error)));
  }
};

export const selectTerminalTab =
  (sessionId: number): AppThunk =>
  (dispatch, getState, extra) => {
    const controller = requireController(extra);
    if (sessionId <= 0) {
      return;
    }
    if (dispatch(maybeRespawnDeadDefaultTab(sessionId))) {
      return;
    }
    if (sessionId === getState().terminal.sessionId) {
      return;
    }
    dispatch(setSessionId(sessionId));
    dispatch(rememberSelectedTab(sessionId));
    const state = getState();
    const exitReason = state.sessions.exitReasons[sessionId] ?? '';
    if (exitReason) {
      dispatch(setTerminalCopyOutput(state.sessions.exitOutputs[sessionId] ?? ''));
      dispatch(setTerminalCopyStatus(''));
      dispatch(showTerminalMessage(exitReason));
    } else {
      dispatch(hideTerminalMessage());
    }
    controller.focusTerminalSoon();
    controller.queueTerminalResize();
  };

export const closeTerminalTab =
  (sessionId: number): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    if (sessionId <= 0) {
      return;
    }
    const state = getState();
    const selection = state.selection.selected;
    if (!selection) {
      return;
    }
    const runSelection = { ...selection };
    const key = selectionKey(runSelection);
    const tabs = state.terminal.tabsByEnv[key] ?? [];
    const target = tabs.find((tab) => tab.sessionId === sessionId);
    if (target && target.kind !== 'extra') {
      return;
    }
    try {
      await CloseSession(sessionId);
    } catch (error: unknown) {
      dispatch(showTerminalMessage(readError(error)));
      return;
    }
    const remaining = dispatch(removeTab(key, sessionId));
    if (getState().terminal.sessionId === sessionId) {
      const next = remaining[remaining.length - 1];
      if (next) {
        dispatch(selectTerminalTab(next.sessionId));
      } else {
        dispatch(setSessionId(0));
      }
    }
  };

// Re-exported here so existing session-thunks imports still resolve;
// closeEnvironment itself lives in ./closeEnvironmentThunks to keep this
// file under the max-lines cap.
export { closeEnvironment } from './closeEnvironmentThunks';
