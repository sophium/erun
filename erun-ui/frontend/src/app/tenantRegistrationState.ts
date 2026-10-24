// EnvironmentActionState is one row's deploy/stop/delete busy state; action is
// '' when idle, and conflict/unavailable carry the platform's wording.
export interface EnvironmentActionState {
  action: 'deploy' | 'stop' | 'delete' | '';
  error: string;
  conflictMessage: string;
  unavailableMessage: string;
}

export interface RegistrationState {
  contextName: string;
  contextCloudProviderAlias: string;
  contextRegion: string;
  contextInstanceType: string;
  creatingContext: boolean;
  createContextError: string;
  createContextConflict: string;
  contextPreviewPlan: string[] | null;

  // Preview and Register submit the same fields, so the previewed plan matches
  // the registration. envAdopt requires envKubernetesContext and forbids
  // envContextId/envRuntimeVersion, matching the platform's adopt validation.
  envName: string;
  envType: string;
  envContextId: string;
  envKubernetesContext: string;
  envRuntimeVersion: string;
  envAdopt: boolean;
  envPreviewing: boolean;
  envPreviewError: string;
  envPreviewPlan: string[] | null;
  envPreviewQuotaOk: boolean | null;
  envRegistering: boolean;
  envRegisterError: string;
  envRegisterConflict: string;

  envActions: Record<string, EnvironmentActionState>;
  deployVersionDrafts: Record<string, string>;
  deleteConfirmingEnvironmentId: string;
  deleteConfirmationDraft: string;
}

export function defaultEnvironmentActionState(): EnvironmentActionState {
  return { action: '', error: '', conflictMessage: '', unavailableMessage: '' };
}

export function defaultRegistrationState(): RegistrationState {
  return {
    contextName: '',
    contextCloudProviderAlias: '',
    contextRegion: '',
    contextInstanceType: '',
    creatingContext: false,
    createContextError: '',
    createContextConflict: '',
    contextPreviewPlan: null,

    envName: '',
    envType: 'runtime',
    envContextId: '',
    envKubernetesContext: '',
    envRuntimeVersion: '',
    envAdopt: false,
    envPreviewing: false,
    envPreviewError: '',
    envPreviewPlan: null,
    envPreviewQuotaOk: null,
    envRegistering: false,
    envRegisterError: '',
    envRegisterConflict: '',

    envActions: {},
    deployVersionDrafts: {},
    deleteConfirmingEnvironmentId: '',
    deleteConfirmationDraft: '',
  };
}
