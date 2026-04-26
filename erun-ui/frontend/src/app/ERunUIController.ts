import type * as React from 'react';
import { FitAddon } from '@xterm/addon-fit';
import { Terminal } from '@xterm/xterm';

import {
  DeleteEnvironment,
  LoadDiff,
  LoadEnvironmentConfig,
  LoadERunConfig,
  LoadState,
  LoadTenantConfig,
  LoadVersionSuggestions,
  ResizeSession,
  SavePastedImage,
  SaveEnvironmentConfig,
  SaveERunConfig,
  SaveTenantConfig,
  SendSessionInput,
  StartDeploySession,
  StartInitSession,
  StartSession,
} from '../../wailsjs/go/main/App';
import { ClipboardSetText, EventsOn, WindowToggleMaximise } from '../../wailsjs/runtime/runtime';
import { fileToBase64, decodeBase64Bytes, isTerminalPasteTarget, pastedImageFiles } from './clipboard';
import { chooseSelectedDiffPath, cssEscape } from './diffUtils';
import { readError } from './errors';
import {
  FILES_OPEN_STORAGE_KEY,
  FILES_WIDTH_STORAGE_KEY,
  MAX_FILES_WIDTH,
  MAX_REVIEW_WIDTH,
  MAX_SIDEBAR_WIDTH,
  MIN_FILES_WIDTH,
  MIN_REVIEW_WIDTH,
  MIN_SIDEBAR_WIDTH,
  REVIEW_WIDTH_STORAGE_KEY,
  SIDEBAR_WIDTH_STORAGE_KEY,
  defaultEnvironmentDialog,
  defaultGlobalConfigDialog,
  defaultManageDialog,
  defaultTenantDialog,
  type AppState,
  type EnvironmentDialogState,
  type GlobalConfigDialogState,
  type ManageDialogState,
  type TenantDialogState,
} from './state';
import { clamp, loadSavedFilesOpen, loadSavedFilesWidth, loadSavedReviewWidth, loadSavedSidebarWidth, saveBoolean, saveNumber } from './storage';
import { deleteConfirmationValue, normalizeDialogValue, normalizeVersionSuggestions, selectionKey } from './versionSuggestions';
import type {
  DeleteEnvironmentResult,
  DiffResult,
  ManageTab,
  PastedImageResult,
  StartSessionResult,
  TerminalExitPayload,
  TerminalOutputPayload,
  UIERunConfig,
  UIEnvironmentConfig,
  UISelection,
  UIState,
  UITenantConfig,
  UIVersionSuggestion,
} from '@/types';

export interface MountElements {
  terminalRoot: HTMLDivElement;
  terminalPane: HTMLElement;
  reviewView: HTMLElement;
  reviewMain: HTMLDivElement;
  diffList: HTMLDivElement;
}

type TerminalDataDisposable = ReturnType<Terminal['onData']>;

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
    diff: null,
    diffLoading: false,
    diffError: '',
    selectedDiffPath: '',
    diffFilter: '',
    collapsedDiffDirs: new Set<string>(),
    terminalMessage: '',
    terminalCopyOutput: '',
    terminalCopyStatus: '',
  };

  private readonly subscribers = new Set<() => void>();
  private readonly initSessionSelections = new Map<number, UISelection>();
  private readonly deploySessionSelections = new Map<number, UISelection>();
  private readonly openSessionSelections = new Map<number, UISelection>();
  private readonly selectionSessions = new Map<string, number>();
  private readonly sessionBuffers = new Map<number, Uint8Array[]>();
  private readonly sessionExitReasons = new Map<number, string>();
  private readonly sessionExitOutputs = new Map<number, string>();
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
  private terminalCopyStatusTimer = 0;
  private versionSuggestionRequest = 0;
  private bootStarted = false;
  private terminalDataDisposable: TerminalDataDisposable | null = null;
  private terminalOutputOff: (() => void) | null = null;
  private terminalExitOff: (() => void) | null = null;
  private pasteHandler: ((event: ClipboardEvent) => void) | null = null;

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

    if (!this.bootStarted) {
      this.bootStarted = true;
      void this.boot();
    }

    return () => {
      window.removeEventListener('resize', this.queueTerminalResize);
      this.resizeObserver?.disconnect();
      this.terminalDataDisposable?.dispose();
      if (this.pasteHandler && this.terminalRoot) {
        this.terminalRoot.removeEventListener('paste', this.pasteHandler, true);
      }
      this.terminalOutputOff?.();
      this.terminalExitOff?.();
      this.terminal?.dispose();
      this.terminal = null;
      this.fitAddon = null;
    };
  }

  toggleSidebar(): void {
    this.state.sidebarHidden = !this.state.sidebarHidden;
    this.applyLayoutVars();
    this.emit();
    this.queueTerminalResize();
    this.focusTerminalSoon();
  }

  startSidebarResize(event: React.MouseEvent<HTMLElement>): void {
    if (this.state.sidebarHidden) {
      return;
    }
    event.preventDefault();
    document.body.classList.add('is-resizing');

    const move = (moveEvent: MouseEvent) => {
      this.state.sidebarWidth = clamp(moveEvent.clientX, MIN_SIDEBAR_WIDTH, MAX_SIDEBAR_WIDTH);
      this.applyLayoutVars();
      this.emit();
    };
    const stop = () => {
      document.body.classList.remove('is-resizing');
      window.removeEventListener('mousemove', move);
      window.removeEventListener('mouseup', stop);
      saveNumber(SIDEBAR_WIDTH_STORAGE_KEY, this.state.sidebarWidth);
    };

    window.addEventListener('mousemove', move);
    window.addEventListener('mouseup', stop);
  }

  startReviewResize(event: React.MouseEvent<HTMLElement>): void {
    if (!this.state.reviewOpen) {
      return;
    }
    event.preventDefault();
    document.body.classList.add('is-resizing-review');

    const move = (moveEvent: MouseEvent) => {
      const paneRect = this.terminalPane?.getBoundingClientRect();
      if (!paneRect) {
        return;
      }
      this.state.reviewWidth = clamp(paneRect.right - moveEvent.clientX, MIN_REVIEW_WIDTH, MAX_REVIEW_WIDTH);
      this.applyLayoutVars();
      this.emit();
      this.queueTerminalResize();
    };
    const stop = () => {
      document.body.classList.remove('is-resizing-review');
      window.removeEventListener('mousemove', move);
      window.removeEventListener('mouseup', stop);
      saveNumber(REVIEW_WIDTH_STORAGE_KEY, this.state.reviewWidth);
    };

    window.addEventListener('mousemove', move);
    window.addEventListener('mouseup', stop);
  }

  startFilesResize(event: React.MouseEvent<HTMLElement>): void {
    if (!this.state.reviewOpen) {
      return;
    }
    event.preventDefault();
    document.body.classList.add('is-resizing-files');

    const move = (moveEvent: MouseEvent) => {
      const reviewRect = this.reviewView?.getBoundingClientRect();
      if (!reviewRect) {
        return;
      }
      this.state.filesWidth = clamp(reviewRect.right - moveEvent.clientX, MIN_FILES_WIDTH, MAX_FILES_WIDTH);
      this.applyLayoutVars();
      this.emit();
    };
    const stop = () => {
      document.body.classList.remove('is-resizing-files');
      window.removeEventListener('mousemove', move);
      window.removeEventListener('mouseup', stop);
      saveNumber(FILES_WIDTH_STORAGE_KEY, this.state.filesWidth);
    };

    window.addEventListener('mousemove', move);
    window.addEventListener('mouseup', stop);
  }

  toggleReview(): void {
    this.state.reviewOpen = !this.state.reviewOpen;
    this.applyLayoutVars();
    this.setFilesOpen(this.state.filesOpen, false);
    this.emit();
    this.queueTerminalResize();
    if (this.state.reviewOpen) {
      void this.loadReviewDiff();
    }
    this.focusTerminalSoon();
  }

  setFilesOpen(open: boolean, persist = true): void {
    this.state.filesOpen = open;
    this.applyLayoutVars();
    if (persist) {
      saveBoolean(FILES_OPEN_STORAGE_KEY, open);
    }
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
    const key = selectionKey(selection);
    const previousSessionId = this.state.sessionId;
    const previousKnownSessionId = this.selectionSessions.get(key) || 0;

    this.state.selected = selection;
    this.emit();
    if (previousKnownSessionId === 0 || previousKnownSessionId !== previousSessionId) {
      this.state.terminalCopyOutput = '';
      this.state.terminalCopyStatus = '';
      this.showTerminalMessage(`Opening ${selection.tenant} / ${selection.environment}...`);
    }

    this.fitAddon?.fit();
    const result = (await StartSession(selection, this.terminal?.cols || 80, this.terminal?.rows || 24)) as StartSessionResult;
    this.selectionSessions.set(key, result.sessionId);
    this.openSessionSelections.set(result.sessionId, selection);
    this.state.sessionId = result.sessionId;

    if (result.sessionId !== previousSessionId) {
      this.resetTerminal();
      const buffer = this.sessionBuffers.get(result.sessionId);
      if (buffer) {
        this.writeTerminalBuffer(buffer);
      }
    }

    const exitReason = this.sessionExitReasons.get(result.sessionId);
    if (exitReason) {
      this.state.terminalCopyOutput = this.sessionExitOutputs.get(result.sessionId) || '';
      this.state.terminalCopyStatus = '';
      this.showTerminalMessage(exitReason);
    } else {
      this.hideTerminalMessage();
    }

    if (this.state.reviewOpen) {
      await this.loadReviewDiff();
    }
    this.focusTerminalSoon();
    this.queueTerminalResize();
    this.emit();
  }

  openInitializeDialog(): void {
    const tenantDefault = this.state.selected?.tenant || this.state.tenants[0]?.name || '';
    this.state.environmentDialog = {
      open: true,
      actionMode: 'init',
      tenant: tenantDefault,
      environment: '',
      version: this.state.versionSuggestions[0]?.version || '',
      noGit: false,
      versionImage: this.state.versionSuggestions[0]?.image || '',
      choicesOpen: false,
      busy: false,
      error: '',
    };
    this.emit();
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
    const tenant = normalizeDialogValue(dialog.tenant);
    const environment = normalizeDialogValue(dialog.environment);
    const version = normalizeDialogValue(dialog.version);

    if (!tenant || !environment || (dialog.actionMode === 'deploy' && !version)) {
      this.state.environmentDialog = { ...dialog, error: '' };
      this.emit();
      form.reportValidity();
      return;
    }

    const selection = {
      tenant,
      environment,
      version,
      runtimeImage: this.resolveEnvironmentRuntimeImage(version),
      noGit: dialog.noGit,
    };

    this.state.environmentDialog = {
      ...dialog,
      tenant,
      environment,
      version,
      busy: true,
      error: '',
      choicesOpen: false,
    };
    this.emit();

    const previousSelected = this.state.selected;
    try {
      if (dialog.actionMode === 'deploy') {
        await this.startDeploySelection(selection);
      } else {
        await this.startInitSelection(selection);
      }
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

  openManageDialog(selection: UISelection): void {
    this.state.manageDialog = {
      open: true,
      tab: 'config',
      selection,
      version: '',
      versionImage: '',
      config: {
        name: selection.environment,
        repoPath: '',
        kubernetesContext: '',
        containerRegistry: '',
        runtimeVersion: '',
        sshd: { enabled: false, localPort: 0, publicKeyPath: '' },
        remote: false,
        snapshot: true,
      },
      configLoading: true,
      confirmation: '',
      busy: false,
      choicesOpen: false,
      error: '',
    };
    this.emit();
    void this.refreshManageVersionSuggestions(false);
    void this.loadManageConfig();
  }

  closeManageDialog(): void {
    if (this.state.manageDialog.busy) {
      return;
    }
    this.state.manageDialog = defaultManageDialog();
    this.emit();
    this.focusTerminalSoon();
  }

  setManageTab(tab: ManageTab): void {
    if (this.state.manageDialog.busy) {
      return;
    }
    this.state.manageDialog = {
      ...this.state.manageDialog,
      tab,
      choicesOpen: false,
      error: '',
    };
    this.emit();
    if (tab === 'config' && !this.state.manageDialog.configLoading && this.state.manageDialog.selection) {
      void this.loadManageConfig();
    }
  }

  updateManageDialog(values: Partial<ManageDialogState>): void {
    if (this.state.manageDialog.busy) {
      return;
    }
    this.state.manageDialog = {
      ...this.state.manageDialog,
      ...values,
      error: values.error ?? '',
    };
    if (values.version !== undefined) {
      this.state.manageDialog.versionImage = '';
      this.state.manageDialog.choicesOpen = false;
    }
    this.emit();
  }

  toggleManageVersionChoices(): void {
    this.setManageVersionChoicesOpen(!this.state.manageDialog.choicesOpen);
  }

  setManageVersionChoicesOpen(open: boolean): void {
    if (this.state.manageDialog.busy) {
      return;
    }
    this.state.manageDialog = {
      ...this.state.manageDialog,
      choicesOpen: open && this.state.versionSuggestions.length > 0,
    };
    this.emit();
  }

  selectManageVersionSuggestion(suggestion: UIVersionSuggestion | undefined): void {
    if (this.state.manageDialog.busy) {
      return;
    }
    this.state.manageDialog = {
      ...this.state.manageDialog,
      version: suggestion?.version || '',
      versionImage: suggestion?.image || '',
      choicesOpen: false,
    };
    this.emit();
  }

  updateManageConfig(values: Partial<UIEnvironmentConfig>): void {
    if (this.state.manageDialog.busy || this.state.manageDialog.configLoading) {
      return;
    }
    this.state.manageDialog = {
      ...this.state.manageDialog,
      config: {
        ...this.state.manageDialog.config,
        ...values,
      },
      error: '',
    };
    this.emit();
  }

  updateManageSSHDConfig(values: Partial<UIEnvironmentConfig['sshd']>): void {
    if (this.state.manageDialog.busy || this.state.manageDialog.configLoading) {
      return;
    }
    this.state.manageDialog = {
      ...this.state.manageDialog,
      config: {
        ...this.state.manageDialog.config,
        sshd: {
          ...this.state.manageDialog.config.sshd,
          ...values,
        },
      },
      error: '',
    };
    this.emit();
  }

  async loadManageConfig(): Promise<void> {
    const dialog = this.state.manageDialog;
    const selection = dialog.selection;
    if (!dialog.open || !selection) {
      return;
    }
    this.state.manageDialog = {
      ...dialog,
      configLoading: true,
      error: '',
    };
    this.emit();
    try {
      const result = (await LoadEnvironmentConfig(selection)) as UIEnvironmentConfig;
      this.state.manageDialog = {
        ...this.state.manageDialog,
        config: result,
        configLoading: false,
        error: '',
      };
      this.emit();
    } catch (error) {
      this.state.manageDialog = {
        ...this.state.manageDialog,
        configLoading: false,
        error: readError(error),
      };
      this.emit();
    }
  }

  async submitManageConfig(): Promise<void> {
    const dialog = this.state.manageDialog;
    if (dialog.busy || dialog.configLoading) {
      return;
    }
    const selection = dialog.selection;
    if (!selection) {
      this.closeManageDialog();
      return;
    }

    this.state.manageDialog = { ...dialog, busy: true, error: '' };
    this.emit();
    try {
      const result = (await SaveEnvironmentConfig(selection, dialog.config)) as UIEnvironmentConfig;
      this.state.manageDialog = {
        ...this.state.manageDialog,
        config: result,
        busy: false,
        error: '',
      };
      this.showTerminalMessage(`Saved config for ${selection.tenant} / ${selection.environment}.`);
      this.closeManageDialog();
    } catch (error) {
      const message = readError(error);
      this.state.manageDialog = {
        ...this.state.manageDialog,
        busy: false,
        error: message,
      };
      this.showTerminalMessage(message);
      this.emit();
    }
  }

  openGlobalConfigDialog(): void {
    this.state.globalConfigDialog = {
      open: true,
      config: {
        defaultTenant: '',
      },
      configLoading: true,
      busy: false,
      error: '',
    };
    this.emit();
    void this.loadGlobalConfig();
  }

  closeGlobalConfigDialog(): void {
    if (this.state.globalConfigDialog.busy) {
      return;
    }
    this.state.globalConfigDialog = defaultGlobalConfigDialog();
    this.emit();
    this.focusTerminalSoon();
  }

  updateGlobalConfigDialog(values: Partial<GlobalConfigDialogState>): void {
    if (this.state.globalConfigDialog.busy) {
      return;
    }
    this.state.globalConfigDialog = {
      ...this.state.globalConfigDialog,
      ...values,
      error: values.error ?? '',
    };
    this.emit();
  }

  updateGlobalConfig(values: Partial<UIERunConfig>): void {
    if (this.state.globalConfigDialog.busy || this.state.globalConfigDialog.configLoading) {
      return;
    }
    this.updateGlobalConfigDialog({
      config: {
        ...this.state.globalConfigDialog.config,
        ...values,
      },
    });
  }

  async loadGlobalConfig(): Promise<void> {
    const dialog = this.state.globalConfigDialog;
    if (!dialog.open) {
      return;
    }
    this.state.globalConfigDialog = {
      ...dialog,
      configLoading: true,
      error: '',
    };
    this.emit();
    try {
      const result = (await LoadERunConfig()) as UIERunConfig;
      this.state.globalConfigDialog = {
        ...this.state.globalConfigDialog,
        config: result,
        configLoading: false,
        error: '',
      };
      this.emit();
    } catch (error) {
      this.state.globalConfigDialog = {
        ...this.state.globalConfigDialog,
        configLoading: false,
        error: readError(error),
      };
      this.emit();
    }
  }

  async submitGlobalConfig(): Promise<void> {
    const dialog = this.state.globalConfigDialog;
    if (dialog.busy || dialog.configLoading) {
      return;
    }
    this.state.globalConfigDialog = { ...dialog, busy: true, error: '' };
    this.emit();
    try {
      const result = (await SaveERunConfig(dialog.config)) as UIERunConfig;
      this.state.globalConfigDialog = {
        ...this.state.globalConfigDialog,
        config: result,
        busy: false,
        error: '',
      };
      this.showTerminalMessage('Saved ERun config.');
      this.closeGlobalConfigDialog();
    } catch (error) {
      const message = readError(error);
      this.state.globalConfigDialog = {
        ...this.state.globalConfigDialog,
        busy: false,
        error: message,
      };
      this.showTerminalMessage(message);
      this.emit();
    }
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
      this.showTerminalMessage(`Saved config for ${result.name}.`);
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
    const dialog = this.state.manageDialog;
    if (dialog.busy) {
      return;
    }
    const selection = dialog.selection;
    if (!selection) {
      this.closeManageDialog();
      return;
    }
    const version = normalizeDialogValue(dialog.version);
    this.closeManageDialog();
    await this.startDeploySelection({ ...selection, version, runtimeImage: version ? this.resolveManageRuntimeImage(version) : '' });
  }

  async submitManageDelete(): Promise<void> {
    const dialog = this.state.manageDialog;
    if (dialog.busy) {
      return;
    }
    const selection = dialog.selection;
    if (!selection) {
      this.closeManageDialog();
      return;
    }
    const confirmation = normalizeDialogValue(dialog.confirmation);
    const expected = deleteConfirmationValue(selection);
    if (confirmation !== expected) {
      return;
    }

    this.state.manageDialog = { ...dialog, busy: true, error: '' };
    this.state.terminalCopyOutput = '';
    this.state.terminalCopyStatus = '';
    this.showTerminalMessage(`Deleting ${selection.tenant} / ${selection.environment}...`);

    try {
      const result = (await DeleteEnvironment(selection, confirmation)) as DeleteEnvironmentResult;
      const deletedSelected = this.state.selected ? selectionKey(this.state.selected) === selectionKey(selection) : false;
      if (deletedSelected) {
        this.state.selected = null;
        this.state.sessionId = 0;
        this.resetTerminal();
      }
      await this.reloadStateAfterEnvironmentChange();
      this.state.manageDialog = defaultManageDialog();
      this.state.terminalCopyOutput = '';
      this.state.terminalCopyStatus = '';
      const warning = result.namespaceDeleteError ? ` Namespace deletion failed: ${result.namespaceDeleteError}` : '';
      this.showTerminalMessage(`Deleted ${result.tenant} / ${result.environment}.${warning}`);
    } catch (error) {
      const message = readError(error);
      this.state.manageDialog = { ...this.state.manageDialog, busy: false, error: message };
      this.state.terminalCopyOutput = `Failed to delete ${selection.tenant} / ${selection.environment}: ${message}`;
      this.state.terminalCopyStatus = '';
      this.showTerminalMessage(message);
      this.emit();
    }
  }

  setDiffFilter(value: string): void {
    this.state.diffFilter = value.trim().toLowerCase();
    this.emit();
  }

  async loadReviewDiff(): Promise<void> {
    if (!this.state.selected) {
      return;
    }
    this.state.diffLoading = true;
    this.state.diffError = '';
    this.emit();
    try {
      const diff = (await LoadDiff(this.state.selected)) as DiffResult;
      this.state.diff = diff;
      this.state.selectedDiffPath = chooseSelectedDiffPath(diff, this.state.selectedDiffPath);
    } catch (error: unknown) {
      this.state.diff = null;
      this.state.diffError = readError(error);
    } finally {
      this.state.diffLoading = false;
      this.emit();
    }
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

  showTerminalMessage(message: string): void {
    this.state.terminalMessage = message;
    this.emit();
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

  private async boot(): Promise<void> {
    try {
      const loaded = (await LoadState()) as UIState;
      this.state.tenants = loaded.tenants || [];
      this.state.selected = loaded.selected || null;
      this.state.versionSuggestions = normalizeVersionSuggestions(loaded.versionSuggestions || []);
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
    this.state.selected = selection;
    this.emit();
    this.state.terminalCopyOutput = '';
    this.state.terminalCopyStatus = '';
    this.showTerminalMessage(`Initializing ${selection.tenant} / ${selection.environment}...`);

    this.fitAddon?.fit();
    const result = (await StartInitSession(selection, this.terminal?.cols || 80, this.terminal?.rows || 24)) as StartSessionResult;
    this.initSessionSelections.set(result.sessionId, selection);
    this.state.sessionId = result.sessionId;

    this.resetTerminal();
    this.hideTerminalMessage();
    this.focusTerminalSoon();
    this.queueTerminalResize();
    this.emit();
  }

  private async startDeploySelection(selection: UISelection): Promise<void> {
    this.state.selected = selection;
    this.emit();
    this.state.terminalCopyOutput = '';
    this.state.terminalCopyStatus = '';
    this.showTerminalMessage(`Deploying ${selection.tenant} / ${selection.environment}...`);

    this.fitAddon?.fit();
    const result = (await StartDeploySession(selection, this.terminal?.cols || 80, this.terminal?.rows || 24)) as StartSessionResult;
    this.deploySessionSelections.set(result.sessionId, selection);
    this.state.sessionId = result.sessionId;

    this.resetTerminal();
    this.hideTerminalMessage();
    this.focusTerminalSoon();
    this.queueTerminalResize();
    this.emit();
  }

  private async reloadStateAfterEnvironmentChange(): Promise<void> {
    try {
      const loaded = (await LoadState()) as UIState;
      this.state.tenants = loaded.tenants || [];
      this.state.versionSuggestions = normalizeVersionSuggestions(loaded.versionSuggestions || this.state.versionSuggestions);
      this.emit();
    } catch {
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

  private async refreshManageVersionSuggestions(selectDefault: boolean): Promise<void> {
    const selection = this.state.manageDialog.selection;
    if (!selection) {
      return;
    }
    const request = ++this.versionSuggestionRequest;
    const suggestions = normalizeVersionSuggestions((await LoadVersionSuggestions(selection)) as UIVersionSuggestion[]);
    if (request !== this.versionSuggestionRequest || !this.state.manageDialog.open) {
      return;
    }

    this.state.versionSuggestions = suggestions;
    const currentVersion = normalizeDialogValue(this.state.manageDialog.version);
    if (!currentVersion && !selectDefault) {
      this.emit();
    } else if (selectDefault || !suggestions.some((suggestion) => suggestion.version === currentVersion)) {
      this.selectManageVersionSuggestion(suggestions[0]);
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
    this.state.terminalCopyOutput = '';
    this.state.terminalCopyStatus = '';
    this.emit();
  }

  private handleTerminalOutput(payload: TerminalOutputPayload): void {
    if (!payload) {
      return;
    }
    const data = decodeBase64Bytes(payload.data);
    const existing = this.sessionBuffers.get(payload.sessionId) || [];
    existing.push(data);
    this.sessionBuffers.set(payload.sessionId, existing);
    if (payload.sessionId !== this.state.sessionId) {
      return;
    }
    this.terminal?.write(data);
  }

  private async handleTerminalExit(payload: TerminalExitPayload): Promise<void> {
    if (!payload) {
      return;
    }
    const initSelection = this.initSessionSelections.get(payload.sessionId);
    const deploySelection = this.deploySessionSelections.get(payload.sessionId);
    const openSelection = this.openSessionSelections.get(payload.sessionId);
    this.initSessionSelections.delete(payload.sessionId);
    this.deploySessionSelections.delete(payload.sessionId);
    this.openSessionSelections.delete(payload.sessionId);

    const reason = this.terminalExitReason(payload, initSelection, deploySelection, openSelection);
    this.sessionExitReasons.set(payload.sessionId, reason);
    const failedOutput = payload.reason && (initSelection || deploySelection || openSelection)
      ? this.failedTerminalOutput(payload.sessionId, reason)
      : '';
    if (failedOutput) {
      this.sessionExitOutputs.set(payload.sessionId, failedOutput);
    }
    if (initSelection || deploySelection) {
      await this.reloadStateAfterEnvironmentChange();
    }
    if (payload.sessionId !== this.state.sessionId) {
      return;
    }
    if ((initSelection || deploySelection) && !payload.reason) {
      try {
        await this.openSelection(initSelection || deploySelection);
      } catch (error) {
        this.showTerminalMessage(readError(error));
      }
      return;
    }
    if (payload.reason && (initSelection || deploySelection || openSelection)) {
      this.state.terminalCopyOutput = failedOutput;
      this.state.terminalCopyStatus = '';
    }
    this.showTerminalMessage(reason);
  }

  private terminalExitReason(
    payload: TerminalExitPayload,
    initSelection?: UISelection,
    deploySelection?: UISelection,
    openSelection?: UISelection,
  ): string {
    if (payload.reason) {
      if (initSelection) {
        return `Failed to create ${initSelection.tenant} / ${initSelection.environment}: ${payload.reason}`;
      }
      if (deploySelection) {
        return `Failed to deploy ${deploySelection.tenant} / ${deploySelection.environment}: ${payload.reason}`;
      }
      if (openSelection) {
        return `Failed to open ${openSelection.tenant} / ${openSelection.environment}: ${payload.reason}`;
      }
      return payload.reason;
    }
    if (initSelection) {
      return `Created ${initSelection.tenant} / ${initSelection.environment}.`;
    }
    if (deploySelection) {
      return `Deployed ${deploySelection.tenant} / ${deploySelection.environment}.`;
    }
    return 'Session ended.';
  }

  private failedTerminalOutput(sessionId: number, fallback: string): string {
    const chunks = this.sessionBuffers.get(sessionId) || [];
    const decoder = new TextDecoder();
    const output = chunks.map((chunk) => decoder.decode(chunk, { stream: true })).join('') + decoder.decode();
    return cleanTerminalOutput(output) || fallback;
  }

  private applyLayoutVars(): void {
    const root = document.documentElement;
    root.style.setProperty('--sidebar-width', `${this.state.sidebarHidden ? 0 : this.state.sidebarWidth}px`);

    const paneWidth = this.terminalPane?.getBoundingClientRect().width || 0;
    const maxReviewForPane = paneWidth > 0 ? paneWidth - 370 : MAX_REVIEW_WIDTH;
    const reviewMaximum = Math.max(MIN_REVIEW_WIDTH, Math.min(MAX_REVIEW_WIDTH, maxReviewForPane));
    root.style.setProperty('--review-width', `${clamp(this.state.reviewWidth, MIN_REVIEW_WIDTH, reviewMaximum)}px`);

    const reviewWidth = this.reviewView?.getBoundingClientRect().width || this.state.reviewWidth;
    const maxFilesForReview = reviewWidth > 0 ? reviewWidth - 260 : MAX_FILES_WIDTH;
    const filesMaximum = Math.max(MIN_FILES_WIDTH, Math.min(MAX_FILES_WIDTH, maxFilesForReview));
    root.style.setProperty('--files-width', `${clamp(this.state.filesWidth, MIN_FILES_WIDTH, filesMaximum)}px`);
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
    if (!this.state.selectedDiffPath || !this.diffList) {
      return;
    }
    const selector = `[data-path="${cssEscape(this.state.selectedDiffPath)}"]`;
    this.diffList.querySelector<HTMLElement>(selector)?.scrollIntoView({ block: 'start', behavior: 'smooth' });
  }

  private updateSelectedDiffPathFromScroll(): void {
    const path = this.visibleDiffPath();
    if (!path || path === this.state.selectedDiffPath) {
      return;
    }
    this.state.selectedDiffPath = path;
    this.emit();
  }

  private visibleDiffPath(): string {
    if (!this.diffList || !this.reviewMain) {
      return '';
    }
    const sections = Array.from(this.diffList.querySelectorAll<HTMLElement>('.diff-file[data-path]'));
    if (sections.length === 0) {
      return '';
    }

    const containerRect = this.reviewMain.getBoundingClientRect();
    const anchor = containerRect.top + 72;
    let closestPath = '';
    let closestDistance = Number.POSITIVE_INFINITY;

    for (const section of sections) {
      const rect = section.getBoundingClientRect();
      const path = section.dataset.path || '';
      if (!path) {
        continue;
      }
      if (rect.top <= anchor && rect.bottom > anchor) {
        return path;
      }
      const distance = Math.abs(rect.top - anchor);
      if (distance < closestDistance) {
        closestDistance = distance;
        closestPath = path;
      }
    }
    return closestPath;
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

  private writeTerminalBuffer(chunks: Uint8Array[]): void {
    for (const chunk of chunks) {
      this.terminal?.write(chunk);
    }
  }
}

function cleanTerminalOutput(value: string): string {
  return value
    .replace(/\x1B\][^\x07]*(?:\x07|\x1B\\)/g, '')
    .replace(/\x1B(?:[@-Z\\-_]|\[[0-?]*[ -/]*[@-~])/g, '')
    .replace(/\r\n/g, '\n')
    .replace(/\r/g, '\n')
    .trim();
}
