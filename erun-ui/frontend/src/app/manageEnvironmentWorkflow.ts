import { TerminalSessionRegistry } from './TerminalSessionRegistry';
import { StartDoctorSession, StartSSHDInitSession } from '../../wailsjs/go/main/App';
import { cloudApi } from './api/cloudApi';
import { environmentApi } from './api/environmentApi';
import { store } from './store';
import { readError } from './errors';
import { runtimePodConfigToDisplay, runtimePodConfigToKubernetes, runtimeResourceLimitMessage } from './runtimeResources';
import type { HiddenSessionMode } from './model';
import { defaultEnvironmentConfig, defaultManageDialog, type AppState, type ManageDialogState } from './state';
import { rememberPastContainerRegistry } from './storage';
import { formatDebugCommand, hiddenSessionBusyMessage } from './terminalStatus';
import {
  deleteConfirmationValue,
  normalizeDialogValue,
  normalizeVersionSuggestions,
  selectionKey,
} from './versionSuggestions';
import type {
  DeleteEnvironmentResult,
  ManageTab,
  StartSessionResult,
  UICloudContextStatus,
  UIEnvironmentConfig,
  UISelection,
  UIVersionSuggestion,
} from '@/types';

interface TerminalSize {
  cols: number;
  rows: number;
}

interface ManageEnvironmentWorkflowDeps {
  state: AppState;
  sessions: TerminalSessionRegistry;
  terminalSize: () => TerminalSize;
  fitTerminal: () => void;
  resetTerminal: () => void;
  emit: () => void;
  focusTerminalSoon: () => void;
  queueTerminalResize: () => void;
  refreshKubernetesContexts: () => void;
  reloadStateAfterEnvironmentChange: () => Promise<void>;
  resolveRuntimeImage: (version: string) => string;
  startDeploySelection: (selection: UISelection) => Promise<void>;
  activateLocalAfterCommand: (selection: UISelection, result: StartSessionResult) => Promise<void>;
  showNotification: (kind: NonNullable<AppState['notification']>['kind'], message: string) => void;
  showTerminalMessage: (message: string, busy?: boolean) => void;
  setPendingDebugHeader: (header: string) => void;
  applyPendingDebugHeader: (sessionId: number) => void;
  syncDebugDisplay: () => void;
}

export class ManageEnvironmentWorkflow {
  private versionSuggestionRequest = 0;

  constructor(private readonly deps: ManageEnvironmentWorkflowDeps) {}

  openDialog(selection: UISelection): void {
    this.state.manageDialog = {
      open: true,
      tab: 'general',
      selection,
      version: '',
      versionImage: '',
      config: {
        ...defaultEnvironmentConfig(),
        name: selection.environment,
      },
      initialConfig: null,
      configLoading: true,
      resourceStatus: null,
      resourceStatusLoading: false,
      confirmation: '',
      busy: false,
      busyAction: '',
      busyTarget: '',
      choicesOpen: false,
      error: '',
      pendingRedeploy: false,
    };
    this.deps.emit();
    void this.refreshVersionSuggestions(false);
    void this.loadConfig();
  }

  closeDialog(): void {
    if (this.state.manageDialog.busy) {
      return;
    }
    this.state.manageDialog = defaultManageDialog();
    this.deps.emit();
    this.deps.focusTerminalSoon();
  }

  setTab(tab: ManageTab): void {
    if (this.state.manageDialog.busy) {
      return;
    }
    this.state.manageDialog = {
      ...this.state.manageDialog,
      tab,
      choicesOpen: false,
      error: '',
    };
    this.deps.emit();
  }

  updateDialog(values: Partial<ManageDialogState>): void {
    if (this.state.manageDialog.busy) {
      return;
    }
    const versionReset = values.version !== undefined;
    this.state.manageDialog = {
      ...this.state.manageDialog,
      ...values,
      error: values.error ?? '',
      ...(versionReset ? { versionImage: '', choicesOpen: false } : {}),
    };
    this.deps.emit();
  }

  toggleVersionChoices(): void {
    this.setVersionChoicesOpen(!this.state.manageDialog.choicesOpen);
  }

  setVersionChoicesOpen(open: boolean): void {
    if (this.state.manageDialog.busy) {
      return;
    }
    this.state.manageDialog = {
      ...this.state.manageDialog,
      choicesOpen: open && this.state.versionSuggestions.length > 0,
    };
    this.deps.emit();
  }

  selectVersionSuggestion(suggestion: UIVersionSuggestion | undefined): void {
    if (this.state.manageDialog.busy) {
      return;
    }
    this.state.manageDialog = {
      ...this.state.manageDialog,
      version: suggestion?.version || '',
      versionImage: suggestion?.image || '',
      choicesOpen: false,
    };
    this.deps.emit();
  }

  updateConfig(values: Partial<UIEnvironmentConfig>): void {
    if (this.state.manageDialog.busy || this.state.manageDialog.configLoading) {
      return;
    }
    const config = {
      ...this.state.manageDialog.config,
      ...values,
    };
    if (values.cloudProviderAlias !== undefined) {
      config.cloudContext = undefined;
    }
    this.state.manageDialog = {
      ...this.state.manageDialog,
      config,
      error: '',
    };
    this.deps.emit();
  }

  updateClaudeConfig(values: Partial<UIEnvironmentConfig['claude']>): void {
    if (this.state.manageDialog.busy || this.state.manageDialog.configLoading) {
      return;
    }
    const merged = { ...this.state.manageDialog.config.claude, ...values };
    const next: UIEnvironmentConfig['claude'] = {};
    if (merged.useMantle !== undefined) next.useMantle = merged.useMantle;
    if (merged.useBedrock !== undefined) next.useBedrock = merged.useBedrock;
    if (merged.models !== undefined && merged.models.length > 0) next.models = merged.models;
    if (merged.maxOutputTokens !== undefined) next.maxOutputTokens = merged.maxOutputTokens;
    this.state.manageDialog = {
      ...this.state.manageDialog,
      config: {
        ...this.state.manageDialog.config,
        claude: next,
      },
      error: '',
    };
    this.deps.emit();
  }

  updateSSHDConfig(values: Partial<UIEnvironmentConfig['sshd']>): void {
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
    this.deps.emit();
  }

  async chooseWorkspaceSyncLocalFolder(): Promise<void> {
    const dialog = this.state.manageDialog;
    const selection = dialog.selection;
    if (dialog.busy || dialog.configLoading || !selection || !dialog.config.sshd.workspaceSyncEnabled) {
      return;
    }
    const folder = await store
      .dispatch(
        environmentApi.endpoints.chooseWorkspaceSyncLocalFolder.initiate({
          selection,
          current: dialog.config.sshd.workspaceSyncLocalPath || '',
        }),
      )
      .unwrap();
    const selected = String(folder || '').trim();
    if (!selected) {
      return;
    }
    this.updateSSHDConfig({ workspaceSyncLocalPath: selected });
  }

  async loadConfig(): Promise<void> {
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
    this.deps.emit();
    try {
      const result = await store
        .dispatch(
          environmentApi.endpoints.getEnvironmentConfig.initiate(selection, { forceRefetch: true }),
        )
        .unwrap();
      const displayConfig = {
        ...result,
        runtimePod: runtimePodConfigToDisplay(result.runtimePod),
      };
      this.state.manageDialog = {
        ...this.state.manageDialog,
        config: displayConfig,
        initialConfig: cloneEnvironmentConfig(displayConfig),
        configLoading: false,
        resourceStatusLoading: true,
        error: '',
      };
      this.deps.emit();
      void this.loadResourceStatus(result.kubernetesContext, selection);
    } catch (error) {
      this.state.manageDialog = {
        ...this.state.manageDialog,
        configLoading: false,
        error: readError(error),
      };
      this.deps.emit();
    }
  }

  private async loadResourceStatus(kubernetesContext: string, selection: UISelection): Promise<void> {
    if (!this.state.manageDialog.open) {
      return;
    }
    try {
      const status = await store
        .dispatch(
          environmentApi.endpoints.getRuntimeResourceStatus.initiate(
            {
              kubernetesContext,
              tenant: selection.tenant,
              environment: selection.environment,
            },
            { forceRefetch: true },
          ),
        )
        .unwrap();
      if (!this.state.manageDialog.open) {
        return;
      }
      this.state.manageDialog = {
        ...this.state.manageDialog,
        resourceStatus: status,
        resourceStatusLoading: false,
      };
      this.deps.emit();
    } catch (error) {
      if (!this.state.manageDialog.open) {
        return;
      }
      this.state.manageDialog = {
        ...this.state.manageDialog,
        resourceStatus: {
          kubernetesContext,
          available: false,
          message: readError(error),
          cpu: { total: 0, used: 0, free: 0, unit: 'cores', formatted: '' },
          memory: { total: 0, used: 0, free: 0, unit: 'GiB', formatted: '' },
        },
        resourceStatusLoading: false,
      };
      this.deps.emit();
    }
  }

  async submitConfig(): Promise<void> {
    const dialog = this.state.manageDialog;
    if (dialog.busy || dialog.configLoading) {
      return;
    }
    const selection = dialog.selection;
    if (!selection) {
      this.closeDialog();
      return;
    }
    const resourceError = runtimeResourceLimitMessage(dialog.config.runtimePod, dialog.resourceStatus);
    if (resourceError) {
      this.state.manageDialog = { ...dialog, error: resourceError };
      this.deps.emit();
      return;
    }

    this.state.manageDialog = { ...dialog, busy: true, busyAction: 'save', busyTarget: '', error: '' };
    this.deps.emit();
    try {
      const saveConfig = {
        ...dialog.config,
        runtimePod: runtimePodConfigToKubernetes(dialog.config.runtimePod),
      };
      const result = await store
        .dispatch(
          environmentApi.endpoints.saveEnvironmentConfig.initiate({ selection, config: saveConfig }),
        )
        .unwrap();
      rememberPastContainerRegistry(result.containerRegistry || saveConfig.containerRegistry);
      const displayConfig = {
        ...result,
        runtimePod: runtimePodConfigToDisplay(result.runtimePod),
      };
      this.state.manageDialog = {
        ...this.state.manageDialog,
        config: displayConfig,
        initialConfig: cloneEnvironmentConfig(displayConfig),
        busy: false,
        busyAction: '',
        busyTarget: '',
        error: '',
        pendingRedeploy: true,
      };
      this.deps.emit();
    } catch (error) {
      const message = readError(error);
      this.state.manageDialog = {
        ...this.state.manageDialog,
        busy: false,
        busyAction: '',
        busyTarget: '',
        error: message,
      };
      this.deps.showTerminalMessage(message);
      this.deps.emit();
    }
  }

  async startCloudContext(name: string): Promise<void> {
    await this.updateCloudContextPower(
      name,
      (target) => store.dispatch(cloudApi.endpoints.startCloudContext.initiate(target)).unwrap(),
      'Started',
    );
    this.deps.refreshKubernetesContexts();
  }

  async enableSSHD(): Promise<void> {
    await this.startHiddenSession('sshd-init', StartSSHDInitSession);
  }

  async startDoctor(): Promise<void> {
    await this.startHiddenSession('doctor', StartDoctorSession);
  }

  async stopCloudContext(name: string): Promise<void> {
    await this.updateCloudContextPower(
      name,
      (target) => store.dispatch(cloudApi.endpoints.stopCloudContext.initiate(target)).unwrap(),
      'Stopped',
    );
  }

  async submitDeploy(): Promise<void> {
    const dialog = this.state.manageDialog;
    if (dialog.busy) {
      return;
    }
    const selection = dialog.selection;
    if (!selection) {
      this.closeDialog();
      return;
    }
    const version = normalizeDialogValue(dialog.version);
    this.closeDialog();
    await this.deps.startDeploySelection({ ...selection, version, runtimeImage: version ? this.deps.resolveRuntimeImage(version) : '' });
  }

  async submitDelete(): Promise<void> {
    const dialog = this.state.manageDialog;
    if (dialog.busy) {
      return;
    }
    const selection = dialog.selection;
    if (!selection) {
      this.closeDialog();
      return;
    }
    const confirmation = normalizeDialogValue(dialog.confirmation);
    const expected = deleteConfirmationValue(selection);
    if (confirmation !== expected) {
      return;
    }

    this.state.manageDialog = { ...dialog, busy: true, busyAction: 'delete', busyTarget: '', error: '' };
    this.state.terminalCopyOutput = '';
    this.state.terminalCopyStatus = '';
    this.deps.showTerminalMessage(`Deleting ${selection.tenant} / ${selection.environment}...`);

    try {
      const result = (await store
        .dispatch(
          environmentApi.endpoints.deleteEnvironment.initiate({ selection, confirmation }),
        )
        .unwrap()) as DeleteEnvironmentResult;
      const deletedSelected = this.state.selected ? selectionKey(this.state.selected) === selectionKey(selection) : false;
      if (deletedSelected) {
        this.state.selected = null;
        this.state.sessionId = 0;
        this.state.debugOutput = '';
        this.deps.resetTerminal();
      }
      await this.deps.reloadStateAfterEnvironmentChange();
      this.state.manageDialog = defaultManageDialog();
      this.state.terminalCopyOutput = '';
      this.state.terminalCopyStatus = '';
      const warnings = [
        result.namespaceDeleteError ? `Namespace deletion failed: ${result.namespaceDeleteError}` : '',
        result.cloudContextStopError ? `Cloud context stop failed: ${result.cloudContextStopError}` : '',
      ].filter(Boolean).join(' ');
      const warning = warnings ? ` ${warnings}` : '';
      this.deps.showTerminalMessage(`Deleted ${result.tenant} / ${result.environment}.${warning}`);
    } catch (error) {
      const message = readError(error);
      this.state.manageDialog = { ...this.state.manageDialog, busy: false, busyAction: '', busyTarget: '', error: message };
      this.state.terminalCopyOutput = `Failed to delete ${selection.tenant} / ${selection.environment}: ${message}`;
      this.state.terminalCopyStatus = '';
      this.deps.showTerminalMessage(message);
      this.deps.emit();
    }
  }

  private async refreshVersionSuggestions(selectDefault: boolean): Promise<void> {
    const selection = this.state.manageDialog.selection;
    if (!selection) {
      return;
    }
    const request = ++this.versionSuggestionRequest;
    const raw = await store
      .dispatch(
        environmentApi.endpoints.getVersionSuggestions.initiate(selection, { forceRefetch: true }),
      )
      .unwrap();
    const suggestions = normalizeVersionSuggestions(raw);
    if (request !== this.versionSuggestionRequest || !this.state.manageDialog.open) {
      return;
    }

    this.state.versionSuggestions = suggestions;
    const currentVersion = normalizeDialogValue(this.state.manageDialog.version);
    if (!currentVersion && !selectDefault) {
      this.deps.emit();
    } else if (selectDefault || !suggestions.some((suggestion) => suggestion.version === currentVersion)) {
      this.selectVersionSuggestion(suggestions[0]);
    } else {
      this.deps.emit();
    }
  }

  private async startHiddenSession(mode: HiddenSessionMode, starter: (selection: UISelection, cols: number, rows: number) => Promise<unknown>): Promise<void> {
    const dialog = this.state.manageDialog;
    const selection = dialog.selection;
    if (dialog.busy || dialog.configLoading || !selection) {
      return;
    }
    const runSelection = { ...selection, debug: this.state.debugOpen || undefined };
    this.prepareHiddenSession(selection, runSelection, mode);
    this.deps.fitTerminal();
    const terminalSize = this.deps.terminalSize();
    const result = (await starter(runSelection, terminalSize.cols, terminalSize.rows)) as StartSessionResult;
    if (result.kind === 'local') {
      await this.deps.activateLocalAfterCommand(selection, result);
      return;
    }
    this.trackHiddenSession(mode, result.sessionId, runSelection);
    this.sessions.registerDebugSession(result.sessionId, runSelection, 'hidden');
    this.deps.applyPendingDebugHeader(result.sessionId);
    this.state.sessionId = result.sessionId;
    this.deps.syncDebugDisplay();

    this.deps.resetTerminal();
    this.deps.focusTerminalSoon();
    this.deps.queueTerminalResize();
    this.deps.emit();
  }

  private prepareHiddenSession(selection: UISelection, runSelection: UISelection, mode: HiddenSessionMode): void {
    this.state.selected = selection;
    this.state.manageDialog = defaultManageDialog();
    if (this.state.debugOpen) {
      this.deps.setPendingDebugHeader(`$ ${formatDebugCommand(runSelection, mode)}\n`);
    }
    this.deps.emit();
    this.state.terminalCopyOutput = '';
    this.state.terminalCopyStatus = '';
    this.deps.showTerminalMessage(hiddenSessionBusyMessage(selection, mode), true);
  }

  private trackHiddenSession(mode: HiddenSessionMode, sessionId: number, selection: UISelection): void {
    if (mode === 'sshd-init') {
      this.sessions.trackSSHDInitSession(sessionId, selection);
      return;
    }
    this.sessions.trackDoctorSession(sessionId, selection);
  }

  private async updateCloudContextPower(name: string, action: (name: string) => Promise<unknown>, label: string): Promise<void> {
    const contextName = normalizeDialogValue(name);
    const dialog = this.state.manageDialog;
    if (dialog.busy || dialog.configLoading || !dialog.selection || !contextName) {
      return;
    }
    this.state.manageDialog = { ...dialog, busy: true, busyAction: 'cloud-context-power', busyTarget: contextName, error: '' };
    this.deps.emit();
    try {
      const context = (await action(contextName)) as UICloudContextStatus;
      this.state.manageDialog = {
        ...this.state.manageDialog,
        config: {
          ...this.state.manageDialog.config,
          cloudContext: context,
        },
        busy: false,
        busyAction: '',
        busyTarget: '',
        error: '',
      };
      this.deps.showTerminalMessage(`${label} cloud context ${context.kubernetesContext || context.name}.`);
      this.deps.emit();
    } catch (error) {
      const message = readError(error);
      this.state.manageDialog = {
        ...this.state.manageDialog,
        busy: false,
        busyAction: '',
        busyTarget: '',
        error: message,
      };
      this.deps.showTerminalMessage(message);
      this.deps.emit();
    }
  }

  private get state(): AppState {
    return this.deps.state;
  }

  private get sessions(): TerminalSessionRegistry {
    return this.deps.sessions;
  }
}

function cloneEnvironmentConfig(config: UIEnvironmentConfig): UIEnvironmentConfig {
  return JSON.parse(JSON.stringify(config));
}

export function manageDialogTabHasUnsavedChanges(tab: ManageTab, config: UIEnvironmentConfig, initial: UIEnvironmentConfig | null): boolean {
  if (!initial) {
    return false;
  }
  const compare = (...keys: Array<keyof UIEnvironmentConfig>): boolean =>
    keys.some((key) => JSON.stringify(config[key]) !== JSON.stringify(initial[key]));
  switch (tab) {
    case 'general':
      return compare('containerRegistry', 'cloudProviderAlias', 'snapshot');
    case 'runtime':
      return compare('runtimePod', 'idle');
    case 'ai':
      return compare('claude');
    case 'ports':
      return false;
    case 'ssh':
      return JSON.stringify(config.sshd?.workspaceSyncEnabled) !== JSON.stringify(initial.sshd?.workspaceSyncEnabled)
        || JSON.stringify(config.sshd?.workspaceSyncLocalPath) !== JSON.stringify(initial.sshd?.workspaceSyncLocalPath);
    case 'delete':
      return false;
  }
}

