import * as React from 'react';
import {
  Check,
  ChevronDown,
  ChevronRight,
  ChevronsUpDown,
  File,
  FileCode2,
  FileCog,
  FileDiff,
  FileJson,
  FileText,
  Folder,
  FolderOpen,
  FolderPlus,
  Gem,
  ListTree,
  MoreHorizontal,
  PanelLeftClose,
  PanelLeftOpen,
  PanelRightClose,
  PanelRightOpen,
  RefreshCw,
  Rocket,
  Search,
  Trash2,
} from 'lucide-react';
import { FitAddon } from '@xterm/addon-fit';
import { Terminal } from '@xterm/xterm';

import {
  DeleteEnvironment,
  LoadDiff,
  LoadState,
  LoadVersionSuggestions,
  ResizeSession,
  SavePastedImage,
  SendSessionInput,
  StartDeploySession,
  StartInitSession,
  StartSession,
} from '../wailsjs/go/main/App';
import { EventsOn, WindowToggleMaximise } from '../wailsjs/runtime/runtime';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from '@/components/ui/command';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip';
import { cn } from '@/lib/utils';
import type {
  DeleteEnvironmentResult,
  DiffFile,
  DiffHunk,
  DiffLine,
  DiffResult,
  DiffTreeNode,
  EnvironmentActionMode,
  ManageTab,
  PastedImageResult,
  StartSessionResult,
  TerminalExitPayload,
  TerminalOutputPayload,
  UISelection,
  UIState,
  UITenant,
  UIVersionSuggestion,
} from '@/types';

const MIN_SIDEBAR_WIDTH = 248;
const MAX_SIDEBAR_WIDTH = 520;
const DEFAULT_SIDEBAR_WIDTH = 338;
const MIN_REVIEW_WIDTH = 420;
const MAX_REVIEW_WIDTH = 920;
const DEFAULT_REVIEW_WIDTH = 620;
const MIN_FILES_WIDTH = 220;
const MAX_FILES_WIDTH = 460;
const DEFAULT_FILES_WIDTH = 300;
const SIDEBAR_WIDTH_STORAGE_KEY = 'erun.sidebarWidth';
const REVIEW_WIDTH_STORAGE_KEY = 'erun.reviewWidth';
const FILES_WIDTH_STORAGE_KEY = 'erun.filesWidth';
const FILES_OPEN_STORAGE_KEY = 'erun.filesOpen';

interface EnvironmentDialogState {
  open: boolean;
  actionMode: EnvironmentActionMode;
  tenant: string;
  environment: string;
  version: string;
  noGit: boolean;
  versionImage: string;
  choicesOpen: boolean;
}

interface ManageDialogState {
  open: boolean;
  tab: ManageTab;
  selection: UISelection | null;
  version: string;
  versionImage: string;
  confirmation: string;
  busy: boolean;
  choicesOpen: boolean;
}

interface AppState {
  tenants: UITenant[];
  selected: UISelection | null;
  versionSuggestions: UIVersionSuggestion[];
  environmentDialog: EnvironmentDialogState;
  manageDialog: ManageDialogState;
  collapsedTenants: Set<string>;
  sessionId: number;
  sidebarWidth: number;
  reviewWidth: number;
  filesWidth: number;
  filesOpen: boolean;
  sidebarHidden: boolean;
  reviewOpen: boolean;
  diff: DiffResult | null;
  diffLoading: boolean;
  diffError: string;
  selectedDiffPath: string;
  diffFilter: string;
  collapsedDiffDirs: Set<string>;
  terminalMessage: string;
}

interface MountElements {
  terminalRoot: HTMLDivElement;
  terminalPane: HTMLElement;
  reviewView: HTMLElement;
  reviewMain: HTMLDivElement;
  diffList: HTMLDivElement;
}

type TerminalDataDisposable = ReturnType<Terminal['onData']>;

const defaultEnvironmentDialog = (): EnvironmentDialogState => ({
  open: false,
  actionMode: 'init',
  tenant: '',
  environment: '',
  version: '',
  noGit: false,
  versionImage: '',
  choicesOpen: false,
});

const defaultManageDialog = (): ManageDialogState => ({
  open: false,
  tab: 'deploy',
  selection: null,
  version: '',
  versionImage: '',
  confirmation: '',
  busy: false,
  choicesOpen: false,
});

class ERunUIController {
  readonly state: AppState = {
    tenants: [],
    selected: null,
    versionSuggestions: [],
    environmentDialog: defaultEnvironmentDialog(),
    manageDialog: defaultManageDialog(),
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
  };

  private readonly subscribers = new Set<() => void>();
  private readonly initSessionIds = new Set<number>();
  private readonly deploySessionIds = new Set<number>();
  private readonly selectionSessions = new Map<string, number>();
  private readonly sessionBuffers = new Map<number, Uint8Array[]>();
  private readonly sessionExitReasons = new Map<number, string>();
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
    window.setTimeout(() => this.terminal?.focus(), 0);
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
    window.setTimeout(() => this.terminal?.focus(), 0);
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
      this.showTerminalMessage(`Opening ${selection.tenant} / ${selection.environment}...`);
    }

    this.fitAddon?.fit();
    const result = (await StartSession(selection, this.terminal?.cols || 80, this.terminal?.rows || 24)) as StartSessionResult;
    this.selectionSessions.set(key, result.sessionId);
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
      this.showTerminalMessage(exitReason);
    } else {
      this.hideTerminalMessage();
    }

    if (this.state.reviewOpen) {
      await this.loadReviewDiff();
    }
    this.terminal?.focus();
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
    };
    this.emit();
    void this.refreshDialogVersionSuggestions(true);
  }

  closeEnvironmentDialog(): void {
    this.state.environmentDialog = defaultEnvironmentDialog();
    this.emit();
    window.setTimeout(() => this.terminal?.focus(), 0);
  }

  updateEnvironmentDialog(values: Partial<EnvironmentDialogState>): void {
    this.state.environmentDialog = {
      ...this.state.environmentDialog,
      ...values,
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
    this.state.environmentDialog = {
      ...this.state.environmentDialog,
      choicesOpen: open && this.state.versionSuggestions.length > 0,
    };
    this.emit();
  }

  selectEnvironmentVersionSuggestion(suggestion: UIVersionSuggestion | undefined): void {
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
    const tenant = normalizeDialogValue(dialog.tenant);
    const environment = normalizeDialogValue(dialog.environment);
    const version = normalizeDialogValue(dialog.version);

    if (!tenant || !environment || (dialog.actionMode === 'deploy' && !version)) {
      form.reportValidity();
      return;
    }

    this.closeEnvironmentDialog();
    const selection = {
      tenant,
      environment,
      version,
      runtimeImage: this.resolveEnvironmentRuntimeImage(version),
      noGit: dialog.noGit,
    };
    if (dialog.actionMode === 'deploy') {
      await this.startDeploySelection(selection);
      return;
    }
    await this.startInitSelection(selection);
  }

  openManageDialog(selection: UISelection): void {
    const suggestion = this.state.versionSuggestions[0];
    this.state.manageDialog = {
      open: true,
      tab: 'deploy',
      selection,
      version: suggestion?.version || '',
      versionImage: suggestion?.image || '',
      confirmation: '',
      busy: false,
      choicesOpen: false,
    };
    this.emit();
    void this.refreshManageVersionSuggestions(true);
  }

  closeManageDialog(): void {
    if (this.state.manageDialog.busy) {
      return;
    }
    this.state.manageDialog = defaultManageDialog();
    this.emit();
    window.setTimeout(() => this.terminal?.focus(), 0);
  }

  setManageTab(tab: ManageTab): void {
    if (this.state.manageDialog.busy) {
      return;
    }
    this.state.manageDialog = {
      ...this.state.manageDialog,
      tab,
      choicesOpen: false,
    };
    this.emit();
  }

  updateManageDialog(values: Partial<ManageDialogState>): void {
    this.state.manageDialog = {
      ...this.state.manageDialog,
      ...values,
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
    this.state.manageDialog = {
      ...this.state.manageDialog,
      choicesOpen: open && this.state.versionSuggestions.length > 0,
    };
    this.emit();
  }

  selectManageVersionSuggestion(suggestion: UIVersionSuggestion | undefined): void {
    this.state.manageDialog = {
      ...this.state.manageDialog,
      version: suggestion?.version || '',
      versionImage: suggestion?.image || '',
      choicesOpen: false,
    };
    this.emit();
  }

  async submitManageDeploy(form?: HTMLFormElement): Promise<void> {
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
    if (!version) {
      form?.reportValidity();
      return;
    }
    this.closeManageDialog();
    await this.startDeploySelection({ ...selection, version, runtimeImage: this.resolveManageRuntimeImage(version) });
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

    this.state.manageDialog = { ...dialog, busy: true };
    this.showTerminalMessage(`Deleting ${selection.tenant} / ${selection.environment}...`);

    try {
      const result = (await DeleteEnvironment(selection, confirmation)) as DeleteEnvironmentResult;
      this.state.selected = null;
      await this.reloadStateAfterEnvironmentChange();
      this.state.manageDialog = defaultManageDialog();
      const warning = result.namespaceDeleteError ? ` Namespace deletion failed: ${result.namespaceDeleteError}` : '';
      this.showTerminalMessage(`Deleted ${result.tenant} / ${result.environment}.${warning}`);
    } catch (error) {
      this.state.manageDialog = { ...this.state.manageDialog, busy: false };
      this.showTerminalMessage(readError(error));
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
    this.showTerminalMessage(`Initializing ${selection.tenant} / ${selection.environment}...`);

    this.fitAddon?.fit();
    const result = (await StartInitSession(selection, this.terminal?.cols || 80, this.terminal?.rows || 24)) as StartSessionResult;
    this.initSessionIds.add(result.sessionId);
    this.state.sessionId = result.sessionId;

    this.resetTerminal();
    this.hideTerminalMessage();
    this.terminal?.focus();
    this.queueTerminalResize();
    this.emit();
  }

  private async startDeploySelection(selection: UISelection): Promise<void> {
    this.state.selected = selection;
    this.emit();
    this.showTerminalMessage(`Deploying ${selection.tenant} / ${selection.environment}...`);

    this.fitAddon?.fit();
    const result = (await StartDeploySession(selection, this.terminal?.cols || 80, this.terminal?.rows || 24)) as StartSessionResult;
    this.deploySessionIds.add(result.sessionId);
    this.state.sessionId = result.sessionId;

    this.resetTerminal();
    this.hideTerminalMessage();
    this.terminal?.focus();
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
    if (selectDefault || !suggestions.some((suggestion) => suggestion.version === currentVersion)) {
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
    this.sessionExitReasons.set(payload.sessionId, payload.reason || 'Session ended.');
    const initSessionEnded = this.initSessionIds.delete(payload.sessionId);
    const deploySessionEnded = this.deploySessionIds.delete(payload.sessionId);
    if (initSessionEnded || deploySessionEnded) {
      await this.reloadStateAfterEnvironmentChange();
    }
    if (payload.sessionId !== this.state.sessionId) {
      return;
    }
    this.showTerminalMessage(payload.reason || 'Session ended.');
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
    this.terminal?.focus();
  }

  private writeTerminalBuffer(chunks: Uint8Array[]): void {
    for (const chunk of chunks) {
      this.terminal?.write(chunk);
    }
  }
}

export function App(): React.ReactElement {
  const controller = React.useMemo(() => new ERunUIController(), []);
  const state = useControllerState(controller);
  const terminalRootRef = React.useRef<HTMLDivElement>(null);
  const terminalPaneRef = React.useRef<HTMLElement>(null);
  const reviewViewRef = React.useRef<HTMLElement>(null);
  const reviewMainRef = React.useRef<HTMLDivElement>(null);
  const diffListRef = React.useRef<HTMLDivElement>(null);

  React.useEffect(() => {
    if (!terminalRootRef.current || !terminalPaneRef.current || !reviewViewRef.current || !reviewMainRef.current || !diffListRef.current) {
      return undefined;
    }
    return controller.mount({
      terminalRoot: terminalRootRef.current,
      terminalPane: terminalPaneRef.current,
      reviewView: reviewViewRef.current,
      reviewMain: reviewMainRef.current,
      diffList: diffListRef.current,
    });
  }, [controller]);

  return (
    <TooltipProvider>
      <div className={cn('app-shell', state.sidebarHidden && 'sidebar-hidden')}>
        <Titlebar controller={controller} state={state} />
        <div className="workbench">
          <Sidebar controller={controller} state={state} />
          <div
            className="splitter"
            role="separator"
            aria-orientation="vertical"
            aria-label="Resize sidebar"
            onMouseDown={(event) => controller.startSidebarResize(event)}
          />
          <main
            ref={terminalPaneRef}
            className={cn('terminal-pane', state.reviewOpen && 'has-review-panel')}
          >
            <div className="terminal-view">
              <div ref={terminalRootRef} className="terminal" />
              <div className={cn('terminal-message', !state.terminalMessage && 'is-hidden')}>
                {state.terminalMessage}
              </div>
            </div>
            <div
              className={cn('review-splitter', !state.reviewOpen && 'is-hidden')}
              role="separator"
              aria-orientation="vertical"
              aria-label="Resize diff panel"
              onMouseDown={(event) => controller.startReviewResize(event)}
            />
            <ReviewPanel
              controller={controller}
              state={state}
              reviewViewRef={reviewViewRef}
              reviewMainRef={reviewMainRef}
              diffListRef={diffListRef}
            />
          </main>
        </div>
      </div>
      <EnvironmentDialogView controller={controller} state={state} />
      <ManageDialogView controller={controller} state={state} />
    </TooltipProvider>
  );
}

function useControllerState(controller: ERunUIController): AppState {
  const [, setVersion] = React.useState(0);

  React.useEffect(() => controller.subscribe(() => {
    setVersion((version) => version + 1);
  }), [controller]);

  return controller.state;
}

function Titlebar({ controller, state }: { controller: ERunUIController; state: AppState }): React.ReactElement {
  const SidebarIcon = state.sidebarHidden ? PanelLeftOpen : PanelLeftClose;
  const ReviewIcon = state.reviewOpen ? PanelRightClose : PanelRightOpen;

  return (
    <header
      className="titlebar"
      data-wails-drag
      onDoubleClick={(event) => controller.titlebarDoubleClick(event)}
    >
      <IconTooltip label="Toggle sidebar">
        <Button
          className="titlebar-button"
          type="button"
          variant="ghost"
          size="icon"
          aria-label="Toggle sidebar"
          aria-pressed={!state.sidebarHidden}
          onClick={() => controller.toggleSidebar()}
        >
          <SidebarIcon />
        </Button>
      </IconTooltip>
      <IconTooltip label="Toggle diff panel">
        <Button
          className={cn('titlebar-button titlebar-button-right', state.reviewOpen && 'is-active')}
          type="button"
          variant="ghost"
          size="icon"
          aria-label="Toggle diff panel"
          aria-pressed={state.reviewOpen}
          onClick={() => controller.toggleReview()}
        >
          <ReviewIcon />
        </Button>
      </IconTooltip>
      <IconTooltip label="Toggle changed files list">
        <Button
          className={cn(
            'titlebar-button titlebar-button-right titlebar-button-files',
            !state.reviewOpen && 'is-hidden',
            state.filesOpen && 'is-active',
          )}
          type="button"
          variant="ghost"
          size="icon"
          aria-label="Toggle changed files list"
          aria-pressed={state.filesOpen}
          onClick={() => controller.setFilesOpen(!state.filesOpen)}
        >
          <ListTree />
        </Button>
      </IconTooltip>
      <div className="titlebar-fill" data-wails-drag />
    </header>
  );
}

function Sidebar({ controller, state }: { controller: ERunUIController; state: AppState }): React.ReactElement {
  return (
    <aside className="sidebar">
      <div className="sidebar-header">
        <span className="sidebar-title">Environments</span>
        <IconTooltip label="Initialize new remote environment">
          <Button
            className="sidebar-icon-button"
            type="button"
            variant="ghost"
            size="icon"
            aria-label="Initialize new remote environment"
            onClick={() => controller.openInitializeDialog()}
          >
            <FolderPlus />
          </Button>
        </IconTooltip>
      </div>
      <div className="sidebar-list">
        {state.tenants.length === 0 ? (
          <div className="sidebar-empty">No environments</div>
        ) : (
          state.tenants.map((tenant, index) => (
            <TenantGroup
              key={tenant.name}
              controller={controller}
              state={state}
              tenant={tenant}
              spaced={index > 0}
            />
          ))
        )}
      </div>
    </aside>
  );
}

function TenantGroup({
  controller,
  state,
  tenant,
  spaced,
}: {
  controller: ERunUIController;
  state: AppState;
  tenant: UITenant;
  spaced: boolean;
}): React.ReactElement {
  const collapsed = state.collapsedTenants.has(tenant.name);

  return (
    <div className={cn('tenant-group', spaced && 'tenant-group-spaced')}>
      <button className="tenant-row" type="button" onClick={() => controller.toggleTenant(tenant.name)}>
        {collapsed ? (
          <Folder className="folder-icon" aria-hidden="true" />
        ) : (
          <FolderOpen className="folder-icon" aria-hidden="true" />
        )}
        <span>{tenant.name}</span>
      </button>
      {!collapsed && (
        <div className="environment-list">
          {tenant.environments.map((environment) => {
            const selected = state.selected?.tenant === tenant.name && state.selected?.environment === environment.name;
            const selection = { tenant: tenant.name, environment: environment.name };

            return (
              <div key={environment.name} className={cn('environment-row', selected && 'is-selected')}>
                <button
                  type="button"
                  className="environment-open"
                  onClick={() => {
                    void controller.openSelection(selection).catch((error: unknown) => {
                      controller.showTerminalMessage(readError(error));
                    });
                  }}
                >
                  {environment.name}
                </button>
                <IconTooltip label="Manage environment">
                  <Button
                    type="button"
                    className="environment-manage"
                    variant="ghost"
                    size="icon"
                    aria-label={`Manage ${tenant.name} / ${environment.name}`}
                    onClick={(event) => {
                      event.stopPropagation();
                      controller.openManageDialog(selection);
                    }}
                  >
                    <MoreHorizontal />
                  </Button>
                </IconTooltip>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}

function EnvironmentDialogView({ controller, state }: { controller: ERunUIController; state: AppState }): React.ReactElement {
  const dialog = state.environmentDialog;
  const tenantRef = React.useRef<HTMLInputElement>(null);
  const environmentRef = React.useRef<HTMLInputElement>(null);

  React.useEffect(() => {
    if (!dialog.open) {
      return;
    }
    window.setTimeout(() => {
      const target = dialog.tenant ? environmentRef.current : tenantRef.current;
      target?.focus();
      target?.select();
    }, 0);
  }, [dialog.open, dialog.tenant]);

  return (
    <Dialog open={dialog.open} onOpenChange={(open) => !open && controller.closeEnvironmentDialog()}>
      <DialogContent className="sm:max-w-md">
        <form
          className="grid gap-4"
          onSubmit={(event) => {
            event.preventDefault();
            void controller.submitEnvironmentDialog(event.currentTarget).catch((error: unknown) => {
              controller.showTerminalMessage(readError(error));
            });
          }}
        >
          <DialogHeader>
            <DialogTitle>
              {dialog.actionMode === 'deploy' ? 'Deploy environment' : 'New environment'}
            </DialogTitle>
          </DialogHeader>
          <div className="grid gap-2">
            <Label htmlFor="environment-tenant">Tenant</Label>
            <Input
              id="environment-tenant"
              ref={tenantRef}
              value={dialog.tenant}
              type="text"
              autoComplete="off"
              spellCheck={false}
              required
              disabled={dialog.actionMode === 'deploy'}
              onChange={(event) => controller.updateEnvironmentDialog({ tenant: event.target.value })}
            />
          </div>
          <div className="grid gap-2">
            <Label htmlFor="environment-name">Environment</Label>
            <Input
              id="environment-name"
              ref={environmentRef}
              value={dialog.environment}
              type="text"
              autoComplete="off"
              spellCheck={false}
              required
              disabled={dialog.actionMode === 'deploy'}
              onChange={(event) => controller.updateEnvironmentDialog({ environment: event.target.value })}
            />
          </div>
          <VersionField
            id="environment-version"
            value={dialog.version}
            sourceText={selectedVersionSourceText(findVersionSuggestion(state.versionSuggestions, dialog.version, dialog.versionImage))}
            suggestions={state.versionSuggestions}
            choicesOpen={dialog.choicesOpen}
            required={dialog.actionMode === 'deploy'}
            onValueChange={(version) => controller.updateEnvironmentDialog({ version })}
            onChoicesOpenChange={(open) => controller.setEnvironmentVersionChoicesOpen(open)}
            onSelect={(suggestion) => controller.selectEnvironmentVersionSuggestion(suggestion)}
          />
          <div className="flex items-center gap-2">
            <Checkbox
              id="environment-no-git"
              checked={dialog.noGit}
              onCheckedChange={(checked) => controller.updateEnvironmentDialog({ noGit: checked === true })}
            />
            <Label htmlFor="environment-no-git" className="text-sm font-normal">
              Initialize without Git checkout
            </Label>
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" size="sm" onClick={() => controller.closeEnvironmentDialog()}>
              Cancel
            </Button>
            <Button type="submit" size="sm">
              Start enrollment
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function ManageDialogView({ controller, state }: { controller: ERunUIController; state: AppState }): React.ReactElement {
  const dialog = state.manageDialog;
  const versionRef = React.useRef<HTMLInputElement>(null);
  const confirmationRef = React.useRef<HTMLInputElement>(null);
  const selection = dialog.selection;
  const expected = selection ? deleteConfirmationValue(selection) : '';
  const deleteEnabled = !dialog.busy && normalizeDialogValue(dialog.confirmation) === expected;

  React.useEffect(() => {
    if (!dialog.open) {
      return;
    }
    window.setTimeout(() => {
      if (dialog.tab === 'deploy') {
        versionRef.current?.focus();
        versionRef.current?.select();
        return;
      }
      confirmationRef.current?.focus();
    }, 0);
  }, [dialog.open, dialog.tab]);

  return (
    <Dialog open={dialog.open} onOpenChange={(open) => !open && controller.closeManageDialog()}>
      <DialogContent className="sm:max-w-md">
        <form
          className="grid gap-4"
          onSubmit={(event) => {
            event.preventDefault();
            if (dialog.tab === 'deploy') {
              void controller.submitManageDeploy(event.currentTarget).catch((error: unknown) => {
                controller.showTerminalMessage(readError(error));
              });
            }
          }}
        >
          <DialogHeader>
            <DialogTitle>Manage environment</DialogTitle>
            <DialogDescription>
              {selection ? `${selection.tenant} / ${selection.environment}` : ''}
            </DialogDescription>
          </DialogHeader>
          <Tabs
            className="gap-3"
            value={dialog.tab}
            onValueChange={(value) => controller.setManageTab(value as ManageTab)}
          >
            <TabsList className="grid w-full grid-cols-2" aria-label="Environment actions">
              <TabsTrigger value="deploy" disabled={dialog.busy}>
                Deploy
              </TabsTrigger>
              <TabsTrigger value="delete" disabled={dialog.busy}>
                Delete
              </TabsTrigger>
            </TabsList>
            <div className="min-h-20">
              <TabsContent className="mt-0 grid gap-3" value="deploy">
                <VersionField
                  id="manage-version"
                  inputRef={versionRef}
                  value={dialog.version}
                  sourceText={selectedVersionSourceText(findVersionSuggestion(state.versionSuggestions, dialog.version, dialog.versionImage))}
                  suggestions={state.versionSuggestions}
                  choicesOpen={dialog.choicesOpen}
                  required
                  disabled={dialog.busy}
                  onValueChange={(version) => controller.updateManageDialog({ version })}
                  onChoicesOpenChange={(open) => controller.setManageVersionChoicesOpen(open)}
                  onSelect={(suggestion) => controller.selectManageVersionSuggestion(suggestion)}
                />
              </TabsContent>
              <TabsContent className="mt-0 grid gap-3" value="delete">
                <p className="text-sm text-muted-foreground">
                  {selection ? `Type ${expected} to delete ${selection.tenant} / ${selection.environment}.` : ''}
                </p>
                <div className="grid gap-2">
                  <Label htmlFor="manage-confirmation">Confirmation</Label>
                  <Input
                    id="manage-confirmation"
                    ref={confirmationRef}
                    value={dialog.confirmation}
                    type="text"
                    autoComplete="off"
                    spellCheck={false}
                    disabled={dialog.busy}
                    onChange={(event) => controller.updateManageDialog({ confirmation: event.target.value })}
                    onKeyDown={(event) => {
                      if (event.key === 'Enter') {
                        event.preventDefault();
                        void controller.submitManageDelete();
                      }
                    }}
                  />
                </div>
              </TabsContent>
            </div>
          </Tabs>
          <DialogFooter>
            <Button type="button" variant="outline" size="sm" disabled={dialog.busy} onClick={() => controller.closeManageDialog()}>
              Cancel
            </Button>
            {dialog.tab === 'deploy' ? (
              <Button type="submit" size="sm" disabled={dialog.busy}>
                <Rocket aria-hidden="true" />
                Deploy
              </Button>
            ) : (
              <Button
                type="button"
                variant={deleteEnabled || dialog.busy ? 'destructive' : 'outline'}
                size="sm"
                disabled={!deleteEnabled}
                onClick={() => {
                  void controller.submitManageDelete();
                }}
              >
                <Trash2 aria-hidden="true" />
                {dialog.busy ? 'Deleting...' : 'Delete'}
              </Button>
            )}
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function VersionField({
  id,
  inputRef,
  value,
  sourceText,
  suggestions,
  choicesOpen,
  required,
  disabled,
  onValueChange,
  onChoicesOpenChange,
  onSelect,
}: {
  id: string;
  inputRef?: React.Ref<HTMLInputElement>;
  value: string;
  sourceText: string;
  suggestions: UIVersionSuggestion[];
  choicesOpen: boolean;
  required?: boolean;
  disabled?: boolean;
  onValueChange: (version: string) => void;
  onChoicesOpenChange: (open: boolean) => void;
  onSelect: (suggestion: UIVersionSuggestion | undefined) => void;
}): React.ReactElement {
  return (
    <div className="grid gap-2">
      <Label htmlFor={id}>Runtime version</Label>
      <div className="relative">
        <Input
          id={id}
          ref={inputRef}
          className="pr-10"
          value={value}
          type="text"
          autoComplete="off"
          spellCheck={false}
          required={required}
          disabled={disabled}
          onChange={(event) => onValueChange(event.target.value)}
        />
        <Popover open={choicesOpen} onOpenChange={onChoicesOpenChange}>
          <PopoverTrigger asChild>
            <Button
              className="absolute right-1 top-1 size-7 text-muted-foreground"
              type="button"
              variant="ghost"
              size="icon"
              aria-label="Show version choices"
              disabled={disabled}
            >
              <ChevronsUpDown />
            </Button>
          </PopoverTrigger>
          <PopoverContent className="w-80 p-0" align="start">
            <Command>
              <CommandInput placeholder="Search versions..." />
              <CommandList>
                <CommandEmpty>No version found.</CommandEmpty>
                <CommandGroup>
                  {suggestions.map((suggestion) => {
                    const selected = suggestion.version === value;
                    return (
                      <CommandItem
                        className="min-w-0"
                        key={`${suggestion.version}:${suggestion.image || ''}:${suggestion.source || ''}:${suggestion.label || ''}`}
                        value={versionChoiceLabel(suggestion)}
                        onSelect={() => onSelect(suggestion)}
                      >
                        <Check className={cn('size-4 shrink-0 opacity-0', selected && 'opacity-100')} />
                        <span className="flex min-w-0 flex-1 flex-col gap-0.5">
                          <span className="truncate text-sm font-medium leading-tight">{suggestion.version}</span>
                          <span className="truncate text-xs leading-tight text-muted-foreground">
                            {[versionChoiceImage(suggestion), versionChoiceKind(suggestion)].filter(Boolean).join(' | ')}
                          </span>
                        </span>
                      </CommandItem>
                    );
                  })}
                </CommandGroup>
              </CommandList>
            </Command>
          </PopoverContent>
        </Popover>
      </div>
      <p className="min-h-4 text-xs text-muted-foreground">{sourceText}</p>
    </div>
  );
}

function ReviewPanel({
  controller,
  state,
  reviewViewRef,
  reviewMainRef,
  diffListRef,
}: {
  controller: ERunUIController;
  state: AppState;
  reviewViewRef: React.RefObject<HTMLElement | null>;
  reviewMainRef: React.RefObject<HTMLDivElement | null>;
  diffListRef: React.RefObject<HTMLDivElement | null>;
}): React.ReactElement {
  return (
    <section
      ref={reviewViewRef}
      className={cn('review-view', !state.reviewOpen && 'is-hidden', !state.filesOpen && 'files-hidden')}
    >
      <div
        ref={reviewMainRef}
        className="review-main"
        onScroll={() => controller.queueVisibleDiffSelectionUpdate()}
      >
        <div ref={diffListRef} className="diff-list">
          <DiffList controller={controller} state={state} />
        </div>
      </div>
      <div
        className="files-splitter"
        role="separator"
        aria-orientation="vertical"
        aria-label="Resize changed files list"
        onMouseDown={(event) => controller.startFilesResize(event)}
      />
      <aside className="changed-files">
        <div className="changed-files-header">
          <button className="changed-files-title" type="button">
            <FileDiff aria-hidden="true" />
            Changed files <span className="changed-files-count">{state.diff?.summary?.fileCount || 0}</span>
            <ChevronDown aria-hidden="true" />
          </button>
          <div className="changed-files-actions">
            <IconTooltip label="Refresh diff">
              <Button
                className="changed-files-icon-button"
                type="button"
                variant="ghost"
                size="icon"
                aria-label="Refresh diff"
                disabled={state.diffLoading}
                onClick={() => {
                  void controller.loadReviewDiff();
                }}
              >
                <RefreshCw />
              </Button>
            </IconTooltip>
            <div className="changed-files-stats">
              <span>+{state.diff?.summary?.additions || 0}</span>
              <span>-{state.diff?.summary?.deletions || 0}</span>
            </div>
          </div>
        </div>
        <Label className="file-filter">
          <Search aria-hidden="true" />
          <Input
            className="file-filter-input"
            value={state.diffFilter}
            type="search"
            placeholder="Filter files..."
            autoComplete="off"
            onChange={(event) => controller.setDiffFilter(event.target.value)}
          />
        </Label>
        <div className="changed-file-tree">
          <ChangedFileTree controller={controller} state={state} />
        </div>
      </aside>
    </section>
  );
}

function ChangedFileTree({ controller, state }: { controller: ERunUIController; state: AppState }): React.ReactElement {
  if (state.diffLoading) {
    return <ReviewStatus>Loading...</ReviewStatus>;
  }
  if (state.diffError) {
    return <ReviewStatus>{compactDiffError(state.diffError)}</ReviewStatus>;
  }

  const tree = visibleDiffTreeNodes(filterDiffTree(state.diff?.tree || [], state.diffFilter), state.collapsedDiffDirs);
  if (tree.length === 0) {
    return <ReviewStatus>{state.diff ? 'No matching files' : 'No changes'}</ReviewStatus>;
  }

  return (
    <>
      {tree.map((node) => (
        <ChangedFileNode key={node.path} controller={controller} state={state} node={node} />
      ))}
    </>
  );
}

function ChangedFileNode({
  controller,
  state,
  node,
}: {
  controller: ERunUIController;
  state: AppState;
  node: DiffTreeNode;
}): React.ReactElement {
  const style = { '--depth': String(node.depth) } as React.CSSProperties;

  if (node.type === 'directory') {
    const collapsed = state.collapsedDiffDirs.has(node.path);
    return (
      <div className="changed-file-node">
        <button
          type="button"
          className="changed-file-row changed-file-row-directory"
          style={style}
          onClick={() => controller.toggleDiffDirectory(node.path)}
        >
          <ChevronRight className={cn('tree-chevron', !collapsed && 'is-open')} aria-hidden="true" />
          <span>{node.name}</span>
        </button>
      </div>
    );
  }

  return (
    <div className="changed-file-node">
      <button
        type="button"
        className={cn('changed-file-row changed-file-row-file', node.path === state.selectedDiffPath && 'is-selected')}
        style={style}
        data-path={node.path}
        onClick={() => controller.selectDiffPath(node.path)}
      >
        <FileIcon filePath={node.path} />
        <span>{node.name}</span>
      </button>
    </div>
  );
}

function DiffList({ controller, state }: { controller: ERunUIController; state: AppState }): React.ReactElement {
  if (state.diffLoading) {
    return <ReviewStatus>Loading diff...</ReviewStatus>;
  }
  if (state.diffError) {
    return <ReviewStatus>{compactDiffError(state.diffError)}</ReviewStatus>;
  }
  const files = state.diff?.files || [];
  if (files.length === 0) {
    return <ReviewStatus>No changes</ReviewStatus>;
  }
  return (
    <>
      {files.map((file) => (
        <DiffFileView key={file.path} file={file} selected={file.path === state.selectedDiffPath} />
      ))}
      <span className="sr-only">{controller.state.selectedDiffPath}</span>
    </>
  );
}

function DiffFileView({ file, selected }: { file: DiffFile; selected: boolean }): React.ReactElement {
  return (
    <section className={cn('diff-file', selected && 'is-selected')} data-path={file.path}>
      <header className="diff-file-header">
        <span className="diff-file-path">{file.path}</span>
        <span className="diff-file-counts">
          <span>+{file.additions}</span> <span>-{file.deletions}</span>
        </span>
      </header>
      {file.binary ? (
        <ReviewStatus>Binary file changed</ReviewStatus>
      ) : (
        (file.hunks || []).map((hunk) => <DiffHunkView key={hunk.header} hunk={hunk} />)
      )}
    </section>
  );
}

function DiffHunkView({ hunk }: { hunk: DiffHunk }): React.ReactElement {
  const contentWidth = Math.max(1, ...(hunk.lines || []).map((line) => line.content?.length || 0));
  const style = { '--diff-content-width': `${contentWidth + 2}ch` } as React.CSSProperties;

  return (
    <div className="diff-hunk">
      <div className="diff-hunk-header">{hunk.header}</div>
      <div className="diff-hunk-body" style={style}>
        {(hunk.lines || []).map((line, index) => (
          <div key={`${line.oldLine || ''}:${line.newLine || ''}:${index}`} className={`diff-line diff-line-${line.kind}`}>
            <span className="diff-line-old">{line.oldLine || ''}</span>
            <span className="diff-line-new">{line.newLine || ''}</span>
            <span className="diff-line-mark">{diffLineMark(line.kind)}</span>
            <span className="diff-line-content">{line.content || ' '}</span>
          </div>
        ))}
      </div>
    </div>
  );
}

function ReviewStatus({ children }: { children: React.ReactNode }): React.ReactElement {
  return <div className="review-status">{children}</div>;
}

function FileIcon({ filePath }: { filePath: string }): React.ReactElement {
  const Icon = fileIconForPath(filePath);
  return (
    <span className="file-icon" aria-hidden="true">
      <Icon className="file-icon-glyph" />
    </span>
  );
}

function fileIconForPath(filePath: string): typeof File {
  const name = filePath.split('/').pop()?.toLowerCase() || '';
  const extension = filePath.split('.').pop()?.toLowerCase() || '';
  if (['json', 'jsonc'].includes(extension)) {
    return FileJson;
  }
  if (extension === 'rb') {
    return Gem;
  }
  if (['yaml', 'yml', 'toml'].includes(extension) || name === 'dockerfile' || name === 'makefile') {
    return FileCog;
  }
  if (['md', 'mdx', 'txt'].includes(extension)) {
    return FileText;
  }
  if (['css', 'go', 'html', 'java', 'js', 'jsx', 'py', 'sh', 'ts', 'tsx'].includes(extension)) {
    return FileCode2;
  }
  return File;
}

function IconTooltip({ label, children }: { label: string; children: React.ReactElement }): React.ReactElement {
  return (
    <Tooltip>
      <TooltipTrigger asChild>{children}</TooltipTrigger>
      <TooltipContent>{label}</TooltipContent>
    </Tooltip>
  );
}

function findVersionSuggestion(suggestions: UIVersionSuggestion[], version: string, image: string): UIVersionSuggestion | undefined {
  if (!version) {
    return undefined;
  }
  if (image) {
    return suggestions.find((suggestion) => suggestion.version === version && suggestion.image === image);
  }
  return suggestions.find((suggestion) => suggestion.version === version);
}

function normalizeDialogValue(value: string): string {
  return value.trim();
}

function normalizeVersionSuggestions(values: UIVersionSuggestion[]): UIVersionSuggestion[] {
  const suggestions: UIVersionSuggestion[] = [];
  for (const value of values) {
    const version = normalizeDialogValue(value.version);
    const image = normalizeDialogValue(value.image || '');
    const source = normalizeDialogValue(value.source || '');
    const label = normalizeDialogValue(value.label);
    if (version && !suggestions.some((suggestion) => suggestion.version === version && suggestion.image === image && suggestion.source === source && suggestion.label === label)) {
      suggestions.push({
        label,
        version,
        source,
        image,
      });
    }
  }
  return suggestions;
}

function versionChoiceLabel(suggestion: UIVersionSuggestion): string {
  const source = versionChoiceSource(suggestion);
  if (!suggestion.label) {
    if (source) {
      return `${source}: ${suggestion.version}`;
    }
    return suggestion.version;
  }
  if (source && !suggestion.label.toLowerCase().startsWith(source.toLowerCase())) {
    return `${source} ${suggestion.label.toLowerCase()}: ${suggestion.version}`;
  }
  return `${suggestion.label}: ${suggestion.version}`;
}

function versionChoiceKind(suggestion: UIVersionSuggestion): string {
  const label = normalizeDialogValue(suggestion.label);
  if (!label) {
    return '';
  }
  const source = versionChoiceSource(suggestion);
  if (source && label.toLowerCase().startsWith(source.toLowerCase())) {
    return normalizeDialogValue(label.slice(source.length));
  }
  return label;
}

function versionChoiceSource(suggestion: UIVersionSuggestion): string {
  const source = normalizeDialogValue(suggestion.source || '');
  if (source) {
    return source;
  }
  const image = normalizeDialogValue(suggestion.image || '');
  if (image === 'erun-devops') {
    return 'ERun';
  }
  if (image.endsWith('-devops')) {
    return image.slice(0, -'-devops'.length);
  }
  return '';
}

function versionChoiceImage(suggestion: UIVersionSuggestion): string {
  const image = normalizeDialogValue(suggestion.image || '');
  if (image) {
    return image;
  }
  const source = versionChoiceSource(suggestion);
  if (!source) {
    return '';
  }
  if (source === 'ERun') {
    return 'erun-devops';
  }
  return `${source}-devops`;
}

function selectedVersionSourceText(suggestion: UIVersionSuggestion | undefined): string {
  if (!suggestion) {
    return '';
  }
  const image = versionChoiceImage(suggestion);
  if (!image) {
    return '';
  }
  return `Image: ${image}`;
}

function deleteConfirmationValue(selection: UISelection): string {
  return `${selection.tenant}-${selection.environment}`;
}

function filterDiffTree(nodes: DiffTreeNode[], filter: string): DiffTreeNode[] {
  if (!filter) {
    return nodes;
  }
  const matchingPaths = new Set<string>();
  const nodesByPath = new Map(nodes.map((node) => [node.path, node]));
  for (const node of nodes.filter((item) => item.type === 'file')) {
    if (!node.path.toLowerCase().includes(filter)) {
      continue;
    }
    matchingPaths.add(node.path);
    let parentPath = node.parentPath || '';
    while (parentPath) {
      matchingPaths.add(parentPath);
      parentPath = nodesByPath.get(parentPath)?.parentPath || '';
    }
  }
  return nodes.filter((node) => matchingPaths.has(node.path));
}

function visibleDiffTreeNodes(nodes: DiffTreeNode[], collapsedDiffDirs: Set<string>): DiffTreeNode[] {
  const nodesByPath = new Map(nodes.map((node) => [node.path, node]));
  return nodes.filter((node) => {
    let parentPath = node.parentPath || '';
    while (parentPath) {
      if (collapsedDiffDirs.has(parentPath)) {
        return false;
      }
      parentPath = nodesByPath.get(parentPath)?.parentPath || '';
    }
    return true;
  });
}

function chooseSelectedDiffPath(diff: DiffResult | null, currentPath: string): string {
  const files = diff?.files || [];
  if (files.some((file) => file.path === currentPath)) {
    return currentPath;
  }
  return files[0]?.path || '';
}

function compactDiffError(message: string): string {
  if (message.includes('unknown tool "diff"')) {
    return 'Runtime MCP does not expose diff yet. Refresh after deploy finishes.';
  }
  return message;
}

function diffLineMark(kind: DiffLine['kind']): string {
  if (kind === 'add') {
    return '+';
  }
  if (kind === 'delete') {
    return '-';
  }
  return '';
}

function cssEscape(value: string): string {
  if ('CSS' in window && typeof window.CSS.escape === 'function') {
    return window.CSS.escape(value);
  }
  return value.split('"').join('\\"');
}

function isTerminalPasteTarget(terminalRoot: HTMLDivElement, target: EventTarget | null): boolean {
  return target instanceof Node && terminalRoot.contains(target);
}

function pastedImageFiles(event: ClipboardEvent): File[] {
  const items = event.clipboardData?.items;
  if (!items) {
    return [];
  }

  const files: File[] = [];
  for (const item of Array.from(items)) {
    if (item.kind !== 'file' || !item.type.toLowerCase().startsWith('image/')) {
      continue;
    }
    const file = item.getAsFile();
    if (file) {
      files.push(file);
    }
  }
  return files;
}

async function fileToBase64(file: File): Promise<string> {
  const buffer = await file.arrayBuffer();
  return bytesToBase64(new Uint8Array(buffer));
}

function bytesToBase64(bytes: Uint8Array): string {
  const chunkSize = 0x8000;
  let binary = '';
  for (let index = 0; index < bytes.length; index += chunkSize) {
    binary += String.fromCharCode(...bytes.subarray(index, index + chunkSize));
  }
  return window.btoa(binary);
}

function readError(error: unknown): string {
  if (typeof error === 'string') {
    return error;
  }
  if (error instanceof Error && typeof error.message === 'string') {
    return error.message;
  }
  if (error && typeof error === 'object' && 'message' in error && typeof error.message === 'string') {
    return error.message;
  }
  return 'Unexpected error';
}

function decodeBase64Bytes(value: string): Uint8Array {
  const binary = window.atob(value);
  const bytes = new Uint8Array(binary.length);
  for (let index = 0; index < binary.length; index += 1) {
    bytes[index] = binary.charCodeAt(index);
  }
  return bytes;
}

function loadSavedSidebarWidth(): number {
  return loadSavedNumber(SIDEBAR_WIDTH_STORAGE_KEY, DEFAULT_SIDEBAR_WIDTH, MIN_SIDEBAR_WIDTH, MAX_SIDEBAR_WIDTH);
}

function loadSavedReviewWidth(): number {
  return loadSavedNumber(REVIEW_WIDTH_STORAGE_KEY, DEFAULT_REVIEW_WIDTH, MIN_REVIEW_WIDTH, MAX_REVIEW_WIDTH);
}

function loadSavedFilesWidth(): number {
  return loadSavedNumber(FILES_WIDTH_STORAGE_KEY, DEFAULT_FILES_WIDTH, MIN_FILES_WIDTH, MAX_FILES_WIDTH);
}

function loadSavedNumber(key: string, fallback: number, minimum: number, maximum: number): number {
  try {
    const raw = window.localStorage.getItem(key);
    if (!raw) {
      return fallback;
    }
    const parsed = Number.parseInt(raw, 10);
    if (!Number.isFinite(parsed)) {
      return fallback;
    }
    return clamp(parsed, minimum, maximum);
  } catch {
    return fallback;
  }
}

function loadSavedFilesOpen(): boolean {
  try {
    return window.localStorage.getItem(FILES_OPEN_STORAGE_KEY) !== 'false';
  } catch {
    return true;
  }
}

function saveNumber(key: string, value: number): void {
  try {
    window.localStorage.setItem(key, String(value));
  } catch {
  }
}

function saveBoolean(key: string, value: boolean): void {
  try {
    window.localStorage.setItem(key, String(value));
  } catch {
  }
}

function clamp(value: number, minimum: number, maximum: number): number {
  return Math.min(maximum, Math.max(minimum, value));
}

function selectionKey(selection: UISelection): string {
  return `${selection.tenant}\u0000${selection.environment}`;
}
