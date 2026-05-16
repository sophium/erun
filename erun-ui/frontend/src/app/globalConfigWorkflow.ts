import { TerminalSessionRegistry } from './TerminalSessionRegistry';
import { StartCloudInitAWSSession } from '../../wailsjs/go/main/App';
import { cloudApi } from './api/cloudApi';
import { globalConfigApi } from './api/globalConfigApi';
import { store } from './store';
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
  UISelection,
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
  focusTerminalSoon: () => void;
  queueTerminalResize: () => void;
  openSelection: (selection: UISelection) => Promise<void>;
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
    void this.loadConfig();
  }

  closeDialog(): void {
    if (this.state.globalConfigDialog.busy) {
      return;
    }
    this.state.globalConfigDialog = defaultGlobalConfigDialog();
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
    try {
      const result = await store
        .dispatch(globalConfigApi.endpoints.getERunConfig.initiate(undefined, { forceRefetch: true }))
        .unwrap();
      this.state.globalConfigDialog = {
        ...this.state.globalConfigDialog,
        config: result,
        cloudContextDraft: cloudContextDraftForConfig(result, this.state.globalConfigDialog.cloudContextDraft),
        configLoading: false,
        error: '',
      };
    } catch (error) {
      this.state.globalConfigDialog = {
        ...this.state.globalConfigDialog,
        configLoading: false,
        error: readError(error),
      };
    }
  }

  async refreshCloudProviders(): Promise<void> {
    const dialog = this.state.globalConfigDialog;
    if (!dialog.open || dialog.busy) {
      return;
    }
    try {
      const cloudProviders = await store
        .dispatch(cloudApi.endpoints.getCloudProviderStatuses.initiate(undefined, { forceRefetch: true }))
        .unwrap();
      this.state.globalConfigDialog = {
        ...this.state.globalConfigDialog,
        config: {
          ...this.state.globalConfigDialog.config,
          cloudProviders,
        },
        error: '',
      };
      this.deps.showNotification('success', 'Cloud aliases refreshed.');
    } catch (error) {
      const message = readError(error);
      this.state.globalConfigDialog = {
        ...this.state.globalConfigDialog,
        error: message,
      };
      this.deps.showTerminalMessage(message);
    }
  }

  async refreshCloudContexts(): Promise<void> {
    const dialog = this.state.globalConfigDialog;
    if (!dialog.open || dialog.busy) {
      return;
    }
    try {
      const cloudContexts = await store
        .dispatch(cloudApi.endpoints.getCloudContextStatuses.initiate(undefined, { forceRefetch: true }))
        .unwrap();
      this.state.globalConfigDialog = {
        ...this.state.globalConfigDialog,
        config: {
          ...this.state.globalConfigDialog.config,
          cloudContexts,
        },
        error: '',
      };
      this.deps.showNotification('success', 'Cloud contexts refreshed.');
    } catch (error) {
      const message = readError(error);
      this.state.globalConfigDialog = {
        ...this.state.globalConfigDialog,
        error: message,
      };
      this.deps.showTerminalMessage(message);
    }
  }

  async initCloudContext(): Promise<void> {
    const dialog = this.state.globalConfigDialog;
    if (dialog.busy || dialog.configLoading) {
      return;
    }
    this.state.globalConfigDialog = { ...dialog, busy: true, busyAction: 'cloud-context-init', busyTarget: '', error: '' };
    try {
      const context = await store
        .dispatch(cloudApi.endpoints.initCloudContext.initiate(dialog.cloudContextDraft))
        .unwrap();
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
    }
  }

  async stopCloudContext(name: string): Promise<void> {
    await this.updateCloudContextPower(
      name,
      (target) => store.dispatch(cloudApi.endpoints.stopCloudContext.initiate(target)).unwrap(),
      'Stopped',
    );
  }

  async startCloudContext(name: string): Promise<void> {
    await this.updateCloudContextPower(
      name,
      (target) => store.dispatch(cloudApi.endpoints.startCloudContext.initiate(target)).unwrap(),
      'Started',
    );
    this.deps.refreshKubernetesContexts();
  }

  async toggleIdleCloudContext(): Promise<void> {
    const action = idleCloudContextAction(this.state.idleStatus, this.state.idleCloudContextBusy);
    if (!action) {
      return;
    }
    const selection = this.state.selected ? { ...this.state.selected } : null;
    this.state.idleCloudContextBusy = true;
    try {
      const context = (await action.run(action.name)) as UICloudContextStatus;
      this.applyIdleCloudContextResult(action.idleStatus, context);
      this.state.idleCloudContextBusy = false;
      this.deps.showNotification('success', `${action.label} cloud environment ${context.kubernetesContext || context.name}.`);
      if (action.refreshKubernetesContexts) {
        this.deps.refreshKubernetesContexts();
      }
      if (action.operation === 'start' && selection) {
        await this.deps.openSelection(selection);
      }
      this.deps.refreshIdleStatus();
    } catch (error) {
      const message = readError(error);
      this.state.idleCloudContextBusy = false;
      this.deps.showNotification('error', message);
      this.deps.showTerminalMessage(message);
    }
  }

  async startAWSCloudInit(): Promise<void> {
    const dialog = this.state.globalConfigDialog;
    if (dialog.busy || dialog.configLoading) {
      return;
    }
    this.state.globalConfigDialog = { ...dialog, busy: true, busyAction: 'cloud-provider-init', busyTarget: '', error: '' };
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
    }
  }

  async loginCloudProvider(alias: string): Promise<void> {
    const dialog = this.state.globalConfigDialog;
    if (dialog.busy || dialog.configLoading) {
      return;
    }
    this.state.globalConfigDialog = { ...dialog, busy: true, busyAction: 'cloud-provider-login', busyTarget: alias, error: '' };
    try {
      const provider = await store
        .dispatch(cloudApi.endpoints.loginCloudProvider.initiate(alias))
        .unwrap();
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
    }
  }

  async submitConfig(): Promise<void> {
    const dialog = this.state.globalConfigDialog;
    if (dialog.busy || dialog.configLoading) {
      return;
    }
    this.state.globalConfigDialog = { ...dialog, busy: true, busyAction: 'save', busyTarget: '', error: '' };
    try {
      const result = await store
        .dispatch(globalConfigApi.endpoints.saveERunConfig.initiate(dialog.config))
        .unwrap();
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
    }
  }
}
