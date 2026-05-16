import type * as React from 'react';
import { FitAddon } from '@xterm/addon-fit';
import { Terminal, type IDisposable } from '@xterm/xterm';

import { cloudApi } from './api/cloudApi';
import { environmentApi } from './api/environmentApi';
import { idleApi } from './api/idleApi';
import { kubernetesApi } from './api/kubernetesApi';
import { reviewApi } from './api/reviewApi';
import { sessionApi } from './api/sessionApi';
import { stateApi } from './api/stateApi';
import { tenantApi } from './api/tenantApi';
import { createControllerStateProxy } from './controllerStateProxy';
import { toggleTenantCollapsed } from './slices/sidebarSlice';
import { toggleDiffDirCollapsed } from './slices/reviewSlice';
import { store } from './store';
import { TerminalSessionRegistry } from './TerminalSessionRegistry';
import {
  CloseSession,
  ResizeSession,
  SendSessionInput,
  StartAISession,
  StartDeploySession,
  StartInitSession,
  StartLocalSession,
  StartSession,
} from '../../wailsjs/go/main/App';
import { ClipboardSetText, EventsOn, WindowToggleMaximise } from '../../wailsjs/runtime/runtime';
import { fileToBase64, decodeBase64Bytes, isTerminalPasteTarget, pastedImageFiles } from './clipboard';
import { replaceCloudProvider } from './cloudContextState';
import { chooseSelectedDiffPath } from './diffUtils';
import { isMcpUnreachableMessage, stripMcpUnreachableMarker } from './reconnectCopy';
import {
  normalizedEnvironmentDialogValues,
  rememberEnvironmentDialogSelection,
  validEnvironmentDialogValues,
} from './environmentDialogState';
import { GlobalConfigWorkflow } from './globalConfigWorkflow';
import { ManageEnvironmentWorkflow } from './manageEnvironmentWorkflow';
import {
  setDebugOpen as applyDebugOpen,
  setFilesOpen as applyFilesOpen,
  startDebugResize as startDebugPanelResize,
  startFilesResize as startFilesPanelResize,
  startReviewResize as startReviewPanelResize,
  startSidebarResize as startSidebarPanelResize,
  toggleReview as toggleReviewPanel,
  toggleSidebar as toggleSidebarPanel,
} from './layoutActions';
import { readError } from './errors';
import { runtimePodConfigToKubernetes, runtimeResourceLimitMessage } from './runtimeResources';
import { scrollSelectedDiffIntoView, visibleDiffPath } from './reviewDiffNavigation';
import { registerTerminalQueryResponseHandlers } from './terminalQueryResponses';
import type {
  AppStatusPayload,
  DebugSessionMode,
  EnvironmentInitializedPayload,
  IDEKind,
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
  defaultEnvironmentDialog,
  defaultTenantDashboard,
  defaultTenantDialog,
  type AppState,
  type EnvironmentDialogState,
  type GlobalConfigDialogState,
  type ManageDialogState,
  type TenantDashboardTab,
  type TenantDialogState,
  type TerminalStatusAction,
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

import {
  clamp,
  loadSavedPastContainerRegistries,
} from './storage';
import {
  classifiedTerminalFailure,
  debugOutputBlock,
  decodeDebugOutput,
  failedTerminalExitReason,
  formatDebugCommand,
  formatIDECommand,
  ideLabel,
  ideOpenFailure,
  statusForTerminalOutput,
  successfulTerminalExitReason,
  terminalExitHasTrackedSelection,
  trimDebugOutput,
} from './terminalStatus';
import { failedTerminalOutput, filterTerminalDisplayData, rebuildTerminalDisplayBuffer } from './terminalBuffers';
import { normalizeDialogValue, normalizeVersionSuggestions, selectionKey } from './versionSuggestions';
import type {
  ManageTab,
  StartSessionResult,
  TerminalExitPayload,
  TerminalOutputPayload,
  UICloudContextInitInput,
  UICloudProviderStatus,
  UIERunConfig,
  UIEnvironmentConfig,
  UISelection,
  UITenant,
  UITenantDashboardInput,
  UITenantConfig,
  UIVersionSuggestion,
} from '@/types';

const REVIEW_DIFF_REFRESH_INTERVAL_MS = 5000;

export class ERunUIController {
  readonly state: AppState = createControllerStateProxy(store);

  // Components subscribe to Redux directly via useAppSelector. The
  // controller's subscribe stays as a no-op stub only to satisfy any
  // legacy caller; the body is empty because react-redux owns
  // notification now.
  private readonly sessions = new TerminalSessionRegistry();
  private pendingDebugHeader = '';
  private terminal: Terminal | null = null;
  private fitAddon: FitAddon | null = null;
  private terminalRoot: HTMLDivElement | null = null;
  private terminalPane: HTMLElement | null = null;
  private reviewView: HTMLElement | null = null;
  private reviewMain: HTMLDivElement | null = null;
  private diffList: HTMLDivElement | null = null;
  private resizeObserver: ResizeObserver | null = null;
  private resizeTimer = 0;
  private reviewScrollFrame = 0;
  private versionSuggestionTimer = 0;
  private notificationTimer = 0;
  private terminalCopyStatusTimer = 0;
  private idleStatusTimer = 0;
  private reviewDiffRefreshTimer = 0;
  private reviewDiffRequest = 0;
  private idleStatusRequest = 0;
  private versionSuggestionRequest = 0;
  private environmentResourceStatusRequest = 0;
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
  private terminalStatusRetrySelection: UISelection | null = null;
  private readonly globalConfig = new GlobalConfigWorkflow({
    state: this.state,
    sessions: this.sessions,
    terminalSize: () => ({ cols: this.terminal?.cols || 80, rows: this.terminal?.rows || 24 }),
    fitTerminal: () => this.fitAddon?.fit(),
    resetTerminal: () => this.resetTerminal(),
    focusTerminalSoon: () => this.focusTerminalSoon(),
    queueTerminalResize: () => this.queueTerminalResize(),
    openSelection: (selection) => this.openSelection(selection),
    refreshIdleStatus: () => { void this.refreshIdleStatus(); },
    refreshKubernetesContexts: () => { void this.refreshKubernetesContexts(); },
    hideTerminalMessage: () => this.hideTerminalMessage(),
    showNotification: (kind, message) => this.showNotification(kind, message),
    showTerminalMessage: (message, busy) => this.showTerminalMessage(message, busy),
  });
  private readonly manageEnvironment = new ManageEnvironmentWorkflow({
    state: this.state,
    sessions: this.sessions,
    terminalSize: () => ({ cols: this.terminal?.cols || 80, rows: this.terminal?.rows || 24 }),
    fitTerminal: () => this.fitAddon?.fit(),
    resetTerminal: () => this.resetTerminal(),
    focusTerminalSoon: () => this.focusTerminalSoon(),
    queueTerminalResize: () => this.queueTerminalResize(),
    refreshKubernetesContexts: () => { void this.refreshKubernetesContexts(); },
    reloadStateAfterEnvironmentChange: () => this.reloadStateAfterEnvironmentChange(),
    resolveRuntimeImage: (version) => this.resolveManageRuntimeImage(version),
    startDeploySelection: (selection) => this.startDeploySelection(selection),
    activateLocalAfterCommand: (selection, result) => this.activateLocalAfterCommand(selection, result),
    showNotification: (kind, message) => this.showNotification(kind, message),
    showTerminalMessage: (message, busy) => this.showTerminalMessage(message, busy),
    setPendingDebugHeader: (header) => this.setPendingDebugHeader(header),
    applyPendingDebugHeader: (sessionId) => this.applyPendingDebugHeader(sessionId),
    syncDebugDisplay: () => this.syncDebugDisplay(),
  });

  subscribe = (_subscriber: () => void): (() => void) => {
    return () => {
      /* no-op: components subscribe to Redux directly via useAppSelector. */
    };
  };

  mount(elements: MountElements): () => void {
    this.terminalRoot = elements.terminalRoot;
    this.terminalPane = elements.terminalPane;
    this.reviewView = elements.reviewView;
    this.reviewMain = elements.reviewMain;
    this.diffList = elements.diffList;
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
      (error) => this.showTerminalMessage(readError(error)),
    );
    this.terminalDataDisposable = this.terminal.onData((data) => {
      SendSessionInput(this.state.sessionId, data).catch((error: unknown) => {
        this.showTerminalMessage(readError(error));
      });
    });

    this.pasteHandler = (event: ClipboardEvent) => {
      void this.handleTerminalPaste(event).catch((error: unknown) => {
        this.showTerminalMessage(readError(error));
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
    window.clearTimeout(this.notificationTimer);
    window.clearTimeout(this.terminalCopyStatusTimer);
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

  toggleSidebar(): void {
    toggleSidebarPanel(this.state, this.layoutCallbacks());
  }

  startSidebarResize(event: React.MouseEvent<HTMLElement>): void {
    startSidebarPanelResize(this.state, event, () => this.applyLayoutVars());
  }

  startReviewResize(event: React.MouseEvent<HTMLElement>): void {
    startReviewPanelResize(this.state, event, this.terminalPane, this.layoutCallbacks());
  }

  startFilesResize(event: React.MouseEvent<HTMLElement>): void {
    startFilesPanelResize(this.state, event, this.reviewView, () => this.applyLayoutVars());
  }

  startDebugResize(event: React.MouseEvent<HTMLElement>): void {
    startDebugPanelResize(this.state, event, this.terminalPane, this.layoutCallbacks());
  }

  toggleReview(): void {
    toggleReviewPanel(this.state, { ...this.layoutCallbacks(), loadReviewDiff: () => { void this.loadReviewDiff(); } });
    if (!this.state.reviewOpen) {
      this.stopReviewDiffRefresh();
    }
  }

  setFilesOpen(open: boolean, persist = true): void {
    applyFilesOpen(this.state, open, persist, () => this.applyLayoutVars());
  }

  setDebugOpen(open: boolean): void {
    applyDebugOpen(this.state, open, this.queueTerminalResize);
  }

  clearDebugOutput(): void {
    this.state.debugOutput = '';
    this.sessions.clearSessionDebug(this.state.sessionId);
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

  toggleTenant(tenant: string): void {
    store.dispatch(toggleTenantCollapsed(tenant));
  }

  openTenantDashboard(tenant: string): void {
    tenant = tenant.trim();
    if (!tenant) {
      return;
    }
    this.state.selected = null;
    this.state.idleStatus = null;
    this.state.tenantDashboard = {
      tenant,
      tab: this.state.tenantDashboard.tenant === tenant ? this.state.tenantDashboard.tab : 'users',
      loading: true,
      error: '',
      data: null,
    };
    this.state.reviewOpen = false;
    this.showTerminalMessage('');
    void this.loadTenantDashboard(tenant);
  }

  setTenantDashboardTab(tab: TenantDashboardTab): void {
    this.state.tenantDashboard = {
      ...this.state.tenantDashboard,
      tab,
    };
  }

  async loadTenantDashboard(tenant = this.state.tenantDashboard.tenant): Promise<void> {
    tenant = tenant.trim();
    if (!tenant || this.state.tenantDashboard.tenant !== tenant) {
      return;
    }
    const tenantState = this.state.tenants.find((candidate) => candidate.name === tenant);
    const input = tenantDashboardInput(tenantState);
    if (!input) {
      this.state.tenantDashboard = {
        ...this.state.tenantDashboard,
        loading: false,
        error: 'Tenant dashboard requires an API URL and a primary cloud alias.',
        data: null,
      };
      return;
    }
    this.state.tenantDashboard = { ...this.state.tenantDashboard, loading: true, error: '' };
    try {
      const loadedData = await store
        .dispatch(tenantApi.endpoints.getTenantDashboard.initiate(input))
        .unwrap();
      if (this.state.tenantDashboard.tenant !== tenant) {
        return;
      }
      const data = { ...loadedData, environment: loadedData.environment || input.environment };
      this.state.tenantDashboard = { ...this.state.tenantDashboard, loading: false, error: '', data };
    } catch (error) {
      if (this.state.tenantDashboard.tenant !== tenant) {
        return;
      }
      this.state.tenantDashboard = { ...this.state.tenantDashboard, loading: false, error: readError(error), data: null };
    }
  }

  async refreshTenantDashboard(): Promise<void> {
    const tenant = this.state.tenantDashboard.tenant;
    await this.loadTenantDashboard(tenant);
    if (this.state.tenantDashboard.tenant === tenant && !this.state.tenantDashboard.error) {
      this.showNotification('success', 'Dashboard refreshed.');
    }
  }

  async openSelection(selection: UISelection): Promise<void> {
    this.state.tenantDashboard = defaultTenantDashboard();
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
      await this.loadReviewDiff();
    }
    this.focusTerminalSoon();
    this.queueTerminalResize();
  }

  private async ensureDefaultEnvTabs(runSelection: UISelection, key: string): Promise<void> {
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

  private rememberSelectedTabForCurrentEnv(sessionId: number): void {
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
    this.selectTerminalTab(remembered);
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
    this.showTerminalMessage(`Opening ${selection.tenant} / ${selection.environment}...`, true);
  }

  private registerOpenSessionResult(key: string, result: StartSessionResult, runSelection: UISelection, previousSessionId: number): void {
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

  private recordTab(key: string, sessionId: number, slot: number, kind: TerminalTabKind, label: string): void {
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

  private removeTab(key: string, sessionId: number): TerminalTab[] {
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

  async addTerminalTab(): Promise<void> {
    const selection = this.state.selected;
    if (!selection) {
      return;
    }
    const runSelection = { ...selection, debug: this.state.debugOpen || undefined };
    const key = selectionKey(runSelection);
    const tabs = this.state.tabsByEnv[key] || [];
    const nextSlot = tabs.reduce((max, tab) => (tab.slot >= max ? tab.slot + 1 : max), 0);
    const previousSessionId = this.state.sessionId;
    try {
      const result = (await StartSession(runSelection, nextSlot, this.terminal?.cols || 80, this.terminal?.rows || 24)) as StartSessionResult;
      this.registerOpenSessionResult(key, result, runSelection, previousSessionId);
      this.focusTerminalSoon();
      this.queueTerminalResize();
    } catch (error: unknown) {
      this.showTerminalMessage(readError(error));
    }
  }

  selectTerminalTab(sessionId: number): void {
    if (sessionId <= 0 || sessionId === this.state.sessionId) {
      return;
    }
    this.state.sessionId = sessionId;
    this.rememberSelectedTabForCurrentEnv(sessionId);
    this.syncDebugDisplay();
    rebuildTerminalDisplayBuffer(this.sessions, sessionId);
    this.resetTerminal();
    this.writeTerminalBuffer(this.sessions.displayBuffer(sessionId));
    const exitReason = this.sessions.exitReason(sessionId);
    if (exitReason) {
      this.state.terminalCopyOutput = this.sessions.exitOutput(sessionId);
      this.state.terminalCopyStatus = '';
      this.showTerminalMessage(exitReason);
    } else {
      this.hideTerminalMessage();
    }
    this.focusTerminalSoon();
    this.queueTerminalResize();
  }

  async closeTerminalTab(sessionId: number): Promise<void> {
    if (sessionId <= 0) {
      return;
    }
    const selection = this.state.selected;
    if (!selection) {
      return;
    }
    const runSelection = { ...selection, debug: this.state.debugOpen || undefined };
    const key = selectionKey(runSelection);
    const tabs = this.state.tabsByEnv[key] || [];
    const target = tabs.find((tab) => tab.sessionId === sessionId);
    if (target && target.kind !== 'extra') {
      return;
    }
    try {
      await CloseSession(sessionId);
    } catch (error: unknown) {
      this.showTerminalMessage(readError(error));
      return;
    }
    const remaining = this.removeTab(key, sessionId);
    this.sessions.clearSessionDebug(sessionId);
    if (this.state.sessionId === sessionId) {
      const next = remaining[remaining.length - 1];
      if (next) {
        this.selectTerminalTab(next.sessionId);
      } else {
        this.state.sessionId = 0;
        this.state.debugOutput = '';
        this.resetTerminal();
      }
      return;
    }
  }

  private showOpenSelectionStatus(sessionId: number, selection: UISelection): void {
    const exitReason = this.sessions.exitReason(sessionId);
    if (exitReason) {
      this.state.terminalCopyOutput = this.sessions.exitOutput(sessionId);
      this.state.terminalCopyStatus = '';
      this.showTerminalMessage(exitReason);
      return;
    }
    const buffer = this.sessions.displayBuffer(sessionId);
    if (buffer.length > 0) {
      this.hideTerminalMessage();
      return;
    }
    this.showTerminalMessage(`Opening ${selection.tenant} / ${selection.environment}...`, true);
  }

  async openIDE(selection: UISelection | null, ide: IDEKind): Promise<void> {
    if (!selection) {
      this.showTerminalMessage('Choose an environment from the left pane.');
      return;
    }
    const runSelection = { ...selection, debug: this.state.debugOpen || undefined };
    const label = ideLabel(ide);
    this.state.selected = selection;
    if (this.state.debugOpen) {
      const header = `$ ${formatIDECommand(runSelection, ide)}\n`;
      this.sessions.setSessionDebug(this.state.sessionId, header);
      this.syncDebugDisplay();
    }
    this.state.terminalCopyOutput = '';
    this.state.terminalCopyStatus = '';
    this.showTerminalMessage(`Opening ${label} for ${selection.tenant} / ${selection.environment}...`);

    try {
      await store.dispatch(sessionApi.endpoints.openIDE.initiate({ selection: runSelection, ide })).unwrap();
    } catch (error: unknown) {
      const failure = ideOpenFailure(selection, label, readError(error));
      this.appendDebugOutput(debugOutputBlock(failure.copyOutput));
      this.dismissNotification();
      this.showTerminalFailure(failure.message, failure.detail, failure.copyOutput, '', null);
      return;
    }
    this.dismissTerminalStatus();
    this.showNotification('success', `Opened ${label} for ${selection.tenant} / ${selection.environment}.`);
  }

  openInitializeDialog(): void {
    const tenantDefault = this.state.selected?.tenant || this.state.tenants[0]?.name || '';
    const containerRegistryDefault = loadSavedPastContainerRegistries()[0] || '';
    this.state.environmentDialog = {
      open: true,
      actionMode: 'init',
      tenant: tenantDefault,
      environment: '',
      version: this.state.versionSuggestions[0]?.version || '',
      kubernetesContext: '',
      kubernetesContexts: [],
      kubernetesContextsLoading: true,
      resourceStatus: null,
      resourceStatusLoading: false,
      runtimePod: defaultEnvironmentDialog().runtimePod,
      containerRegistry: containerRegistryDefault,
      noGit: false,
      bootstrap: false,
      setDefaultTenant: true,
      versionImage: this.state.versionSuggestions[0]?.image || '',
      choicesOpen: false,
      busy: false,
      error: '',
    };
    void this.refreshKubernetesContexts();
    void this.refreshDialogVersionSuggestions(true);
  }

  closeEnvironmentDialog(): void {
    if (this.state.environmentDialog.busy) {
      return;
    }
    this.state.environmentDialog = defaultEnvironmentDialog();
    this.focusTerminalSoon();
  }

  updateEnvironmentDialog(values: Partial<EnvironmentDialogState>): void {
    if (this.state.environmentDialog.busy) {
      return;
    }
    const versionReset = values.version !== undefined;
    this.state.environmentDialog = {
      ...this.state.environmentDialog,
      ...values,
      error: values.error ?? '',
      ...(versionReset ? { versionImage: '', choicesOpen: false } : {}),
    };
    if (values.tenant !== undefined) {
      this.scheduleDialogVersionSuggestionRefresh(true);
    }
    if (values.kubernetesContext !== undefined) {
      void this.refreshEnvironmentRuntimeResources(values.kubernetesContext);
    }
  }

  toggleEnvironmentVersionChoices(): void {
    this.setEnvironmentVersionChoicesOpen(!this.state.environmentDialog.choicesOpen);
  }

  setEnvironmentVersionChoicesOpen(open: boolean): void {
    if (this.state.environmentDialog.busy) {
      return;
    }
    this.state.environmentDialog = {
      ...this.state.environmentDialog,
      choicesOpen: open && this.state.versionSuggestions.length > 0,
    };
  }

  selectEnvironmentVersionSuggestion(suggestion: UIVersionSuggestion | undefined): void {
    if (this.state.environmentDialog.busy) {
      return;
    }
    this.state.environmentDialog = {
      ...this.state.environmentDialog,
      version: suggestion?.version || '',
      versionImage: suggestion?.image || '',
      choicesOpen: false,
    };
  }

  async submitEnvironmentDialog(form: HTMLFormElement): Promise<void> {
    const dialog = this.state.environmentDialog;
    if (dialog.busy) {
      return;
    }
    const selection = this.environmentDialogSelection(dialog);
    if (!selection) {
      this.state.environmentDialog = { ...dialog, error: '' };
      form.reportValidity();
      return;
    }
    const resourceError = dialog.actionMode === 'init' ? runtimeResourceLimitMessage(dialog.runtimePod, dialog.resourceStatus) : '';
    if (resourceError) {
      this.state.environmentDialog = { ...dialog, error: resourceError };
      return;
    }

    rememberEnvironmentDialogSelection(selection, dialog.actionMode);
    this.beginEnvironmentDialogSubmit(dialog, selection);
    const previousSelected = this.state.selected;
    try {
      await this.startEnvironmentDialogSelection(selection, dialog.actionMode);
      this.state.environmentDialog = defaultEnvironmentDialog();
      this.focusTerminalSoon();
    } catch (error) {
      const message = readError(error);
      this.state.selected = previousSelected;
      this.state.environmentDialog = {
        ...this.state.environmentDialog,
        busy: false,
        error: message,
      };
      this.showTerminalMessage(message);
    }
  }

  private environmentDialogSelection(dialog: EnvironmentDialogState): UISelection | null {
    const values = normalizedEnvironmentDialogValues(dialog);
    if (!validEnvironmentDialogValues(values, dialog.actionMode)) {
      return null;
    }
    const isInit = dialog.actionMode === 'init';
    const runtimePod = runtimePodConfigToKubernetes(dialog.runtimePod);
    return {
      tenant: values.tenant,
      environment: values.environment,
      version: values.version,
      runtimeImage: this.resolveEnvironmentRuntimeImage(values.version),
      runtimeCpu: isInit ? runtimePod.cpu : undefined,
      runtimeMemory: isInit ? runtimePod.memory : undefined,
      kubernetesContext: isInit ? values.kubernetesContext : undefined,
      containerRegistry: isInit ? values.containerRegistry : undefined,
      noGit: dialog.noGit,
      bootstrap: isInit ? dialog.bootstrap : undefined,
      setDefaultTenant: isInit ? dialog.setDefaultTenant : undefined,
    };
  }

  private beginEnvironmentDialogSubmit(dialog: EnvironmentDialogState, selection: UISelection): void {
    this.state.environmentDialog = {
      ...dialog,
      tenant: selection.tenant,
      environment: selection.environment,
      version: selection.version || '',
      kubernetesContext: selection.kubernetesContext || '',
      runtimePod: dialog.runtimePod,
      containerRegistry: selection.containerRegistry || '',
      busy: true,
      error: '',
      choicesOpen: false,
    };
  }

  private async startEnvironmentDialogSelection(selection: UISelection, actionMode: EnvironmentDialogState['actionMode']): Promise<void> {
    if (actionMode === 'deploy') {
      await this.startDeploySelection(selection);
      return;
    }
    await this.startInitSelection(selection);
  }

  openManageDialog(selection: UISelection): void {
    this.manageEnvironment.openDialog(selection);
  }

  closeManageDialog(): void {
    this.manageEnvironment.closeDialog();
  }

  setManageTab(tab: ManageTab): void {
    this.manageEnvironment.setTab(tab);
  }

  updateManageDialog(values: Partial<ManageDialogState>): void {
    this.manageEnvironment.updateDialog(values);
  }

  toggleManageVersionChoices(): void {
    this.manageEnvironment.toggleVersionChoices();
  }

  setManageVersionChoicesOpen(open: boolean): void {
    this.manageEnvironment.setVersionChoicesOpen(open);
  }

  selectManageVersionSuggestion(suggestion: UIVersionSuggestion | undefined): void {
    this.manageEnvironment.selectVersionSuggestion(suggestion);
  }

  updateManageConfig(values: Partial<UIEnvironmentConfig>): void {
    this.manageEnvironment.updateConfig(values);
  }

  updateManageSSHDConfig(values: Partial<UIEnvironmentConfig['sshd']>): void {
    this.manageEnvironment.updateSSHDConfig(values);
  }

  updateManageClaudeConfig(values: Partial<UIEnvironmentConfig['claude']>): void {
    this.manageEnvironment.updateClaudeConfig(values);
  }

  async chooseWorkspaceSyncLocalFolder(): Promise<void> {
    await this.manageEnvironment.chooseWorkspaceSyncLocalFolder();
  }

  async loadManageConfig(): Promise<void> {
    await this.manageEnvironment.loadConfig();
  }

  async submitManageConfig(): Promise<void> {
    await this.manageEnvironment.submitConfig();
  }

  async startManageCloudContext(name: string): Promise<void> {
    await this.manageEnvironment.startCloudContext(name);
  }

  async enableManageSSHD(): Promise<void> {
    await this.manageEnvironment.enableSSHD();
  }

  async startManageDoctor(): Promise<void> {
    await this.manageEnvironment.startDoctor();
  }

  async stopManageCloudContext(name: string): Promise<void> {
    await this.manageEnvironment.stopCloudContext(name);
  }

  openGlobalConfigDialog(): void {
    this.globalConfig.openDialog();
  }

  closeGlobalConfigDialog(): void {
    this.globalConfig.closeDialog();
  }

  updateGlobalConfigDialog(values: Partial<GlobalConfigDialogState>): void {
    this.globalConfig.updateDialog(values);
  }

  updateGlobalConfig(values: Partial<UIERunConfig>): void {
    this.globalConfig.updateConfig(values);
  }

  updateCloudContextDraft(values: Partial<UICloudContextInitInput>): void {
    this.globalConfig.updateCloudContextDraft(values);
  }

  async loadGlobalConfig(): Promise<void> {
    await this.globalConfig.loadConfig();
  }

  async refreshCloudProviders(): Promise<void> {
    await this.globalConfig.refreshCloudProviders();
  }

  async refreshCloudContexts(): Promise<void> {
    await this.globalConfig.refreshCloudContexts();
  }

  async initCloudContext(): Promise<void> {
    await this.globalConfig.initCloudContext();
  }

  async stopCloudContext(name: string): Promise<void> {
    await this.globalConfig.stopCloudContext(name);
  }

  async startCloudContext(name: string): Promise<void> {
    await this.globalConfig.startCloudContext(name);
  }

  async toggleIdleCloudContext(): Promise<void> {
    await this.globalConfig.toggleIdleCloudContext();
  }

  async startAWSCloudInit(): Promise<void> {
    await this.globalConfig.startAWSCloudInit();
  }

  async loginCloudProvider(alias: string): Promise<void> {
    await this.globalConfig.loginCloudProvider(alias);
  }

  async loginPrimaryCloudProvider(alias: string): Promise<void> {
    await this.updatePrimaryCloudProvider(alias, 'login', (target) =>
      store.dispatch(cloudApi.endpoints.loginCloudProvider.initiate(target)).unwrap(),
    );
  }

  async logoutPrimaryCloudProvider(alias: string): Promise<void> {
    await this.updatePrimaryCloudProvider(alias, 'logout', (target) =>
      store.dispatch(cloudApi.endpoints.logoutCloudProvider.initiate(target)).unwrap(),
    );
  }

  async getPrimaryCloudProviderBearerToken(alias: string): Promise<void> {
    alias = alias.trim();
    if (!alias || this.state.sidebarCloudAliasBusy) {
      return;
    }
    this.state.sidebarCloudAliasBusy = true;
    this.state.sidebarCloudAliasAction = 'bearer';
    try {
      const result = await store
        .dispatch(cloudApi.endpoints.getCloudProviderBearerToken.initiate(alias))
        .unwrap();
      await ClipboardSetText(result.token);
      this.state.cloudProviders = replaceCloudProvider(this.state.cloudProviders, result.provider);
      this.state.sidebarCloudAliasBusy = false;
      this.state.sidebarCloudAliasAction = '';
      const issuer = result.issuer?.trim();
      this.showTerminalMessage(issuer ? `Copied bearer token for ${result.alias}. Issuer: ${issuer}` : `Copied bearer token for ${result.alias}.`);
      this.showNotification('success', `Copied bearer token for ${result.alias}.`);
    } catch (error) {
      const message = readError(error);
      this.state.sidebarCloudAliasBusy = false;
      this.state.sidebarCloudAliasAction = '';
      this.showTerminalMessage(message);
      this.showNotification('error', message);
    }
  }

  async submitGlobalConfig(): Promise<void> {
    await this.globalConfig.submitConfig();
  }

  openTenantDialog(tenant: string): void {
    this.state.tenantDialog = {
      open: true,
      tenant,
      config: {
        name: tenant,
        defaultEnvironment: '',
        apiUrl: '',
        cloudProviderAliases: [],
        primaryCloudProviderAlias: '',
        cloudProviders: [],
      },
      configLoading: true,
      busy: false,
      busyAction: '',
      busyTarget: '',
      error: '',
    };
    void this.loadTenantConfig();
  }

  closeTenantDialog(): void {
    if (this.state.tenantDialog.busy) {
      return;
    }
    this.state.tenantDialog = defaultTenantDialog();
    this.focusTerminalSoon();
  }

  updateTenantDialog(values: Partial<TenantDialogState>): void {
    if (this.state.tenantDialog.busy) {
      return;
    }
    this.state.tenantDialog = {
      ...this.state.tenantDialog,
      ...values,
      error: values.error ?? '',
    };
  }

  updateTenantConfig(values: Partial<UITenantConfig>): void {
    if (this.state.tenantDialog.busy || this.state.tenantDialog.configLoading) {
      return;
    }
    this.updateTenantDialog({
      config: {
        ...this.state.tenantDialog.config,
        ...values,
      },
    });
  }

  async loadTenantConfig(): Promise<void> {
    const dialog = this.state.tenantDialog;
    if (!dialog.open || !dialog.tenant) {
      return;
    }
    this.state.tenantDialog = {
      ...dialog,
      configLoading: true,
      error: '',
    };
    try {
      const result = await store
        .dispatch(tenantApi.endpoints.getTenantConfig.initiate(dialog.tenant))
        .unwrap();
      if (result.cloudProviders) {
        this.state.cloudProviders = result.cloudProviders;
      }
      this.state.tenantDialog = {
        ...this.state.tenantDialog,
        config: result,
        configLoading: false,
        error: '',
      };
    } catch (error) {
      this.state.tenantDialog = {
        ...this.state.tenantDialog,
        configLoading: false,
        error: readError(error),
      };
    }
  }

  async submitTenantConfig(): Promise<void> {
    const dialog = this.state.tenantDialog;
    if (dialog.busy || dialog.configLoading) {
      return;
    }
    if (!dialog.tenant) {
      this.closeTenantDialog();
      return;
    }
    this.state.tenantDialog = { ...dialog, busy: true, busyAction: 'save', busyTarget: '', error: '' };
    try {
      const result = await store
        .dispatch(tenantApi.endpoints.saveTenantConfig.initiate(dialog.config))
        .unwrap();
      this.applySavedTenantConfig(result);
      this.state.tenantDialog = {
        ...this.state.tenantDialog,
        config: result,
        busy: false,
        busyAction: '',
        busyTarget: '',
        error: '',
      };
      this.showNotification('success', `Saved config for ${result.name}.`);
      this.closeTenantDialog();
    } catch (error) {
      const message = readError(error);
      this.state.tenantDialog = {
        ...this.state.tenantDialog,
        busy: false,
        busyAction: '',
        busyTarget: '',
        error: message,
      };
      this.showTerminalMessage(message);
    }
  }

  async setupTenantCloudProviderOIDC(alias: string): Promise<void> {
    alias = alias.trim();
    const dialog = this.state.tenantDialog;
    if (!alias || dialog.busy || dialog.configLoading) {
      return;
    }
    this.state.tenantDialog = { ...dialog, busy: true, busyAction: 'cloud-oidc', busyTarget: alias, error: '' };
    try {
      const provider = await store
        .dispatch(cloudApi.endpoints.setupCloudProviderOIDC.initiate(alias))
        .unwrap();
      this.state.cloudProviders = replaceCloudProvider(this.state.cloudProviders, provider);
      const currentProviders = this.state.tenantDialog.config.cloudProviders || [];
      this.state.tenantDialog = {
        ...this.state.tenantDialog,
        config: {
          ...this.state.tenantDialog.config,
          cloudProviders: replaceCloudProvider(currentProviders, provider),
        },
        busy: false,
        busyAction: '',
        busyTarget: '',
        error: '',
      };
      this.showNotification('success', `Updated OIDC issuer for ${provider.alias}.`);
    } catch (error) {
      const message = readError(error);
      this.state.tenantDialog = {
        ...this.state.tenantDialog,
        busy: false,
        busyAction: '',
        busyTarget: '',
        error: message,
      };
      this.showTerminalMessage(message);
      this.showNotification('error', message);
    }
  }

  private async updatePrimaryCloudProvider(alias: string, action: 'login' | 'logout', run: (alias: string) => Promise<unknown>): Promise<void> {
    alias = alias.trim();
    if (!alias || this.state.sidebarCloudAliasBusy) {
      return;
    }
    this.state.sidebarCloudAliasBusy = true;
    this.state.sidebarCloudAliasAction = action;
    try {
      const provider = (await run(alias)) as UICloudProviderStatus;
      this.state.cloudProviders = replaceCloudProvider(this.state.cloudProviders, provider);
      this.state.sidebarCloudAliasBusy = false;
      this.state.sidebarCloudAliasAction = '';
      this.showTerminalMessage(`${provider.alias}: ${provider.status}`);
    } catch (error) {
      const message = readError(error);
      this.state.sidebarCloudAliasBusy = false;
      this.state.sidebarCloudAliasAction = '';
      this.showTerminalMessage(message);
      this.showNotification('error', message);
    }
  }

  private applySavedTenantConfig(config: UITenantConfig): void {
    const tenantName = config.name.trim();
    if (!tenantName) {
      return;
    }
    this.state.tenants = this.state.tenants.map((tenant) => {
      if (tenant.name !== tenantName) {
        return tenant;
      }
      return {
        ...tenant,
        cloudProviderAliases: config.cloudProviderAliases || [],
        primaryCloudProviderAlias: config.primaryCloudProviderAlias || '',
      };
    });
    if (config.cloudProviders) {
      this.state.cloudProviders = config.cloudProviders;
    }
  }

  async submitManageDeploy(): Promise<void> {
    await this.manageEnvironment.submitDeploy();
  }

  async submitManageDelete(): Promise<void> {
    await this.manageEnvironment.submitDelete();
  }

  setDiffFilter(value: string): void {
    this.state.diffFilter = value.trim().toLowerCase();
  }

  toggleChangedFiles(): void {
    this.state.changedFilesOpen = !this.state.changedFilesOpen;
  }

  selectReviewRange(scope: AppState['selectedReviewScope'], hash = ''): void {
    const selected = hash.trim();
    if ((scope === this.state.selectedReviewScope && selected === this.state.selectedReviewCommit) || this.state.diffLoading) {
      return;
    }
    this.state.selectedReviewScope = scope;
    this.state.selectedReviewCommit = selected;
    void this.loadReviewDiff();
  }

  async loadReviewDiff(options: { silent?: boolean } = {}): Promise<void> {
    const selection = this.state.selected;
    if (!selection) {
      return;
    }
    const request = ++this.reviewDiffRequest;
    const selectedKey = selectionKey(selection);
    const scope = this.state.selectedReviewScope;
    const selectedCommit = this.state.selectedReviewCommit;
    if (!options.silent) {
      this.state.diffLoading = true;
      this.state.diffError = '';
    }
    try {
      const diff = await store
        .dispatch(
          reviewApi.endpoints.getDiff.initiate(
            { selection, options: { scope, selectedCommit } },
            { forceRefetch: true },
          ),
        )
        .unwrap();
      if (!this.isCurrentReviewDiffRequest(request, selectedKey)) {
        return;
      }
      this.state.diff = diff;
      this.state.diffError = '';
      this.state.diffErrorReconnectable = false;
      this.state.selectedReviewScope = diff.scope || 'current';
      this.state.selectedReviewCommit = diff.selectedCommit || '';
      this.state.selectedDiffPath = chooseSelectedDiffPath(diff, this.state.selectedDiffPath);
    } catch (error: unknown) {
      if (!this.isCurrentReviewDiffRequest(request, selectedKey)) {
        return;
      }
      if (options.silent && this.state.diff) {
        return;
      }
      if (!options.silent || !this.state.diff) {
        this.state.diff = null;
      }
      const message = readError(error);
      if (isMcpUnreachableMessage(message)) {
        this.state.diffError = stripMcpUnreachableMarker(message);
        this.state.diffErrorReconnectable = true;
      } else {
        this.state.diffError = message;
        this.state.diffErrorReconnectable = false;
      }
    } finally {
      if (request === this.reviewDiffRequest) {
        if (!options.silent) {
          this.state.diffLoading = false;
        }
        this.scheduleReviewDiffRefresh();
      }
    }
  }

  async refreshReviewDiff(): Promise<void> {
    if (!this.state.selected) {
      return;
    }
    await this.loadReviewDiff();
    if (!this.state.diffError) {
      this.showNotification('success', 'Diff refreshed.');
    }
  }

  private isCurrentReviewDiffRequest(request: number, selectedKey: string): boolean {
    return request === this.reviewDiffRequest && selectedKey === selectionKey(this.state.selected || { tenant: '', environment: '' });
  }

  private scheduleReviewDiffRefresh(delay = REVIEW_DIFF_REFRESH_INTERVAL_MS): void {
    window.clearTimeout(this.reviewDiffRefreshTimer);
    if (!this.state.reviewOpen || !this.state.selected) {
      this.reviewDiffRefreshTimer = 0;
      return;
    }
    this.reviewDiffRefreshTimer = window.setTimeout(() => {
      if (!this.state.reviewOpen || !this.state.selected) {
        this.stopReviewDiffRefresh();
        return;
      }
      if (this.state.diffLoading) {
        this.scheduleReviewDiffRefresh();
        return;
      }
      void this.loadReviewDiff({ silent: true });
    }, delay);
  }

  private stopReviewDiffRefresh(): void {
    window.clearTimeout(this.reviewDiffRefreshTimer);
    this.reviewDiffRefreshTimer = 0;
  }

  // requestReconnect opens the explicit-confirmation dialog for the
  // unreachable-MCP recovery flow. The dialog runs `erun open`, which can
  // redeploy the runtime, so the action is gated on a deliberate user click.
  requestReconnect(): void {
    if (!this.state.selected) {
      return;
    }
    this.state.reconnect = { status: 'confirm', lastLine: '', error: '' };
  }

  cancelReconnect(): void {
    if (this.state.reconnect.status === 'running') {
      return;
    }
    this.state.reconnect = { status: 'idle', lastLine: '', error: '' };
  }

  async confirmReconnect(): Promise<void> {
    const selection = this.state.selected;
    if (!selection || this.state.reconnect.status === 'running') {
      return;
    }
    this.state.reconnect = { status: 'running', lastLine: '', error: '' };
    try {
      await store.dispatch(sessionApi.endpoints.reconnectMCP.initiate(selection)).unwrap();
      this.state.reconnect = { status: 'idle', lastLine: '', error: '' };
      await this.loadReviewDiff();
    } catch (error: unknown) {
      this.state.reconnect = {
        status: 'error',
        lastLine: this.state.reconnect.lastLine,
        error: readError(error),
      };
    }
  }

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

  toggleDiffDirectory(path: string): void {
    store.dispatch(toggleDiffDirCollapsed(path));
  }

  selectDiffPath(path: string): void {
    this.state.selectedDiffPath = path;
    window.setTimeout(() => this.scrollSelectedDiffIntoView(), 0);
  }

  queueVisibleDiffSelectionUpdate(): void {
    if (this.reviewScrollFrame > 0) {
      return;
    }
    this.reviewScrollFrame = window.requestAnimationFrame(() => {
      this.reviewScrollFrame = 0;
      this.updateSelectedDiffPathFromScroll();
    });
  }

  titlebarDoubleClick(event: React.MouseEvent<HTMLElement>): void {
    const target = event.target;
    if (target instanceof HTMLElement && target.closest('button')) {
      return;
    }
    WindowToggleMaximise();
  }

  showTerminalMessage(message: string, busy = false): void {
    this.state.terminalMessage = message;
    this.state.terminalStatusKind = 'info';
    this.state.terminalStatusDetail = '';
    this.state.terminalStatusAction = '';
    this.state.terminalBusy = busy;
    if (busy) {
      this.state.terminalCopyOutput = '';
      this.state.terminalCopyStatus = '';
    }
    this.terminalStatusRetrySelection = null;
  }

  showTerminalFailure(message: string, detail: string, copyOutput: string, action: TerminalStatusAction, retrySelection: UISelection | null): void {
    this.state.terminalMessage = message;
    this.state.terminalStatusKind = action === 'wait-longer' ? 'warning' : 'error';
    this.state.terminalStatusDetail = detail;
    this.state.terminalStatusAction = action;
    this.state.terminalBusy = false;
    this.state.terminalCopyOutput = copyOutput;
    this.state.terminalCopyStatus = '';
    this.terminalStatusRetrySelection = action === 'wait-longer' ? retrySelection : null;
  }

  showNotification(kind: NonNullable<AppState['notification']>['kind'], message: string): void {
    const trimmed = message.trim();
    if (!trimmed) {
      return;
    }
    window.clearTimeout(this.notificationTimer);
    this.state.notification = {
      kind,
      message: trimmed,
    };
    if (kind === 'success' || kind === 'info') {
      this.notificationTimer = window.setTimeout(() => {
        this.dismissNotification();
      }, 3200);
    }
  }

  dismissNotification(): void {
    window.clearTimeout(this.notificationTimer);
    if (!this.state.notification) {
      return;
    }
    this.state.notification = null;
  }

  dismissTerminalStatus(): void {
    if (!this.state.terminalMessage && !this.state.terminalStatusDetail && !this.state.terminalCopyOutput && !this.state.terminalCopyStatus) {
      return;
    }
    this.state.terminalMessage = '';
    this.state.terminalStatusKind = 'info';
    this.state.terminalStatusDetail = '';
    this.state.terminalStatusAction = '';
    this.state.terminalBusy = false;
    this.state.terminalCopyOutput = '';
    this.state.terminalCopyStatus = '';
    this.terminalStatusRetrySelection = null;
  }

  async waitLongerForTerminalStatus(): Promise<void> {
    const selection = this.terminalStatusRetrySelection;
    if (!selection) {
      return;
    }
    this.state.terminalStatusAction = '';
    this.state.terminalCopyOutput = '';
    this.state.terminalCopyStatus = '';
    this.showTerminalMessage(`Waiting longer for ${selection.tenant} / ${selection.environment}...`, true);
    await this.openSelection(selection);
  }

  focusTerminalSoon(): void {
    window.setTimeout(() => {
      this.terminal?.focus();
      window.requestAnimationFrame(() => this.terminal?.focus());
      window.setTimeout(() => this.terminal?.focus(), 80);
    }, 0);
  }

  async copyTerminalOutput(): Promise<void> {
    if (!this.state.terminalCopyOutput) {
      return;
    }
    try {
      await ClipboardSetText(this.state.terminalCopyOutput);
      this.state.terminalCopyStatus = 'Copied';
    } catch (error) {
      this.state.terminalCopyStatus = readError(error);
    }
    window.clearTimeout(this.terminalCopyStatusTimer);
    this.terminalCopyStatusTimer = window.setTimeout(() => {
      this.state.terminalCopyStatus = '';
    }, 1400);
  }


  private scheduleIdleStatusPoll(delay = 1000): void {
    window.clearTimeout(this.idleStatusTimer);
    this.idleStatusTimer = window.setTimeout(() => {
      void this.refreshIdleStatus();
    }, delay);
  }

  private async refreshIdleStatus(): Promise<void> {
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
      this.showTerminalMessage('Loading environments...', true);
      const loaded = await store
        .dispatch(stateApi.endpoints.getInitialState.initiate(undefined, { forceRefetch: true }))
        .unwrap();
      this.state.tenants = loaded.tenants || [];
      this.state.cloudProviders = loaded.cloudProviders || [];
      this.state.selected = loaded.selected || null;
      this.state.versionSuggestions = normalizeVersionSuggestions(loaded.versionSuggestions || []);
      this.selectLoadedKubernetesContexts(loaded.kubernetesContexts || []);
      if (loaded.message) {
        this.showTerminalMessage(loaded.message);
        return;
      }

      if (this.state.selected) {
        await this.openSelection(this.state.selected);
        return;
      }

      this.showTerminalMessage('Choose an environment from the left pane.');
    } catch (error: unknown) {
      this.showTerminalMessage(readError(error));
    }
  }

  private async startInitSelection(selection: UISelection): Promise<void> {
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

  private async startDeploySelection(selection: UISelection): Promise<void> {
    const runSelection = { ...selection, debug: this.state.debugOpen || undefined };
    this.state.selected = selection;
    this.state.terminalCopyOutput = '';
    this.state.terminalCopyStatus = '';
    this.showTerminalMessage(`Deploying runtime for ${selection.tenant} / ${selection.environment}...`, true);

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
    this.hideTerminalMessage();
    this.focusTerminalSoon();
    this.queueTerminalResize();
  }

  private async reloadStateAfterEnvironmentChange(): Promise<void> {
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

  private async refreshKubernetesContexts(): Promise<void> {
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

  private async refreshEnvironmentRuntimeResources(kubernetesContext: string): Promise<void> {
    const request = ++this.environmentResourceStatusRequest;
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
      if (request !== this.environmentResourceStatusRequest || !this.state.environmentDialog.open) {
        return;
      }
      this.state.environmentDialog = {
        ...this.state.environmentDialog,
        resourceStatus: status,
        resourceStatusLoading: false,
      };
    } catch (error) {
      if (request !== this.environmentResourceStatusRequest || !this.state.environmentDialog.open) {
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

  private scheduleDialogVersionSuggestionRefresh(selectDefault: boolean): void {
    if (this.versionSuggestionTimer) {
      window.clearTimeout(this.versionSuggestionTimer);
    }
    this.versionSuggestionTimer = window.setTimeout(() => {
      void this.refreshDialogVersionSuggestions(selectDefault);
    }, 250);
  }

  private async refreshDialogVersionSuggestions(selectDefault: boolean): Promise<void> {
    const request = ++this.versionSuggestionRequest;
    const dialog = this.state.environmentDialog;
    const selection = {
      tenant: normalizeDialogValue(dialog.tenant),
      environment: normalizeDialogValue(dialog.environment),
      action: dialog.actionMode,
    };
    const raw = await store
      .dispatch(
        environmentApi.endpoints.getVersionSuggestions.initiate(selection, { forceRefetch: true }),
      )
      .unwrap();
    const suggestions = normalizeVersionSuggestions(raw);
    if (request !== this.versionSuggestionRequest || !this.state.environmentDialog.open) {
      return;
    }

    this.state.versionSuggestions = suggestions;
    const currentVersion = normalizeDialogValue(this.state.environmentDialog.version);
    if (selectDefault || !suggestions.some((suggestion) => suggestion.version === currentVersion)) {
      this.selectEnvironmentVersionSuggestion(suggestions[0]);
    } else {
    }
  }

  private resolveEnvironmentRuntimeImage(version: string): string {
    if (this.state.environmentDialog.versionImage) {
      return this.state.environmentDialog.versionImage;
    }
    const suggestion = this.state.versionSuggestions.find((value) => value.version === version);
    return suggestion?.image || '';
  }

  private resolveManageRuntimeImage(version: string): string {
    if (this.state.manageDialog.versionImage) {
      return this.state.manageDialog.versionImage;
    }
    const suggestion = this.state.versionSuggestions.find((value) => value.version === version);
    return suggestion?.image || '';
  }

  private resetTerminal(): void {
    this.terminal?.reset();
    this.terminal?.clear();
  }

  private hideTerminalMessage(): void {
    this.state.terminalMessage = '';
    this.state.terminalStatusKind = 'info';
    this.state.terminalStatusDetail = '';
    this.state.terminalStatusAction = '';
    this.state.terminalBusy = false;
    this.state.terminalCopyOutput = '';
    this.state.terminalCopyStatus = '';
    this.terminalStatusRetrySelection = null;
  }

  private handleAppStatus(payload: AppStatusPayload): void {
    const message = String(payload?.message || '').trim();
    if (!message) {
      return;
    }
    this.appendDebugOutput(`[status] ${message}\n`);
    this.showTerminalMessage(message, payload.busy === true);
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
    this.showNotification('success', `Created ${tenant} / ${environment}.`);
    try {
      await this.openSelection({ tenant, environment });
    } catch (error) {
      this.showTerminalMessage(readError(error));
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
    this.showNotification('error', `Failed to create ${tenant} / ${environment}. See the Local tab and the activity drawer for details.`);
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

  private appendDebugOutput(text: string, fromSessionId?: number): void {
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
      this.hideTerminalMessage();
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
      this.showTerminalFailure(failure.message, failure.detail, failedOutput, failure.action, failure.retrySelection);
      return;
    }
    this.showTerminalMessage(reason);
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
      this.selectTerminalTab(next.sessionId);
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
      this.showTerminalMessage(reason);
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
    this.showTerminalMessage(status, true);
  }

  private layoutCallbacks(): {
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

  private applyLayoutVars(): void {
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
    const reviewWidth = this.reviewView?.getBoundingClientRect().width || this.state.reviewWidth;
    const maxFilesForReview = reviewWidth > 0 ? reviewWidth - 260 : MAX_FILES_WIDTH;
    return clamp(this.state.filesWidth, MIN_FILES_WIDTH, Math.max(MIN_FILES_WIDTH, Math.min(MAX_FILES_WIDTH, maxFilesForReview)));
  }

  private clampedDebugHeight(): number {
    const paneHeight = this.terminalPane?.getBoundingClientRect().height || 0;
    const maxDebugForPane = paneHeight > 0 ? paneHeight - 120 : MAX_DEBUG_HEIGHT;
    return clamp(this.state.debugHeight, MIN_DEBUG_HEIGHT, Math.max(MIN_DEBUG_HEIGHT, Math.min(MAX_DEBUG_HEIGHT, maxDebugForPane)));
  }

  private queueTerminalResize = (): void => {
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

  private scrollSelectedDiffIntoView(): void {
    scrollSelectedDiffIntoView(this.diffList, this.state.selectedDiffPath);
  }

  private updateSelectedDiffPathFromScroll(): void {
    const path = visibleDiffPath(this.diffList, this.reviewMain);
    if (!path || path === this.state.selectedDiffPath) {
      return;
    }
    this.state.selectedDiffPath = path;
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

  private registerDebugSession(sessionId: number, selection: UISelection, mode: DebugSessionMode): void {
    this.sessions.registerDebugSession(sessionId, selection, mode);
  }

  private writeTerminalBuffer(chunks: TerminalWriteData[]): void {
    for (const chunk of chunks) {
      this.terminal?.write(chunk);
    }
  }
}

function tenantDashboardInput(tenant: UITenant | undefined): UITenantDashboardInput | null {
  if (!tenant) {
    return null;
  }
  const environment = tenantDashboardEnvironment(tenant);
  const apiUrl = trimOptional(environment?.apiUrl);
  const cloudProviderAlias = trimOptional(tenant.primaryCloudProviderAlias);
  if (!apiUrl || !cloudProviderAlias) {
    return null;
  }
  return {
    tenant: tenant.name,
    environment: trimOptional(environment?.name),
    apiUrl,
    mcpUrl: trimOptional(environment?.mcpUrl),
    kubernetesContext: trimOptional(environment?.kubernetesContext),
    cloudProviderAlias,
  };
}

function tenantDashboardEnvironment(tenant: UITenant): UITenant['environments'][number] | undefined {
  const defaultEnvironment = tenant.defaultEnvironment?.trim();
  return tenant.environments.find((candidate) => candidate.name === defaultEnvironment && candidate.apiUrl) ||
    tenant.environments.find((candidate) => candidate.apiUrl);
}

function trimOptional(value: string | undefined): string {
  return value?.trim() || '';
}
