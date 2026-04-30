import { TerminalSessionRegistry } from './TerminalSessionRegistry';
import {
  InitCloudContext,
  LoadCloudContextStatuses,
  LoadCloudProviderStatuses,
  LoadERunConfig,
  LoginCloudProvider,
  SaveERunConfig,
  StartCloudContext,
  StartCloudInitAWSSession,
  StopCloudContext,
} from '../../wailsjs/go/main/App';
import {
  cloudContextDraftForConfig,
  idleCloudContextAction,
  replaceCloudContext,
  replaceCloudProvider,
} from './cloudContextState';
import { readError } from './errors';
import type { AppState, GlobalConfigDialogState } from './state';
import { defaultCloudContextInitInput, defaultGlobalConfigDialog } from './state';
import type {
  StartSessionResult,
  UICloudContextInitInput,
  UICloudContextStatus,
  UIERunConfig,
} from '@/types';

interface TerminalSize {
  cols: number;
  rows: number;
}

interface GlobalConfigWorkflowDeps {
  state: AppState;
  sessions: TerminalSessionRegistry;
  terminalSize: () => TerminalSize;
  fitTerminal: () => void;
  resetTerminal: () => void;
  emit: () => void;
  focusTerminalSoon: () => void;
  queueTerminalResize: () => void;
  refreshIdleStatus: () => void;
  refreshKubernetesContexts: () => void;
  hideTerminalMessage: () => void;
  showNotification: (kind: NonNullable<AppState['notification']>['kind'], message: string) => void;
  showTerminalMessage: (message: string, busy?: boolean) => void;
}

export class GlobalConfigWorkflow {
  constructor(private readonly deps: GlobalConfigWorkflowDeps) {}

  openDialog(): void {
    this.state.globalConfigDialog = {
      open: true,
      config: {
        defaultTenant: '',
        cloudProviders: [],
        cloudContexts: [],
      },
      cloudContextDraft: defaultCloudContextInitInput(),
      configLoading: true,
      busy: false,
      busyAction: '',
      busyTarget: '',
      error: '',
    };
    this.deps.emit();
    void this.loadConfig();
  }

  closeDialog(): void {
    if (this.state.globalConfigDialog.busy) {
      return;
    }
    this.state.globalConfigDialog = defaultGlobalConfigDialog();
    this.deps.emit();
    this.deps.focusTerminalSoon();
  }

  updateDialog(values: Partial<GlobalConfigDialogState>): void {
    if (this.state.globalConfigDialog.busy) {
      return;
    }
    this.state.globalConfigDialog = {
      ...this.state.globalConfigDialog,
      ...values,
      error: values.error ?? '',
    };
    this.deps.emit();
  }

  updateConfig(values: Partial<UIERunConfig>): void {
    if (this.state.globalConfigDialog.busy || this.state.globalConfigDialog.configLoading) {
      return;
    }
    this.updateDialog({
      config: {
        ...this.state.globalConfigDialog.config,
        ...values,
      },
    });
  }

  updateCloudContextDraft(values: Partial<UICloudContextInitInput>): void {
    if (this.state.globalConfigDialog.busy || this.state.globalConfigDialog.configLoading) {
      return;
    }
    this.updateDialog({
      cloudContextDraft: {
        ...this.state.globalConfigDialog.cloudContextDraft,
        ...values,
      },
    });
  }

  async loadConfig(): Promise<void> {
    const dialog = this.state.globalConfigDialog;
    if (!dialog.open) {
      return;
    }
    this.state.globalConfigDialog = {
      ...dialog,
      configLoading: true,
      error: '',
    };
    this.deps.emit();
    try {
      const result = (await LoadERunConfig()) as UIERunConfig;
      this.state.globalConfigDialog = {
        ...this.state.globalConfigDialog,
        config: result,
        cloudContextDraft: cloudContextDraftForConfig(result, this.state.globalConfigDialog.cloudContextDraft),
        configLoading: false,
        error: '',
      };
      this.deps.emit();
    } catch (error) {
      this.state.globalConfigDialog = {
        ...this.state.globalConfigDialog,
        configLoading: false,
        error: readError(error),
      };
      this.deps.emit();
    }
  }

  async refreshCloudProviders(): Promise<void> {
    const dialog = this.state.globalConfigDialog;
    if (!dialog.open || dialog.busy) {
      return;
    }
    try {
      const cloudProviders = await LoadCloudProviderStatuses();
      this.state.globalConfigDialog = {
        ...this.state.globalConfigDialog,
        config: {
          ...this.state.globalConfigDialog.config,
          cloudProviders,
        },
        error: '',
      };
      this.deps.emit();
    } catch (error) {
      const message = readError(error);
      this.state.globalConfigDialog = {
        ...this.state.globalConfigDialog,
        error: message,
      };
      this.deps.showTerminalMessage(message);
      this.deps.emit();
    }
  }

  async refreshCloudContexts(): Promise<void> {
    const dialog = this.state.globalConfigDialog;
    if (!dialog.open || dialog.busy) {
      return;
    }
    try {
      const cloudContexts = await LoadCloudContextStatuses();
      this.state.globalConfigDialog = {
        ...this.state.globalConfigDialog,
        config: {
          ...this.state.globalConfigDialog.config,
          cloudContexts,
        },
        error: '',
      };
      this.deps.emit();
    } catch (error) {
      const message = readError(error);
      this.state.globalConfigDialog = {
        ...this.state.globalConfigDialog,
        error: message,
      };
      this.deps.showTerminalMessage(message);
      this.deps.emit();
    }
  }

  async initCloudContext(): Promise<void> {
    const dialog = this.state.globalConfigDialog;
    if (dialog.busy || dialog.configLoading) {
      return;
    }
    this.state.globalConfigDialog = { ...dialog, busy: true, busyAction: 'cloud-context-init', busyTarget: '', error: '' };
    this.deps.emit();
    try {
      const context = (await InitCloudContext(dialog.cloudContextDraft)) as UICloudContextStatus;
      this.state.globalConfigDialog = {
        ...this.state.globalConfigDialog,
        config: {
          ...this.state.globalConfigDialog.config,
          cloudContexts: replaceCloudContext(this.state.globalConfigDialog.config.cloudContexts || [], context),
        },
        cloudContextDraft: cloudContextDraftForConfig(this.state.globalConfigDialog.config, {
          ...defaultCloudContextInitInput(),
          cloudProviderAlias: dialog.cloudContextDraft.cloudProviderAlias,
          region: dialog.cloudContextDraft.region,
        }),
        busy: false,
        busyAction: '',
        busyTarget: '',
        error: '',
      };
      this.deps.showTerminalMessage(`Initialized cloud context ${context.kubernetesContext}.`);
      this.deps.refreshKubernetesContexts();
      this.deps.emit();
    } catch (error) {
      const message = readError(error);
      this.state.globalConfigDialog = {
        ...this.state.globalConfigDialog,
        busy: false,
        busyAction: '',
        busyTarget: '',
        error: message,
      };
      this.deps.showTerminalMessage(message);
      this.deps.emit();
    }
  }

  async stopCloudContext(name: string): Promise<void> {
    await this.updateCloudContextPower(name, StopCloudContext, 'Stopped');
  }

  async startCloudContext(name: string): Promise<void> {
    await this.updateCloudContextPower(name, StartCloudContext, 'Started');
    this.deps.refreshKubernetesContexts();
  }

  async toggleIdleCloudContext(): Promise<void> {
    const action = idleCloudContextAction(this.state.idleStatus, this.state.idleCloudContextBusy);
    if (!action) {
      return;
    }
    this.state.idleCloudContextBusy = true;
    this.deps.emit();
    try {
      const context = (await action.run(action.name)) as UICloudContextStatus;
      this.applyIdleCloudContextResult(action.idleStatus, context);
      this.state.idleCloudContextBusy = false;
      this.deps.showNotification('success', `${action.label} cloud environment ${context.kubernetesContext || context.name}.`);
      this.deps.emit();
      if (action.refreshKubernetesContexts) {
        this.deps.refreshKubernetesContexts();
      }
      this.deps.refreshIdleStatus();
    } catch (error) {
      const message = readError(error);
      this.state.idleCloudContextBusy = false;
      this.deps.showNotification('error', message);
      this.deps.showTerminalMessage(message);
      this.deps.emit();
    }
  }

  async startAWSCloudInit(): Promise<void> {
    const dialog = this.state.globalConfigDialog;
    if (dialog.busy || dialog.configLoading) {
      return;
    }
    this.state.globalConfigDialog = { ...dialog, busy: true, busyAction: 'cloud-provider-init', busyTarget: '', error: '' };
    this.deps.emit();
    try {
      this.deps.fitTerminal();
      const terminalSize = this.deps.terminalSize();
      const result = (await StartCloudInitAWSSession(terminalSize.cols, terminalSize.rows)) as StartSessionResult;
      this.deps.sessions.trackCloudInitSession(result.sessionId);
      this.state.globalConfigDialog = defaultGlobalConfigDialog();
      this.state.sessionId = result.sessionId;
      this.state.terminalCopyOutput = '';
      this.state.terminalCopyStatus = '';
      this.deps.resetTerminal();
      this.deps.hideTerminalMessage();
      this.deps.focusTerminalSoon();
      this.deps.queueTerminalResize();
      this.deps.emit();
    } catch (error) {
      const message = readError(error);
      this.state.globalConfigDialog = {
        ...this.state.globalConfigDialog,
        busy: false,
        busyAction: '',
        busyTarget: '',
        error: message,
      };
      this.deps.showTerminalMessage(message);
      this.deps.emit();
    }
  }

  async loginCloudProvider(alias: string): Promise<void> {
    const dialog = this.state.globalConfigDialog;
    if (dialog.busy || dialog.configLoading) {
      return;
    }
    this.state.globalConfigDialog = { ...dialog, busy: true, busyAction: 'cloud-provider-login', busyTarget: alias, error: '' };
    this.deps.emit();
    try {
      const provider = await LoginCloudProvider(alias);
      this.state.globalConfigDialog = {
        ...this.state.globalConfigDialog,
        config: {
          ...this.state.globalConfigDialog.config,
          cloudProviders: replaceCloudProvider(this.state.globalConfigDialog.config.cloudProviders || [], provider),
        },
        busy: false,
        busyAction: '',
        busyTarget: '',
        error: '',
      };
      this.deps.showTerminalMessage(`${provider.alias}: ${provider.status}`);
      this.deps.emit();
    } catch (error) {
      const message = readError(error);
      this.state.globalConfigDialog = {
        ...this.state.globalConfigDialog,
        busy: false,
        busyAction: '',
        busyTarget: '',
        error: message,
      };
      this.deps.showTerminalMessage(message);
      this.deps.emit();
    }
  }

  async submitConfig(): Promise<void> {
    const dialog = this.state.globalConfigDialog;
    if (dialog.busy || dialog.configLoading) {
      return;
    }
    this.state.globalConfigDialog = { ...dialog, busy: true, busyAction: 'save', busyTarget: '', error: '' };
    this.deps.emit();
    try {
      const result = (await SaveERunConfig(dialog.config as Parameters<typeof SaveERunConfig>[0])) as UIERunConfig;
      this.state.globalConfigDialog = {
        ...this.state.globalConfigDialog,
        config: result,
        busy: false,
        busyAction: '',
        busyTarget: '',
        error: '',
      };
      this.deps.showNotification('success', 'Saved ERun config.');
      this.closeDialog();
    } catch (error) {
      const message = readError(error);
      this.state.globalConfigDialog = {
        ...this.state.globalConfigDialog,
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

  private applyIdleCloudContextResult(idleStatus: NonNullable<AppState['idleStatus']>, context: UICloudContextStatus): void {
    this.state.idleStatus = {
      ...(this.state.idleStatus ?? idleStatus),
      cloudContextName: context.name,
      cloudContextStatus: context.status,
      cloudContextLabel: context.kubernetesContext || context.name,
    };
    if (!this.state.globalConfigDialog.open) {
      return;
    }
    this.state.globalConfigDialog = {
      ...this.state.globalConfigDialog,
      config: {
        ...this.state.globalConfigDialog.config,
        cloudContexts: replaceCloudContext(this.state.globalConfigDialog.config.cloudContexts || [], context),
      },
    };
  }

  private async updateCloudContextPower(name: string, action: (name: string) => Promise<unknown>, label: string): Promise<void> {
    const dialog = this.state.globalConfigDialog;
    if (dialog.busy || dialog.configLoading) {
      return;
    }
    this.state.globalConfigDialog = { ...dialog, busy: true, busyAction: 'cloud-context-power', busyTarget: name, error: '' };
    this.deps.emit();
    try {
      const context = (await action(name)) as UICloudContextStatus;
      this.state.globalConfigDialog = {
        ...this.state.globalConfigDialog,
        config: {
          ...this.state.globalConfigDialog.config,
          cloudContexts: replaceCloudContext(this.state.globalConfigDialog.config.cloudContexts || [], context),
        },
        busy: false,
        busyAction: '',
        busyTarget: '',
        error: '',
      };
      this.deps.showTerminalMessage(`${label} cloud context ${context.kubernetesContext}.`);
      this.deps.emit();
    } catch (error) {
      const message = readError(error);
      this.state.globalConfigDialog = {
        ...this.state.globalConfigDialog,
        busy: false,
        busyAction: '',
        busyTarget: '',
        error: message,
      };
      this.deps.showTerminalMessage(message);
      this.deps.emit();
    }
  }
}
