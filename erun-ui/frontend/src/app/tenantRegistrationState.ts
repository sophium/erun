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

  // One environment form backs both Preview and Register: the two actions
  // submit the exact same fields to the same POST /v1/environments route
  // (preview:true / preview:false), so the plan an operator previews can
  // never diverge from what register then does. envAdopt is "this
  // environment already exists" — set from the register-on-the-row
  // affordance (see RegistrationEnvironmentsSection's local-environments
  // list) or toggled by hand; it requires envKubernetesContext and forbids
  // envContextId/envRuntimeVersion, matching the platform's own adopt
  // validation.
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
