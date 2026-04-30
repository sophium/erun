import type * as React from 'react';
import { FitAddon } from '@xterm/addon-fit';
import { Terminal, type IDisposable } from '@xterm/xterm';

import { TerminalSessionRegistry } from './TerminalSessionRegistry';
import {
  LoadDiff,
  LoadIdleStatus,
  LoadKubernetesContexts,
  LoadRuntimeResourceStatus,
  LoadState,
  LoadTenantConfig,
  LoadVersionSuggestions,
  OpenIDE,
  ResizeSession,
  SavePastedImage,
  SaveTenantConfig,
  SendSessionInput,
  StartDeploySession,
  StartInitSession,
  StartSession,
} from '../../wailsjs/go/main/App';
import { ClipboardSetText, EventsOn, WindowToggleMaximise } from '../../wailsjs/runtime/runtime';
import { fileToBase64, decodeBase64Bytes, isTerminalPasteTarget, pastedImageFiles } from './clipboard';
import { chooseSelectedDiffPath } from './diffUtils';
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
  MAX_REVIEW_WIDTH,
  MIN_DEBUG_HEIGHT,
  MIN_FILES_WIDTH,
  MIN_REVIEW_WIDTH,
  defaultEnvironmentDialog,
  defaultGlobalConfigDialog,
  defaultManageDialog,
  defaultTenantDialog,
  type AppState,
  type EnvironmentDialogState,
  type GlobalConfigDialogState,
  type ManageDialogState,
  type TenantDialogState,
  type TerminalStatusAction,
} from './state';
import {
  clamp,
  loadSavedDebugHeight,
  loadSavedDebugOpen,
  loadSavedFilesOpen,
  loadSavedFilesWidth,
  loadSavedPastContainerRegistries,
  loadSavedReviewWidth,
  loadSavedSidebarWidth,
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
  DiffResult,
  ManageTab,
  PastedImageResult,
  StartSessionResult,
  TerminalExitPayload,
  TerminalOutputPayload,
  UICloudContextInitInput,
  UIERunConfig,
  UIEnvironmentConfig,
  UIIdleStatus,
  UIRuntimeResourceStatus,
  UISelection,
  UIState,
  UITenantConfig,
  UIVersionSuggestion,
} from '@/types';

const REVIEW_DIFF_REFRESH_INTERVAL_MS = 5000;

export class ERunUIController {
  readonly state: AppState = {
    tenants: [],
    selected: null,
    versionSuggestions: [],
    environmentDialog: defaultEnvironmentDialog(),
    manageDialog: defaultManageDialog(),
    tenantDialog: defaultTenantDialog(),
    globalConfigDialog: defaultGlobalConfigDialog(),
    collapsedTenants: new Set<string>(),
    sessionId: 0,
    sidebarWidth: loadSavedSidebarWidth(),
    reviewWidth: loadSavedReviewWidth(),
    filesWidth: loadSavedFilesWidth(),
    filesOpen: loadSavedFilesOpen(),
    sidebarHidden: false,
    reviewOpen: false,
    changedFilesOpen: true,
    diff: null,
    diffLoading: false,
    diffError: '',
    selectedDiffPath: '',
    selectedReviewScope: 'current',
    selectedReviewCommit: '',
    diffFilter: '',
    collapsedDiffDirs: new Set<string>(),
    notification: null,
    terminalMessage: '',
    terminalStatusKind: 'info',
    terminalStatusDetail: '',
    terminalStatusAction: '',
    terminalBusy: false,
    terminalCopyOutput: '',
    terminalCopyStatus: '',
    idleStatus: null,
    idleCloudContextBusy: false,
    debugOpen: loadSavedDebugOpen(),
    debugHeight: loadSavedDebugHeight(),
    debugOutput: '',
  };

  private readonly subscribers = new Set<() => void>();
  private readonly sessions = new TerminalSessionRegistry();
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
  private pasteHandler: ((event: ClipboardEvent) => void) | null = null;
  private terminalStatusRetrySelection: UISelection | null = null;
  private readonly globalConfig = new GlobalConfigWorkflow({
    state: this.state,
    sessions: this.sessions,
    terminalSize: () => ({ cols: this.terminal?.cols || 80, rows: this.terminal?.rows || 24 }),
    fitTerminal: () => this.fitAddon?.fit(),
    resetTerminal: () => this.resetTerminal(),
    emit: () => this.emit(),
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
    emit: () => this.emit(),
    focusTerminalSoon: () => this.focusTerminalSoon(),
    queueTerminalResize: () => this.queueTerminalResize(),
    refreshKubernetesContexts: () => { void this.refreshKubernetesContexts(); },
    reloadStateAfterEnvironmentChange: () => this.reloadStateAfterEnvironmentChange(),
    resolveRuntimeImage: (version) => this.resolveManageRuntimeImage(version),
    startDeploySelection: (selection) => this.startDeploySelection(selection),
    showNotification: (kind, message) => this.showNotification(kind, message),
    showTerminalMessage: (message, busy) => this.showTerminalMessage(message, busy),
  });

  subscribe = (subscriber: () => void): (() => void) => {
    this.subscribers.add(subscriber);
    return () => this.subscribers.delete(subscriber);
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
      SendSessionInput,
      (error) => this.showTerminalMessage(readError(error)),
    );
    this.terminalDataDisposable = this.terminal.onData((data) => {
      SendSessionInput(data).catch((error: unknown) => {
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
    this.terminalQueryResponseDisposables = [];
    this.terminal?.dispose();
    this.terminal = null;
    this.fitAddon = null;
  }

  toggleSidebar(): void {
    toggleSidebarPanel(this.state, this.layoutCallbacks());
  }

  startSidebarResize(event: React.MouseEvent<HTMLElement>): void {
    startSidebarPanelResize(this.state, event, () => this.applyLayoutVars(), () => this.emit());
  }

  startReviewResize(event: React.MouseEvent<HTMLElement>): void {
    startReviewPanelResize(this.state, event, this.terminalPane, this.layoutCallbacks());
  }

  startFilesResize(event: React.MouseEvent<HTMLElement>): void {
    startFilesPanelResize(this.state, event, this.reviewView, () => this.applyLayoutVars(), () => this.emit());
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
    applyFilesOpen(this.state, open, persist, () => this.applyLayoutVars(), () => this.emit());
  }

  setDebugOpen(open: boolean): void {
    applyDebugOpen(this.state, open, () => this.emit(), this.queueTerminalResize);
  }

  clearDebugOutput(): void {
    this.state.debugOutput = '';
    this.emit();
  }

  toggleTenant(tenant: string): void {
    if (this.state.collapsedTenants.has(tenant)) {
      this.state.collapsedTenants.delete(tenant);
    } else {
      this.state.collapsedTenants.add(tenant);
    }
    this.emit();
  }

  async openSelection(selection: UISelection): Promise<void> {
    const runSelection = { ...selection, debug: this.state.debugOpen || undefined };
    const key = selectionKey(runSelection);
    const previousSessionId = this.state.sessionId;
    const previousKnownSessionId = this.sessions.knownSelectionSession(key);

    this.prepareOpenSelection(selection, runSelection, previousSessionId, previousKnownSessionId);
    this.fitAddon?.fit();
    const result = (await StartSession(runSelection, this.terminal?.cols || 80, this.terminal?.rows || 24)) as StartSessionResult;
    this.registerOpenSessionResult(key, result, runSelection, previousSessionId);
    this.showOpenSelectionStatus(result.sessionId, selection);

    if (this.state.reviewOpen) {
      await this.loadReviewDiff();
    }
    this.focusTerminalSoon();
    this.queueTerminalResize();
    this.emit();
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
      this.emit();
      return;
    }
    if (this.state.debugOpen) {
      this.state.debugOutput = `$ ${formatDebugCommand(runSelection)}\n`;
    }
    this.state.terminalCopyOutput = '';
    this.state.terminalCopyStatus = '';
    this.showTerminalMessage(`Opening ${selection.tenant} / ${selection.environment}...`, true);
    this.emit();
  }

  private registerOpenSessionResult(key: string, result: StartSessionResult, runSelection: UISelection, previousSessionId: number): void {
    this.sessions.trackOpenSession(key, result.sessionId, runSelection);
    this.registerDebugSession(result.sessionId, runSelection, 'open');
    rebuildTerminalDisplayBuffer(this.sessions, result.sessionId);
    this.state.sessionId = result.sessionId;
    if (result.sessionId !== previousSessionId) {
      this.resetTerminal();
      this.writeTerminalBuffer(this.sessions.displayBuffer(result.sessionId));
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
      this.state.debugOutput = `$ ${formatIDECommand(runSelection, ide)}\n`;
    }
    this.emit();
    this.state.terminalCopyOutput = '';
    this.state.terminalCopyStatus = '';
    this.showTerminalMessage(`Opening ${label} for ${selection.tenant} / ${selection.environment}...`);

    try {
      await OpenIDE(runSelection, ide);
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
    const containerRegistryDefault = loadSavedPastContainerRegistries()[0] || 'erunpaas';
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
    this.emit();
    void this.refreshKubernetesContexts();
    void this.refreshDialogVersionSuggestions(true);
  }

  closeEnvironmentDialog(): void {
    if (this.state.environmentDialog.busy) {
      return;
    }
    this.state.environmentDialog = defaultEnvironmentDialog();
    this.emit();
    this.focusTerminalSoon();
  }

  updateEnvironmentDialog(values: Partial<EnvironmentDialogState>): void {
    if (this.state.environmentDialog.busy) {
      return;
    }
    this.state.environmentDialog = {
      ...this.state.environmentDialog,
      ...values,
      error: values.error ?? '',
    };
    if (values.version !== undefined) {
      this.state.environmentDialog.versionImage = '';
      this.state.environmentDialog.choicesOpen = false;
    }
    this.emit();
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
    this.emit();
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
    this.emit();
  }

  async submitEnvironmentDialog(form: HTMLFormElement): Promise<void> {
    const dialog = this.state.environmentDialog;
    if (dialog.busy) {
      return;
    }
    const selection = this.environmentDialogSelection(dialog);
    if (!selection) {
      this.state.environmentDialog = { ...dialog, error: '' };
      this.emit();
      form.reportValidity();
      return;
    }
    const resourceError = dialog.actionMode === 'init' ? runtimeResourceLimitMessage(dialog.runtimePod, dialog.resourceStatus) : '';
    if (resourceError) {
      this.state.environmentDialog = { ...dialog, error: resourceError };
      this.emit();
      return;
    }

    rememberEnvironmentDialogSelection(selection, dialog.actionMode);
    this.beginEnvironmentDialogSubmit(dialog, selection);
    const previousSelected = this.state.selected;
    try {
      await this.startEnvironmentDialogSelection(selection, dialog.actionMode);
      this.state.environmentDialog = defaultEnvironmentDialog();
      this.emit();
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
    this.emit();
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
      },
      configLoading: true,
      busy: false,
      error: '',
    };
    this.emit();
    void this.loadTenantConfig();
  }

  closeTenantDialog(): void {
    if (this.state.tenantDialog.busy) {
      return;
    }
    this.state.tenantDialog = defaultTenantDialog();
    this.emit();
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
    this.emit();
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
    this.emit();
    try {
      const result = (await LoadTenantConfig(dialog.tenant)) as UITenantConfig;
      this.state.tenantDialog = {
        ...this.state.tenantDialog,
        config: result,
        configLoading: false,
        error: '',
      };
      this.emit();
    } catch (error) {
      this.state.tenantDialog = {
        ...this.state.tenantDialog,
        configLoading: false,
        error: readError(error),
      };
      this.emit();
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
    this.state.tenantDialog = { ...dialog, busy: true, error: '' };
    this.emit();
    try {
      const result = (await SaveTenantConfig(dialog.config)) as UITenantConfig;
      this.state.tenantDialog = {
        ...this.state.tenantDialog,
        config: result,
        busy: false,
        error: '',
      };
      this.showNotification('success', `Saved config for ${result.name}.`);
      this.closeTenantDialog();
    } catch (error) {
      const message = readError(error);
      this.state.tenantDialog = {
        ...this.state.tenantDialog,
        busy: false,
        error: message,
      };
      this.showTerminalMessage(message);
      this.emit();
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
    this.emit();
  }

  toggleChangedFiles(): void {
    this.state.changedFilesOpen = !this.state.changedFilesOpen;
    this.emit();
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
      this.emit();
    }
    try {
      const diff = (await LoadDiff(selection, {
        scope,
        selectedCommit,
      })) as DiffResult;
      if (!this.isCurrentReviewDiffRequest(request, selectedKey)) {
        return;
      }
      this.state.diff = diff;
      this.state.diffError = '';
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
      this.state.diffError = readError(error);
    } finally {
      if (request === this.reviewDiffRequest) {
        if (!options.silent) {
          this.state.diffLoading = false;
        }
        this.emit();
        this.scheduleReviewDiffRefresh();
      }
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

  toggleDiffDirectory(path: string): void {
    if (this.state.collapsedDiffDirs.has(path)) {
      this.state.collapsedDiffDirs.delete(path);
    } else {
      this.state.collapsedDiffDirs.add(path);
    }
    this.emit();
  }

  selectDiffPath(path: string): void {
    this.state.selectedDiffPath = path;
    this.emit();
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
    this.emit();
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
    this.emit();
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
    this.emit();

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
    this.emit();
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
    this.emit();
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
    this.emit();
    window.clearTimeout(this.terminalCopyStatusTimer);
    this.terminalCopyStatusTimer = window.setTimeout(() => {
      this.state.terminalCopyStatus = '';
      this.emit();
    }, 1400);
  }

  private emit(): void {
    this.subscribers.forEach((subscriber) => subscriber());
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
      const status = (await LoadIdleStatus(selection)) as UIIdleStatus;
      if (this.isCurrentIdleStatusRequest(request, selection)) {
        this.state.idleStatus = status;
        this.emit();
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
    this.emit();
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
      const loaded = (await LoadState()) as UIState;
      this.state.tenants = loaded.tenants || [];
      this.state.selected = loaded.selected || null;
      this.state.versionSuggestions = normalizeVersionSuggestions(loaded.versionSuggestions || []);
      this.selectLoadedKubernetesContexts(loaded.kubernetesContexts || []);
      this.emit();

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
    const runSelection = { ...selection, debug: this.state.debugOpen || undefined };
    this.state.selected = selection;
    if (this.state.debugOpen) {
      this.state.debugOutput = `$ ${formatDebugCommand(runSelection, 'init')}\n`;
    }
    this.emit();
    this.state.terminalCopyOutput = '';
    this.state.terminalCopyStatus = '';
    this.showTerminalMessage(`Creating remote environment ${selection.tenant} / ${selection.environment}...`, true);

    this.fitAddon?.fit();
    const result = (await StartInitSession(runSelection, this.terminal?.cols || 80, this.terminal?.rows || 24)) as StartSessionResult;
    this.sessions.trackInitSession(result.sessionId, runSelection);
    this.registerDebugSession(result.sessionId, runSelection, 'hidden');
    this.state.sessionId = result.sessionId;

    this.resetTerminal();
    this.focusTerminalSoon();
    this.queueTerminalResize();
    this.emit();
  }

  private async startDeploySelection(selection: UISelection): Promise<void> {
    const runSelection = { ...selection, debug: this.state.debugOpen || undefined };
    this.state.selected = selection;
    if (this.state.debugOpen) {
      this.state.debugOutput = `$ ${formatDebugCommand(runSelection, 'deploy')}\n`;
    }
    this.emit();
    this.state.terminalCopyOutput = '';
    this.state.terminalCopyStatus = '';
    this.showTerminalMessage(`Deploying runtime for ${selection.tenant} / ${selection.environment}...`, true);

    this.fitAddon?.fit();
    const result = (await StartDeploySession(runSelection, this.terminal?.cols || 80, this.terminal?.rows || 24)) as StartSessionResult;
    this.sessions.trackDeploySession(result.sessionId, runSelection);
    this.registerDebugSession(result.sessionId, runSelection, 'hidden');
    this.state.sessionId = result.sessionId;

    this.resetTerminal();
    this.focusTerminalSoon();
    this.queueTerminalResize();
    this.emit();
  }

  private async reloadStateAfterEnvironmentChange(): Promise<void> {
    try {
      const loaded = (await LoadState()) as UIState;
      this.state.tenants = loaded.tenants || [];
      this.state.versionSuggestions = normalizeVersionSuggestions(loaded.versionSuggestions || this.state.versionSuggestions);
      this.selectLoadedKubernetesContexts(loaded.kubernetesContexts || []);
      this.emit();
    } catch {
    }
  }

  private async refreshKubernetesContexts(): Promise<void> {
    try {
      const contexts = ((await LoadKubernetesContexts()) as string[]).map((context) => context.trim()).filter(Boolean);
      if (!this.state.environmentDialog.open || this.state.environmentDialog.actionMode !== 'init') {
        return;
      }
      this.state.environmentDialog = {
        ...this.state.environmentDialog,
        kubernetesContexts: contexts,
        kubernetesContext: this.resolveDialogKubernetesContext(contexts),
        kubernetesContextsLoading: false,
      };
      this.emit();
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
      this.emit();
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
    this.emit();
    try {
      const status = (await LoadRuntimeResourceStatus({
        kubernetesContext: context,
        tenant: normalizeDialogValue(this.state.environmentDialog.tenant),
        environment: normalizeDialogValue(this.state.environmentDialog.environment),
      })) as UIRuntimeResourceStatus;
      if (request !== this.environmentResourceStatusRequest || !this.state.environmentDialog.open) {
        return;
      }
      this.state.environmentDialog = {
        ...this.state.environmentDialog,
        resourceStatus: status,
        resourceStatusLoading: false,
      };
      this.emit();
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
      this.emit();
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
    const suggestions = normalizeVersionSuggestions((await LoadVersionSuggestions(selection)) as UIVersionSuggestion[]);
    if (request !== this.versionSuggestionRequest || !this.state.environmentDialog.open) {
      return;
    }

    this.state.versionSuggestions = suggestions;
    const currentVersion = normalizeDialogValue(this.state.environmentDialog.version);
    if (selectDefault || !suggestions.some((suggestion) => suggestion.version === currentVersion)) {
      this.selectEnvironmentVersionSuggestion(suggestions[0]);
    } else {
      this.emit();
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
    this.emit();
  }

  private handleAppStatus(payload: AppStatusPayload): void {
    const message = String(payload?.message || '').trim();
    if (!message) {
      return;
    }
    this.appendDebugOutput(`[status] ${message}\n`);
    this.showTerminalMessage(message, payload.busy === true);
  }

  private appendDebugOutput(text: string): void {
    if (!this.state.debugOpen || !text) {
      return;
    }
    this.state.debugOutput = trimDebugOutput(this.state.debugOutput + text);
    this.emit();
  }

  private handleTerminalOutput(payload: TerminalOutputPayload): void {
    if (!payload) {
      return;
    }
    const data = decodeBase64Bytes(payload.data);
    this.sessions.appendSessionBuffer(payload.sessionId, data);
    const debugOutput = decodeDebugOutput(data);
    this.appendDebugOutput(debugOutput);
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

    if (selections.initSelection || selections.deploySelection || selections.sshdInitSelection) {
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

  private takeTerminalExitSelections(sessionId: number): TerminalExitSelections {
    return this.sessions.takeExitSelections(sessionId);
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
    const completedSelection = selections.initSelection || selections.deploySelection;
    if (!completedSelection) {
      return false;
    }
    await this.openCompletedSelection(completedSelection);
    return true;
  }

  private async openCompletedSelection(selection: UISelection): Promise<void> {
    try {
      await this.openSelection(selection);
    } catch (error) {
      this.showTerminalMessage(readError(error));
    }
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
    emit: () => void;
    focusTerminalSoon: () => void;
    queueTerminalResize: () => void;
  } {
    return {
      applyLayoutVars: () => this.applyLayoutVars(),
      emit: () => this.emit(),
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
    const paneWidth = this.terminalPane?.getBoundingClientRect().width || 0;
    const maxReviewForPane = paneWidth > 0 ? paneWidth - 370 : MAX_REVIEW_WIDTH;
    return clamp(this.state.reviewWidth, MIN_REVIEW_WIDTH, Math.max(MIN_REVIEW_WIDTH, Math.min(MAX_REVIEW_WIDTH, maxReviewForPane)));
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
        ResizeSession(this.terminal.cols, this.terminal.rows).catch(() => {
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
    this.emit();
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
    const paths: string[] = [];
    for (const image of images) {
      const result = (await SavePastedImage({
        data: await fileToBase64(image),
        mimeType: image.type,
        name: image.name,
      })) as PastedImageResult;
      if (result.path) {
        paths.push(result.path);
      }
    }
    if (paths.length === 0) {
      return;
    }
    await SendSessionInput(`${paths.join(' ')} `);
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
