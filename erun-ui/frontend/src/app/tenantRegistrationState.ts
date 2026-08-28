// Registration tab state, split out of reviewDetailState.ts to keep that
// file under eslint's 500-line max-lines cap. Backs the desktop's own
// erun-platform registration surface: creating a cloud context, previewing
// provisioning, and registering/deploying/stopping/deleting a hosted
// environment — the objects TenantPlatformState.tsx's five-state readiness
// machine stops short of (an operator authenticated and enrolled has no
// desktop path to register anything on the platform beyond that point).

// EnvironmentActionState is one environment row's deploy/stop/delete busy
// state, keyed by environmentId in RegistrationState.envActions. action is
// '' when idle; conflictMessage/unavailableMessage carry the platform's own
// wording for a recoverable "conflict"/"unavailable" outcome (a quota cap,
// a deploy already in flight, no deploy executor configured) — rendered
// inline as an expected state, never as a raw error, mirroring the
// console's DeployOutcome/feedbackRole pattern this mirrors.
export interface EnvironmentActionState {
  action: 'deploy' | 'stop' | 'delete' | '';
  error: string;
  conflictMessage: string;
  unavailableMessage: string;
}

export interface RegistrationState {
  // Create-context form.
  contextName: string;
  contextCloudProviderAlias: string;
  contextRegion: string;
  contextInstanceType: string;
  creatingContext: boolean;
  createContextError: string;
  createContextConflict: string;
  contextPreviewPlan: string[] | null;

  // Provision preview: always a preview, never a write, shown before a
  // register action so an operator sees quota/placement/namespace/deploy
  // before anything is created.
  previewEnvName: string;
  previewEnvType: string;
  previewKubernetesContext: string;
  previewing: boolean;
  previewError: string;
  previewPlan: string[] | null;
  previewQuotaOk: boolean | null;

  // Register-environment form.
  registerName: string;
  registerType: string;
  registerContextId: string;
  registerKubernetesContext: string;
  registerRuntimeVersion: string;
  registering: boolean;
  registerError: string;
  registerConflict: string;

  // Per-environment deploy/stop/delete state, plus each row's own deploy-
  // version draft (kept apart so it survives while a different row is busy).
  envActions: Record<string, EnvironmentActionState>;
  deployVersionDrafts: Record<string, string>;
  // deleteConfirmingEnvironmentId/deleteConfirmationDraft back the type-the-
  // name confirmation an unrecoverable delete requires (root AGENTS.md
  // Design-Language Decision Record), mirroring ManageDialogDeleteTab's
  // pattern for the Registration tab's own row-inline delete.
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

    previewEnvName: '',
    previewEnvType: 'runtime',
    previewKubernetesContext: '',
    previewing: false,
    previewError: '',
    previewPlan: null,
    previewQuotaOk: null,

    registerName: '',
    registerType: 'runtime',
    registerContextId: '',
    registerKubernetesContext: '',
    registerRuntimeVersion: '',
    registering: false,
    registerError: '',
    registerConflict: '',

    envActions: {},
    deployVersionDrafts: {},
    deleteConfirmingEnvironmentId: '',
    deleteConfirmationDraft: '',
  };
}
