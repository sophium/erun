import { FitAddon } from '@xterm/addon-fit';
import { Terminal, type IDisposable } from '@xterm/xterm';

import { environmentApi } from './api/environmentApi';
import { idleApi } from './api/idleApi';
import { kubernetesApi } from './api/kubernetesApi';
import { sessionApi } from './api/sessionApi';
import { stateApi } from './api/stateApi';
import { createControllerStateProxy } from './controllerStateProxy';
import { store } from './store';
import { thunkExtra } from './thunkExtra';
import { TerminalSessionRegistry } from './TerminalSessionRegistry';
import {
  ResizeSession,
  SendSessionInput,
  StartAISession,
  StartDeploySession,
  StartInitSession,
  StartLocalSession,
  StartSession,
} from '../../wailsjs/go/main/App';
import { EventsOn } from '../../wailsjs/runtime/runtime';
import { fileToBase64, decodeBase64Bytes, isTerminalPasteTarget, pastedImageFiles } from './clipboard';
import { readError } from './errors';
import {
  hideTerminalMessage,
  showNotification,
  showTerminalFailure,
  showTerminalMessage,
} from './notificationThunks';
import { selectTerminalTab as selectTerminalTabThunk } from './sessionThunks';
import { loadReviewDiff } from './reviewThunks';
import { visibleDiffPath } from './reviewDiffNavigation';
import { registerTerminalQueryResponseHandlers } from './terminalQueryResponses';
import type {
  AppStatusPayload,
  DebugSessionMode,
  EnvironmentInitializedPayload,
  MountElements,
  TerminalDataDisposable,
  TerminalExitSelections,
  TerminalWriteData,
} from './model';
import { isNewSessionSelection } from './sessionSelection';
import {
  MAX_DEBUG_HEIGHT,
  MAX_FILES_WIDTH,
  MIN_DEBUG_HEIGHT,
  MIN_FILES_WIDTH,
  MIN_REVIEW_WIDTH,
  computeMaxReviewWidth,
  type AppState,
  type TerminalTab,
  type TerminalTabKind,
} from './state';

const TAB_KIND_ORDER: Record<TerminalTabKind, number> = {
  local: 0,
  erun: 1,
  ai: 2,
  extra: 3,
};

function compareTabs(a: TerminalTab, b: TerminalTab): number {
  return TAB_KIND_ORDER[a.kind] - TAB_KIND_ORDER[b.kind] || a.slot - b.slot;
}

import { clamp } from './storage';
import {
  classifiedTerminalFailure,
  decodeDebugOutput,
  failedTerminalExitReason,
  formatDebugCommand,
  statusForTerminalOutput,
  successfulTerminalExitReason,
  terminalExitHasTrackedSelection,
  trimDebugOutput,
} from './terminalStatus';
import { failedTerminalOutput, filterTerminalDisplayData, rebuildTerminalDisplayBuffer } from './terminalBuffers';
import { normalizeDialogValue, normalizeVersionSuggestions, selectionKey } from './versionSuggestions';
import type {
  StartSessionResult,
  TerminalExitPayload,
  TerminalOutputPayload,
  UISelection,
} from '@/types';

const REVIEW_DIFF_REFRESH_INTERVAL_MS = 5000;

export class ERunUIController {
  readonly state: AppState = createControllerStateProxy(store);

  readonly sessions = new TerminalSessionRegistry();
  private pendingDebugHeader = '';
  private terminal: Terminal | null = null;
  private fitAddon: FitAddon | null = null;
  private terminalRoot: HTMLDivElement | null = null;
  private _terminalPane: HTMLElement | null = null;
  private _reviewView: HTMLElement | null = null;
  private reviewMain: HTMLDivElement | null = null;
  private _diffList: HTMLDivElement | null = null;
  private resizeObserver: ResizeObserver | null = null;
  private resizeTimer = 0;
  private reviewScrollFrame = 0;
  private idleStatusTimer = 0;
  private reviewDiffRefreshTimer = 0;
  private idleStatusRequest = 0;
  private bootStarted = false;
  private terminalDataDisposable: TerminalDataDisposable | null = null;
  private terminalQueryResponseDisposables: IDisposable[] = [];
  private terminalOutputOff: (() => void) | null = null;
  private terminalExitOff: (() => void) | null = null;
  private appStatusOff: (() => void) | null = null;
  private reconnectLineOff: (() => void) | null = null;
  private environmentInitializedOff: (() => void) | null = null;
  private environmentInitFailedOff: (() => void) | null = null;
  private environmentsChangedOff: (() => void) | null = null;
  private pasteHandler: ((event: ClipboardEvent) => void) | null = null;

  constructor() {
    thunkExtra.controller = this;
  }

  // Public ref accessors used by layoutThunks/reviewThunks. These point to the
  // live DOM elements registered in mount(); their nullability matches the
  // pre-mount state.
  get terminalPane(): HTMLElement | null {
    return this._terminalPane;
  }

  get reviewView(): HTMLElement | null {
    return this._reviewView;
  }

  get diffList(): HTMLDivElement | null {
    return this._diffList;
  }

  terminalSize(): { cols: number; rows: number } {
    return { cols: this.terminal?.cols || 80, rows: this.terminal?.rows || 24 };
  }

  fitTerminal(): void {
    this.fitAddon?.fit();
  }

  mount(elements: MountElements): () => void {
    this.terminalRoot = elements.terminalRoot;
    this._terminalPane = elements.terminalPane;
    this._reviewView = elements.reviewView;
    this.reviewMain = elements.reviewMain;
    this._diffList = elements.diffList;
    this.applyLayoutVars();

    if (this.terminal) {
      this.queueTerminalResize();
      return () => {};
    }

    this.terminal = new Terminal({
      allowProposedApi: false,
      cursorBlink: true,
      fontFamily: 'ui-monospace, SFMono-Regular, SF Mono, Menlo, Monaco, Consolas, Liberation Mono, monospace',
      fontSize: 13,
      lineHeight: 1.18,
      theme: {
        background: 'oklch(0 0 0)',
      },
    });
    this.fitAddon = new FitAddon();
    this.terminal.loadAddon(this.fitAddon);
    this.terminal.open(elements.terminalRoot);
    this.fitAddon.fit();

    this.terminalQueryResponseDisposables = registerTerminalQueryResponseHandlers(
      this.terminal,
      (data) => SendSessionInput(this.state.sessionId, data),
      (error) => store.dispatch(showTerminalMessage(readError(error))),
    );
    this.terminalDataDisposable = this.terminal.onData((data) => {
      SendSessionInput(this.state.sessionId, data).catch((error: unknown) => {
        store.dispatch(showTerminalMessage(readError(error)));
      });
    });

    this.pasteHandler = (event: ClipboardEvent) => {
      void this.handleTerminalPaste(event).catch((error: unknown) => {
        store.dispatch(showTerminalMessage(readError(error)));
      });
    };
    elements.terminalRoot.addEventListener('paste', this.pasteHandler, true);

    this.resizeObserver = new ResizeObserver(() => {
      this.queueTerminalResize();
    });
    this.resizeObserver.observe(elements.terminalRoot);
    window.addEventListener('resize', this.queueTerminalResize);

    this.terminalOutputOff = EventsOn('terminal-output', (payload: TerminalOutputPayload) => {
      this.handleTerminalOutput(payload);
    });
    this.terminalExitOff = EventsOn('terminal-exit', (payload: TerminalExitPayload) => {
      void this.handleTerminalExit(payload);
    });
    this.appStatusOff = EventsOn('app-status', (payload: AppStatusPayload) => {
      this.handleAppStatus(payload);
    });
    this.reconnectLineOff = EventsOn('mcp-reconnect-line', (line: string) => {
      this.handleReconnectLine(line);
    });
    this.environmentInitializedOff = EventsOn('environment-initialized', (payload: EnvironmentInitializedPayload) => {
      void this.handleEnvironmentInitialized(payload);
    });
    this.environmentInitFailedOff = EventsOn('environment-init-failed', (payload: EnvironmentInitializedPayload) => {
      this.handleEnvironmentInitFailed(payload);
    });
    this.environmentsChangedOff = EventsOn('environments-changed', () => {
      void this.reloadStateAfterEnvironmentChange();
    });

    if (!this.bootStarted) {
      this.bootStarted = true;
      void this.boot();
    }
    this.scheduleIdleStatusPoll(0);

    return () => this.unmountTerminal();
  }

  private unmountTerminal(): void {
    window.removeEventListener('resize', this.queueTerminalResize);
    this.resizeObserver?.disconnect();
    this.terminalDataDisposable?.dispose();
    for (const disposable of this.terminalQueryResponseDisposables) {
      disposable.dispose();
    }
    this.terminalOutputOff?.();
    this.terminalExitOff?.();
    this.appStatusOff?.();
    this.reconnectLineOff?.();
    this.environmentInitializedOff?.();
    this.environmentInitFailedOff?.();
    this.environmentsChangedOff?.();
    window.clearTimeout(this.idleStatusTimer);
    this.stopReviewDiffRefresh();
    if (this.pasteHandler && this.terminalRoot) {
      this.terminalRoot.removeEventListener('paste', this.pasteHandler, true);
    }
    this.terminalOutputOff = null;
    this.terminalExitOff = null;
    this.appStatusOff = null;
    this.reconnectLineOff = null;
    this.environmentInitializedOff = null;
    this.environmentInitFailedOff = null;
    this.environmentsChangedOff = null;
    this.terminalQueryResponseDisposables = [];
    this.terminal?.dispose();
    this.terminal = null;
    this.fitAddon = null;
  }

  setPendingDebugHeader(header: string): void {
    this.pendingDebugHeader = header;
    if (this.state.debugOpen) {
      this.state.debugOutput = header;
    }
  }

  applyPendingDebugHeader(sessionId: number): void {
    if (!this.pendingDebugHeader || sessionId <= 0) {
      this.pendingDebugHeader = '';
      return;
    }
    if (this.state.debugOpen) {
      this.sessions.setSessionDebug(sessionId, this.pendingDebugHeader);
    }
    this.pendingDebugHeader = '';
  }

  activeSessionDebug(sessionId: number): boolean {
    return sessionId > 0 && this.sessions.debugMode(sessionId) !== undefined;
  }

  syncDebugDisplay(): void {
    if (!this.state.debugOpen) {
      return;
    }
    this.state.debugOutput = this.sessions.sessionDebug(this.state.sessionId);
  }

  async openSelection(selection: UISelection): Promise<void> {
    this.state.tenantDashboard = { tenant: '', tab: 'users', loading: false, error: '', data: null };
    const runSelection = { ...selection, debug: this.state.debugOpen || undefined };
    const key = selectionKey(runSelection);
    const previousSessionId = this.state.sessionId;
    const previousKnownSessionId = this.sessions.knownSelectionSession(key);

    this.prepareOpenSelection(selection, runSelection, previousSessionId, previousKnownSessionId);
    this.fitAddon?.fit();
    const cols = this.terminal?.cols || 80;
    const rows = this.terminal?.rows || 24;

    // Spawn Local first so subsequent ERun/AI spawns can log into it.
    const tabs = this.state.tabsByEnv[key] || [];
    if (!tabs.some((tab) => tab.kind === 'local')) {
      await this.spawnDefaultTab(key, runSelection, 'local', 'Local', cols, rows);
    }

    const slot = this.activeSlotForSelection(runSelection);
    const result = (await StartSession(runSelection, slot, cols, rows)) as StartSessionResult;
    this.registerOpenSessionResult(key, result, runSelection, previousSessionId);
    this.showOpenSelectionStatus(result.sessionId, selection);

    await this.ensureDefaultEnvTabs(runSelection, key);
    this.restoreSelectedTabForEnv(key);

    if (this.state.reviewOpen) {
      await store.dispatch(loadReviewDiff());
    }
    this.focusTerminalSoon();
    this.queueTerminalResize();
  }

  async ensureDefaultEnvTabs(runSelection: UISelection, key: string): Promise<void> {
    const tabs = this.state.tabsByEnv[key] || [];
    const cols = this.terminal?.cols || 80;
    const rows = this.terminal?.rows || 24;
    if (!tabs.some((tab) => tab.kind === 'erun')) {
      await this.spawnERunTabPassive(key, runSelection, cols, rows);
    }
    if (!tabs.some((tab) => tab.kind === 'local')) {
      await this.spawnDefaultTab(key, runSelection, 'local', 'Local', cols, rows);
    }
    if (!tabs.some((tab) => tab.kind === 'ai')) {
      await this.spawnDefaultTab(key, runSelection, 'ai', 'AI', cols, rows);
    }
  }

  private async spawnERunTabPassive(key: string, runSelection: UISelection, cols: number, rows: number): Promise<void> {
    try {
      const result = (await StartSession(runSelection, 0, cols, rows)) as StartSessionResult;
      this.sessions.trackOpenSession(key, result.sessionId, runSelection);
      this.registerDebugSession(result.sessionId, runSelection, 'open');
      this.recordTab(key, result.sessionId, result.slot ?? 0, 'erun', 'ERun');
    } catch {
      // ERun failed to spawn; future env opens will retry.
    }
  }

  private async spawnDefaultTab(
    key: string,
    runSelection: UISelection,
    kind: 'local' | 'ai',
    label: string,
    cols: number,
    rows: number,
  ): Promise<void> {
    const start = kind === 'local' ? StartLocalSession : StartAISession;
    try {
      const result = (await start(runSelection, 0, cols, rows)) as StartSessionResult;
      this.recordTab(key, result.sessionId, result.slot ?? 0, kind, label);
    } catch {
      // Tool unavailable; future env opens will retry.
    }
  }

  rememberSelectedTabForCurrentEnv(sessionId: number): void {
    const selection = this.state.selected;
    if (!selection) {
      return;
    }
    const key = selectionKey({ ...selection, debug: this.state.debugOpen || undefined });
    this.state.selectedSessionByEnv = { ...this.state.selectedSessionByEnv, [key]: sessionId };
  }

  private restoreSelectedTabForEnv(key: string): void {
    const tabs = this.state.tabsByEnv[key] || [];
    const remembered = this.state.selectedSessionByEnv[key];
    if (!remembered || !tabs.some((tab) => tab.sessionId === remembered)) {
      return;
    }
    if (remembered === this.state.sessionId) {
      return;
    }
    store.dispatch(selectTerminalTabThunk(remembered));
  }

  private prepareOpenSelection(selection: UISelection, runSelection: UISelection, previousSessionId: number, previousKnownSessionId: number): void {
    if (selectionKey(selection) !== selectionKey(this.state.selected || { tenant: '', environment: '' })) {
      this.state.selectedReviewScope = 'current';
      this.state.selectedReviewCommit = '';
      this.state.selectedDiffPath = '';
    }
    this.state.selected = selection;
    this.state.idleStatus = null;
    if (!isNewSessionSelection(previousSessionId, previousKnownSessionId)) {
      return;
    }
    if (this.state.debugOpen) {
      this.setPendingDebugHeader(`$ ${formatDebugCommand(runSelection)}\n`);
    }
    this.state.terminalCopyOutput = '';
    this.state.terminalCopyStatus = '';
    store.dispatch(showTerminalMessage(`Opening ${selection.tenant} / ${selection.environment}...`, true));
  }

  registerOpenSessionResult(key: string, result: StartSessionResult, runSelection: UISelection, previousSessionId: number): void {
    this.sessions.trackOpenSession(key, result.sessionId, runSelection);
    this.registerDebugSession(result.sessionId, runSelection, 'open');
    this.applyPendingDebugHeader(result.sessionId);
    rebuildTerminalDisplayBuffer(this.sessions, result.sessionId);
    this.state.sessionId = result.sessionId;
    // Preserve the user's prior tab choice for this env across re-opens
    // (Nielsen heuristic #4: consistency / user control). Only seed
    // selectedSessionByEnv when nothing is remembered, or when the
    // remembered session no longer exists in the live tabs for this env.
    // restoreSelectedTabForEnv below switches the terminal back to the
    // remembered tab when one exists.
    const remembered = this.state.selectedSessionByEnv[key];
    const liveTabs = this.state.tabsByEnv[key] || [];
    const rememberedIsLive = remembered && liveTabs.some((tab) => tab.sessionId === remembered);
    if (!rememberedIsLive) {
      this.state.selectedSessionByEnv = { ...this.state.selectedSessionByEnv, [key]: result.sessionId };
    }
    this.syncDebugDisplay();
    const slot = result.slot ?? 0;
    const kind: TerminalTabKind = slot === 0 ? 'erun' : 'extra';
    const label = kind === 'erun' ? 'ERun' : `Terminal ${slot}`;
    this.recordTab(key, result.sessionId, slot, kind, label);
    if (result.sessionId !== previousSessionId) {
      this.resetTerminal();
      this.writeTerminalBuffer(this.sessions.displayBuffer(result.sessionId));
    }
  }

  private activeSlotForSelection(selection: UISelection): number {
    const tabs = this.state.tabsByEnv[selectionKey(selection)] || [];
    if (tabs.length === 0) {
      return 0;
    }
    const active = tabs.find((tab) => tab.sessionId === this.state.sessionId);
    return (active ?? tabs[0]).slot;
  }

  recordTab(key: string, sessionId: number, slot: number, kind: TerminalTabKind, label: string): void {
    const tabs = this.state.tabsByEnv[key] ? [...this.state.tabsByEnv[key]] : [];
    const existingIndex = tabs.findIndex((tab) => tab.kind === kind && tab.slot === slot);
    if (existingIndex >= 0) {
      tabs[existingIndex] = { sessionId, slot, kind, label };
    } else {
      tabs.push({ sessionId, slot, kind, label });
      tabs.sort(compareTabs);
    }
    this.state.tabsByEnv = { ...this.state.tabsByEnv, [key]: tabs };
  }

  removeTab(key: string, sessionId: number): TerminalTab[] {
    const tabs = this.state.tabsByEnv[key];
    if (!tabs || tabs.length === 0) {
      return [];
    }
    const remaining = tabs.filter((tab) => tab.sessionId !== sessionId);
    const next = { ...this.state.tabsByEnv };
    if (remaining.length === 0) {
      delete next[key];
    } else {
      next[key] = remaining;
    }
    this.state.tabsByEnv = next;
    if (this.state.selectedSessionByEnv[key] === sessionId) {
      const updated = { ...this.state.selectedSessionByEnv };
      delete updated[key];
      this.state.selectedSessionByEnv = updated;
    }
    return remaining;
  }

  private showOpenSelectionStatus(sessionId: number, selection: UISelection): void {
    const exitReason = this.sessions.exitReason(sessionId);
    if (exitReason) {
      this.state.terminalCopyOutput = this.sessions.exitOutput(sessionId);
      this.state.terminalCopyStatus = '';
      store.dispatch(showTerminalMessage(exitReason));
      return;
    }
    const buffer = this.sessions.displayBuffer(sessionId);
    if (buffer.length > 0) {
      store.dispatch(hideTerminalMessage());
      return;
    }
    store.dispatch(showTerminalMessage(`Opening ${selection.tenant} / ${selection.environment}...`, true));
  }

  appendDebugOutput(text: string, fromSessionId?: number): void {
    if (!this.state.debugOpen || !text) {
      return;
    }
    const target = fromSessionId !== undefined ? fromSessionId : this.state.sessionId;
    const next = trimDebugOutput(this.sessions.sessionDebug(target) + text);
    this.sessions.setSessionDebug(target, next);
    if (target === this.state.sessionId) {
      this.state.debugOutput = next;
    }
  }

  focusTerminalSoon(): void {
    window.setTimeout(() => {
      this.terminal?.focus();
      window.requestAnimationFrame(() => this.terminal?.focus());
      window.setTimeout(() => this.terminal?.focus(), 80);
    }, 0);
  }

  private scheduleIdleStatusPoll(delay = 1000): void {
    window.clearTimeout(this.idleStatusTimer);
    this.idleStatusTimer = window.setTimeout(() => {
      void this.refreshIdleStatus();
    }, delay);
  }

  async refreshIdleStatus(): Promise<void> {
    const selection = this.state.selected;
    const request = ++this.idleStatusRequest;
    if (!selection) {
      this.clearIdleStatus();
      this.scheduleIdleStatusPoll();
      return;
    }

    try {
      const status = await store
        .dispatch(idleApi.endpoints.getIdleStatus.initiate(selection, { forceRefetch: true }))
        .unwrap();
      if (this.isCurrentIdleStatusRequest(request, selection)) {
        this.state.idleStatus = status;
      }
    } catch {
      this.clearCurrentIdleStatusRequest(request);
    } finally {
      if (request === this.idleStatusRequest) {
        this.scheduleIdleStatusPoll();
      }
    }
  }

  private clearIdleStatus(): void {
    if (!this.state.idleStatus) {
      return;
    }
    this.state.idleStatus = null;
  }

  private clearCurrentIdleStatusRequest(request: number): void {
    if (request === this.idleStatusRequest) {
      this.clearIdleStatus();
    }
  }

  private isCurrentIdleStatusRequest(request: number, selection: UISelection): boolean {
    return request === this.idleStatusRequest && this.state.selected?.tenant === selection.tenant && this.state.selected.environment === selection.environment;
  }

  private async boot(): Promise<void> {
    try {
      store.dispatch(showTerminalMessage('Loading environments...', true));
      const loaded = await store
        .dispatch(stateApi.endpoints.getInitialState.initiate(undefined, { forceRefetch: true }))
        .unwrap();
      this.state.tenants = loaded.tenants || [];
      this.state.cloudProviders = loaded.cloudProviders || [];
      this.state.selected = loaded.selected || null;
      this.state.versionSuggestions = normalizeVersionSuggestions(loaded.versionSuggestions || []);
      this.selectLoadedKubernetesContexts(loaded.kubernetesContexts || []);
      if (loaded.message) {
        store.dispatch(showTerminalMessage(loaded.message));
        return;
      }

      if (this.state.selected) {
        await this.openSelection(this.state.selected);
        return;
      }

      store.dispatch(showTerminalMessage('Choose an environment from the left pane.'));
    } catch (error: unknown) {
      store.dispatch(showTerminalMessage(readError(error)));
    }
  }

  async startInitSelection(selection: UISelection): Promise<void> {
    // Visibility of system status (Nielsen #1) is provided by three
    // surfaces that persist for the full init duration: the sidebar
    // placeholder row (Sidebar.tsx EnvironmentRow placeholder branch),
    // the activity-drawer init entry registered when the backend's
    // trace handler observes `==> Initializing`, and the live `erun
    // init` output in the Local tab. Setting state.terminalMessage
    // would flash a busy overlay for ~150 ms inside the still-open
    // modal and is then cleared by activateLocalAfterCommand before
    // the user can register it — see erun-ui/AGENTS.md § "UX Impact
    // Review Checklist" item 3 (state-without-affordance).
    const runSelection = { ...selection, debug: this.state.debugOpen || undefined };
    this.state.selected = selection;
    this.state.terminalCopyOutput = '';
    this.state.terminalCopyStatus = '';
    this.fitAddon?.fit();
    const result = (await StartInitSession(runSelection, this.terminal?.cols || 80, this.terminal?.rows || 24)) as StartSessionResult;
    await this.activateLocalAfterCommand(selection, result);
  }

  async startDeploySelection(selection: UISelection): Promise<void> {
    const runSelection = { ...selection, debug: this.state.debugOpen || undefined };
    this.state.selected = selection;
    this.state.terminalCopyOutput = '';
    this.state.terminalCopyStatus = '';
    store.dispatch(showTerminalMessage(`Deploying runtime for ${selection.tenant} / ${selection.environment}...`, true));

    this.fitAddon?.fit();
    const result = (await StartDeploySession(runSelection, this.terminal?.cols || 80, this.terminal?.rows || 24)) as StartSessionResult;
    await this.activateLocalAfterCommand(selection, result);
  }

  async activateLocalAfterCommand(selection: UISelection, result: StartSessionResult): Promise<void> {
    const runSelection = { ...selection, debug: this.state.debugOpen || undefined };
    const key = selectionKey(runSelection);
    this.recordTab(key, result.sessionId, result.slot ?? 0, 'local', 'Local');
    // Only spawn dependent ERun/AI tabs when the env config already
    // exists. For `erun init` the env is created mid-command, so the
    // tabs would fail to spawn against missing config; the
    // environment-initialized event fires a second openSelection after
    // success, which spawns the tabs against the now-existing config.
    // See erun-ui/AGENTS.md § "Command Completion And State-Refresh
    // Wiring".
    if (this.environmentExists(selection.tenant, selection.environment)) {
      await this.ensureDefaultEnvTabs(runSelection, key);
    }
    this.state.sessionId = result.sessionId;
    this.state.selectedSessionByEnv = { ...this.state.selectedSessionByEnv, [key]: result.sessionId };
    rebuildTerminalDisplayBuffer(this.sessions, result.sessionId);
    this.resetTerminal();
    this.writeTerminalBuffer(this.sessions.displayBuffer(result.sessionId));
    store.dispatch(hideTerminalMessage());
    this.focusTerminalSoon();
    this.queueTerminalResize();
  }

  async reloadStateAfterEnvironmentChange(): Promise<void> {
    try {
      const loaded = await store
        .dispatch(stateApi.endpoints.getInitialState.initiate(undefined, { forceRefetch: true }))
        .unwrap();
      this.state.tenants = loaded.tenants || [];
      this.state.cloudProviders = loaded.cloudProviders || this.state.cloudProviders;
      this.state.versionSuggestions = normalizeVersionSuggestions(loaded.versionSuggestions || this.state.versionSuggestions);
      this.selectLoadedKubernetesContexts(loaded.kubernetesContexts || []);
    } catch {
    }
  }

  private environmentExists(tenant: string, environment: string): boolean {
    return Boolean(
      this.state.tenants
        .find((entry) => entry.name === tenant)
        ?.environments.some((env) => env.name === environment),
    );
  }

  async refreshKubernetesContexts(): Promise<void> {
    try {
      const result = await store
        .dispatch(kubernetesApi.endpoints.getKubernetesContexts.initiate())
        .unwrap();
      const contexts = result.map((context) => context.trim()).filter(Boolean);
      if (!this.state.environmentDialog.open || this.state.environmentDialog.actionMode !== 'init') {
        return;
      }
      this.state.environmentDialog = {
        ...this.state.environmentDialog,
        kubernetesContexts: contexts,
        kubernetesContext: this.resolveDialogKubernetesContext(contexts),
        kubernetesContextsLoading: false,
      };
      void this.refreshEnvironmentRuntimeResources(this.state.environmentDialog.kubernetesContext);
    } catch (error) {
      if (!this.state.environmentDialog.open || this.state.environmentDialog.actionMode !== 'init') {
        return;
      }
      this.state.environmentDialog = {
        ...this.state.environmentDialog,
        kubernetesContexts: [],
        kubernetesContext: '',
        kubernetesContextsLoading: false,
        error: readError(error),
      };
    }
  }

  private resolveDialogKubernetesContext(contexts: string[]): string {
    const current = normalizeDialogValue(this.state.environmentDialog.kubernetesContext);
    if (current && contexts.includes(current)) {
      return current;
    }
    return contexts[0] || '';
  }

  private selectLoadedKubernetesContexts(contexts: string[]): void {
    if (!this.state.environmentDialog.open || this.state.environmentDialog.actionMode !== 'init') {
      return;
    }
    const normalized = contexts.map((context) => context.trim()).filter(Boolean);
    this.state.environmentDialog = {
      ...this.state.environmentDialog,
      kubernetesContexts: normalized,
      kubernetesContext: this.resolveDialogKubernetesContext(normalized),
      kubernetesContextsLoading: false,
    };
    void this.refreshEnvironmentRuntimeResources(this.state.environmentDialog.kubernetesContext);
  }

  // refreshEnvironmentRuntimeResources is a private helper invoked when the
  // dialog's kubernetesContext changes or when the context list resolves.
  // The environmentDialogThunks own the analogous user-driven refresh
  // (kubernetesContext field changes), but this internal flow stays on the
  // controller because boot() + refreshKubernetesContexts() drive it.
  private async refreshEnvironmentRuntimeResources(kubernetesContext: string): Promise<void> {
    const context = normalizeDialogValue(kubernetesContext);
    if (!this.state.environmentDialog.open || this.state.environmentDialog.actionMode !== 'init' || !context) {
      return;
    }
    this.state.environmentDialog = {
      ...this.state.environmentDialog,
      resourceStatusLoading: true,
      resourceStatus: null,
    };
    try {
      const status = await store
        .dispatch(
          environmentApi.endpoints.getRuntimeResourceStatus.initiate(
            {
              kubernetesContext: context,
              tenant: normalizeDialogValue(this.state.environmentDialog.tenant),
              environment: normalizeDialogValue(this.state.environmentDialog.environment),
            },
            { forceRefetch: true },
          ),
        )
        .unwrap();
      if (!this.state.environmentDialog.open) {
        return;
      }
      this.state.environmentDialog = {
        ...this.state.environmentDialog,
        resourceStatus: status,
        resourceStatusLoading: false,
      };
    } catch (error) {
      if (!this.state.environmentDialog.open) {
        return;
      }
      this.state.environmentDialog = {
        ...this.state.environmentDialog,
        resourceStatus: {
          kubernetesContext: context,
          available: false,
          message: readError(error),
          cpu: { total: 0, used: 0, free: 0, unit: 'cores', formatted: '' },
          memory: { total: 0, used: 0, free: 0, unit: 'GiB', formatted: '' },
        },
        resourceStatusLoading: false,
      };
    }
  }

  resolveManageRuntimeImage(version: string): string {
    if (this.state.manageDialog.versionImage) {
      return this.state.manageDialog.versionImage;
    }
    const suggestion = this.state.versionSuggestions.find((value) => value.version === version);
    return suggestion?.image || '';
  }

  resetTerminal(): void {
    this.terminal?.reset();
    this.terminal?.clear();
  }

  private handleAppStatus(payload: AppStatusPayload): void {
    const message = String(payload?.message || '').trim();
    if (!message) {
      return;
    }
    this.appendDebugOutput(`[status] ${message}\n`);
    store.dispatch(showTerminalMessage(message, payload.busy === true));
  }

  // Fires when the backend's PTY trace handler observes
  // `==> Initialized <tenant>/<env>` from a piped `erun init` command,
  // or when the config-file watcher detects a new env. Reload state so
  // the new env appears in the sidebar, surface a success toast
  // (Nielsen #1 visibility of system status), then open the selection
  // so the ERun and AI tabs spawn against the now-existing config.
  // See erun-ui/AGENTS.md § "Command Completion And State-Refresh
  // Wiring".
  private async handleEnvironmentInitialized(payload: EnvironmentInitializedPayload): Promise<void> {
    const tenant = String(payload?.tenant || '').trim();
    const environment = String(payload?.environment || '').trim();
    if (!tenant || !environment) {
      return;
    }
    await this.reloadStateAfterEnvironmentChange();
    if (!this.environmentExists(tenant, environment)) {
      return;
    }
    store.dispatch(showNotification('success', `Created ${tenant} / ${environment}.`));
    try {
      await this.openSelection({ tenant, environment });
    } catch (error) {
      store.dispatch(showTerminalMessage(readError(error)));
    }
  }

  // Fires when the backend's PTY trace handler observes
  // `==> Initialization failed <tenant>/<env>`. Surfaces an error toast
  // (Nielsen #1 + #9) and reverts the optimistic state.selected so the
  // sidebar's "creating ..." placeholder row disappears.
  private handleEnvironmentInitFailed(payload: EnvironmentInitializedPayload): void {
    const tenant = String(payload?.tenant || '').trim();
    const environment = String(payload?.environment || '').trim();
    if (!tenant || !environment) {
      return;
    }
    store.dispatch(showNotification('error', `Failed to create ${tenant} / ${environment}. See the Local tab and the activity drawer for details.`));
    if (this.selectedIsPendingFor(tenant, environment)) {
      this.state.selected = null;
    }
  }

  private selectedIsPendingFor(tenant: string, environment: string): boolean {
    const selected = this.state.selected;
    if (!selected || selected.tenant !== tenant || selected.environment !== environment) {
      return false;
    }
    return !this.environmentExists(tenant, environment);
  }

  private handleTerminalOutput(payload: TerminalOutputPayload): void {
    if (!payload) {
      return;
    }
    const data = decodeBase64Bytes(payload.data);
    this.sessions.appendSessionBuffer(payload.sessionId, data);
    const debugOutput = decodeDebugOutput(data);
    this.appendDebugOutput(debugOutput, payload.sessionId);
    this.updateOpenStatusFromOutput(payload.sessionId, debugOutput);
    const displayData = filterTerminalDisplayData(this.sessions, payload.sessionId, data);
    if (displayData) {
      this.sessions.appendDisplayBuffer(payload.sessionId, displayData);
    }
    if (payload.sessionId !== this.state.sessionId) {
      return;
    }
    if (!displayData) {
      return;
    }
    if (this.state.terminalMessage && !this.state.terminalCopyOutput) {
      store.dispatch(hideTerminalMessage());
    }
    this.terminal?.write(displayData);
  }

  private async handleTerminalExit(payload: TerminalExitPayload): Promise<void> {
    if (!payload) {
      return;
    }
    const selections = this.takeTerminalExitSelections(payload.sessionId);
    const reason = this.terminalExitReason(payload, selections);
    const failedOutput = this.recordTerminalExit(payload, reason, selections);
    this.dropExitedSessionFromTabs(payload.sessionId, selections.openSelection);
    this.recordDoctorOutcome(payload, selections);

    if (selections.sshdInitSelection) {
      await this.reloadStateAfterEnvironmentChange();
    }
    if (payload.sessionId !== this.state.sessionId) {
      return;
    }
    if (await this.handleSuccessfulTerminalExit(payload, reason, selections)) {
      return;
    }
    if (payload.reason && terminalExitHasTrackedSelection(selections)) {
      const failure = classifiedTerminalFailure(payload.reason, reason, failedOutput, selections.openSelection);
      store.dispatch(showTerminalFailure(failure.message, failure.detail, failedOutput, failure.action, failure.retrySelection));
      return;
    }
    store.dispatch(showTerminalMessage(reason));
  }

  private recordDoctorOutcome(payload: TerminalExitPayload, selections: TerminalExitSelections): void {
    const selection = selections.doctorSelection;
    if (!selection) {
      return;
    }
    const key = selectionKey(selection);
    const reason = (payload.reason || '').trim();
    this.state.lastDoctorBySelection = {
      ...this.state.lastDoctorBySelection,
      [key]: {
        ranAt: Date.now(),
        success: !reason,
        message: reason,
      },
    };
  }

  private takeTerminalExitSelections(sessionId: number): TerminalExitSelections {
    return this.sessions.takeExitSelections(sessionId);
  }

  private dropExitedSessionFromTabs(sessionId: number, openSelection: UISelection | undefined): void {
    if (!openSelection) {
      return;
    }
    const key = selectionKey(openSelection);
    const remaining = this.removeTab(key, sessionId);
    if (this.state.sessionId !== sessionId) {
      return;
    }
    const next = remaining[remaining.length - 1];
    if (next) {
      store.dispatch(selectTerminalTabThunk(next.sessionId));
    }
  }

  private recordTerminalExit(payload: TerminalExitPayload, reason: string, selections: TerminalExitSelections): string {
    this.sessions.recordExitReason(payload.sessionId, reason);
    if (!payload.reason || !terminalExitHasTrackedSelection(selections)) {
      return '';
    }
    const failedOutput = failedTerminalOutput(this.sessions, payload.sessionId, reason);
    if (failedOutput) {
      this.sessions.recordExitOutput(payload.sessionId, failedOutput);
    }
    return failedOutput;
  }

  private async handleSuccessfulTerminalExit(payload: TerminalExitPayload, reason: string, selections: TerminalExitSelections): Promise<boolean> {
    if (payload.reason) {
      return false;
    }
    if (selections.sshdInitSelection) {
      store.dispatch(showTerminalMessage(reason));
      return true;
    }
    return false;
  }

  private terminalExitReason(payload: TerminalExitPayload, selections: TerminalExitSelections): string {
    if (payload.reason) {
      return failedTerminalExitReason(payload.reason, selections);
    }
    return successfulTerminalExitReason(selections);
  }

  private updateOpenStatusFromOutput(sessionId: number, output: string): void {
    if (!output || !this.sessions.isOpenSession(sessionId) || this.state.terminalCopyOutput) {
      return;
    }
    const status = statusForTerminalOutput(output);
    if (!status) {
      return;
    }
    store.dispatch(showTerminalMessage(status, true));
  }

  // handleReconnectLine appends a status line from the reconnect PTY into the
  // reconnect dialog while it is running. Kept on the controller because it
  // wires the EventsOn('mcp-reconnect-line') subscription.
  private handleReconnectLine(line: string): void {
    const trimmed = (line || '').trim();
    if (!trimmed) {
      return;
    }
    if (this.state.reconnect.status !== 'running') {
      return;
    }
    this.state.reconnect = { ...this.state.reconnect, lastLine: trimmed };
  }

  layoutCallbacks(): {
    applyLayoutVars: () => void;
    focusTerminalSoon: () => void;
    queueTerminalResize: () => void;
  } {
    return {
      applyLayoutVars: () => this.applyLayoutVars(),
      focusTerminalSoon: () => this.focusTerminalSoon(),
      queueTerminalResize: this.queueTerminalResize,
    };
  }

  applyLayoutVars(): void {
    const root = document.documentElement;
    root.style.setProperty('--sidebar-width', `${this.state.sidebarHidden ? 0 : this.state.sidebarWidth}px`);
    root.style.setProperty('--review-width', `${this.clampedReviewWidth()}px`);
    root.style.setProperty('--files-width', `${this.clampedFilesWidth()}px`);
    root.style.setProperty('--debug-height', `${this.clampedDebugHeight()}px`);
  }

  private clampedReviewWidth(): number {
    const effectiveSidebar = this.state.sidebarHidden ? 0 : this.state.sidebarWidth;
    const maxWidth = computeMaxReviewWidth(window.innerWidth, effectiveSidebar);
    return clamp(this.state.reviewWidth, MIN_REVIEW_WIDTH, maxWidth);
  }

  private clampedFilesWidth(): number {
    const reviewWidth = this._reviewView?.getBoundingClientRect().width || this.state.reviewWidth;
    const maxFilesForReview = reviewWidth > 0 ? reviewWidth - 260 : MAX_FILES_WIDTH;
    return clamp(this.state.filesWidth, MIN_FILES_WIDTH, Math.max(MIN_FILES_WIDTH, Math.min(MAX_FILES_WIDTH, maxFilesForReview)));
  }

  private clampedDebugHeight(): number {
    const paneHeight = this._terminalPane?.getBoundingClientRect().height || 0;
    const maxDebugForPane = paneHeight > 0 ? paneHeight - 120 : MAX_DEBUG_HEIGHT;
    return clamp(this.state.debugHeight, MIN_DEBUG_HEIGHT, Math.max(MIN_DEBUG_HEIGHT, Math.min(MAX_DEBUG_HEIGHT, maxDebugForPane)));
  }

  queueTerminalResize = (): void => {
    window.clearTimeout(this.resizeTimer);
    this.resizeTimer = window.setTimeout(() => {
      this.applyLayoutVars();
      this.fitAddon?.fit();
      if (this.state.sessionId > 0 && this.terminal) {
        ResizeSession(this.state.sessionId, this.terminal.cols, this.terminal.rows).catch(() => {
        });
      }
    }, 40);
  };

  queueVisibleDiffSelectionUpdate(): void {
    if (this.reviewScrollFrame > 0) {
      return;
    }
    this.reviewScrollFrame = window.requestAnimationFrame(() => {
      this.reviewScrollFrame = 0;
      this.updateSelectedDiffPathFromScroll();
    });
  }

  private updateSelectedDiffPathFromScroll(): void {
    const path = visibleDiffPath(this._diffList, this.reviewMain);
    if (!path || path === this.state.selectedDiffPath) {
      return;
    }
    this.state.selectedDiffPath = path;
  }

  // Review-diff refresh timer accessors. reviewThunks owns the polling logic
  // but the timer field stays here so unmountTerminal() can clear it.
  stopReviewDiffRefresh(): void {
    window.clearTimeout(this.reviewDiffRefreshTimer);
    this.reviewDiffRefreshTimer = 0;
  }

  cancelReviewDiffRefresh(): void {
    window.clearTimeout(this.reviewDiffRefreshTimer);
    this.reviewDiffRefreshTimer = 0;
  }

  scheduleReviewDiffRefreshTimer(callback: () => void, delay: number = REVIEW_DIFF_REFRESH_INTERVAL_MS): void {
    window.clearTimeout(this.reviewDiffRefreshTimer);
    this.reviewDiffRefreshTimer = window.setTimeout(callback, delay);
  }

  private async handleTerminalPaste(event: ClipboardEvent): Promise<void> {
    if (!this.terminalRoot || !isTerminalPasteTarget(this.terminalRoot, event.target)) {
      return;
    }

    const images = pastedImageFiles(event);
    if (images.length === 0) {
      return;
    }

    event.preventDefault();
    const sessionId = this.state.sessionId;
    if (sessionId <= 0) {
      return;
    }
    const paths: string[] = [];
    for (const image of images) {
      const result = await store
        .dispatch(
          sessionApi.endpoints.savePastedImage.initiate({
            sessionId,
            payload: {
              data: await fileToBase64(image),
              mimeType: image.type,
              name: image.name,
            },
          }),
        )
        .unwrap();
      if (result.path) {
        paths.push(result.path);
      }
    }
    if (paths.length === 0) {
      return;
    }
    await SendSessionInput(sessionId, `${paths.join(' ')} `);
    this.focusTerminalSoon();
  }

  registerDebugSession(sessionId: number, selection: UISelection, mode: DebugSessionMode): void {
    this.sessions.registerDebugSession(sessionId, selection, mode);
  }

  writeTerminalBuffer(chunks: TerminalWriteData[]): void {
    for (const chunk of chunks) {
      this.terminal?.write(chunk);
    }
  }

}
