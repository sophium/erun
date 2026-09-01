import { Button, Checkbox, FieldLabel, Input, SelectField } from 'erun-kit';
import { LoaderCircle, Plus } from 'lucide-react';
import * as React from 'react';

import { useAppDispatch } from '@/app/hooks';
import { contextOptions, ENV_TYPE_OPTIONS, NO_CONTEXT } from '@/app/tenantRegistrationFormOptions';
import type { RegistrationState } from '@/app/tenantRegistrationState';
import {
  previewPlatformEnvironment,
  registerPlatformEnvironment,
  updateRegistrationDraft,
} from '@/app/tenantRegistrationThunks';

import { InlineAlert } from './InlineAlert';
import { type TenantDashboardData } from './TenantDashboardMessage';
import { PlanList } from './TenantDashboardPanels.Registration';

// TenantDashboardPanels.RegistrationForms.tsx holds the Registration tab's
// hosted-environment form — split out of
// TenantDashboardPanels.RegistrationEnvironments.tsx to keep that file under
// eslint's 500-line cap. One field set backs both actions: Preview resolves
// the ordered plan for exactly the fields Register would submit, and
// Register follows it — never a separate, drifted field set, so the plan an
// operator sees can never diverge from what submit does. envAdopt switches
// the form to record an environment that already exists: it hides the
// cloud-context and runtime-version fields (the platform forbids both for
// an adopt request) and requires a kubernetes context instead.

function EnvironmentAdoptToggle({
  draft,
  disabled,
}: {
  draft: RegistrationState;
  disabled: boolean;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  return (
    <label htmlFor="env-adopt" className="flex items-center gap-2 text-[13px] text-foreground">
      <Checkbox
        id="env-adopt"
        checked={draft.envAdopt}
        disabled={disabled}
        onCheckedChange={(checked) => {
          dispatch(
            updateRegistrationDraft({
              envAdopt: checked === true,
              envContextId: '',
              envRuntimeVersion: '',
              envPreviewPlan: null,
              envPreviewQuotaOk: null,
            }),
          );
        }}
      />
      This environment already exists — record it without provisioning or deploying anything
    </label>
  );
}

function EnvironmentFormFields({
  data,
  draft,
}: {
  data: TenantDashboardData;
  draft: RegistrationState;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const busy = draft.envPreviewing || draft.envRegistering;
  return (
    <>
      <div className="grid gap-2">
        <FieldLabel htmlFor="env-name" required>
          Name
        </FieldLabel>
        <Input
          id="env-name"
          value={draft.envName}
          disabled={busy}
          onChange={(event) => {
            dispatch(updateRegistrationDraft({ envName: event.target.value }));
          }}
        />
      </div>
      <SelectField
        id="env-type"
        label="Type"
        value={draft.envType}
        options={ENV_TYPE_OPTIONS}
        disabled={busy}
        onChange={(value) => {
          dispatch(updateRegistrationDraft({ envType: value }));
        }}
      />
      <EnvironmentAdoptToggle draft={draft} disabled={busy} />
      {!draft.envAdopt && (
        <SelectField
          id="env-context"
          label="Cloud context"
          value={draft.envContextId || NO_CONTEXT}
          options={contextOptions(data)}
          disabled={busy}
          onChange={(value) => {
            dispatch(updateRegistrationDraft({ envContextId: value === NO_CONTEXT ? '' : value }));
          }}
        />
      )}
      <div className="grid gap-2">
        <FieldLabel htmlFor="env-kube-context" required={draft.envAdopt}>
          {draft.envAdopt
            ? 'Kubernetes context'
            : 'Kubernetes context (if not using a cloud context above)'}
        </FieldLabel>
        <Input
          id="env-kube-context"
          value={draft.envKubernetesContext}
          disabled={busy}
          onChange={(event) => {
            dispatch(updateRegistrationDraft({ envKubernetesContext: event.target.value }));
          }}
        />
      </div>
      {!draft.envAdopt && (
        <div className="grid gap-2">
          <FieldLabel htmlFor="env-runtime-version">
            Runtime version (runtime environments only)
          </FieldLabel>
          <Input
            id="env-runtime-version"
            placeholder="1.2.3"
            value={draft.envRuntimeVersion}
            disabled={busy}
            onChange={(event) => {
              dispatch(updateRegistrationDraft({ envRuntimeVersion: event.target.value }));
            }}
          />
        </div>
      )}
    </>
  );
}

function EnvironmentPreviewFeedback({ draft }: { draft: RegistrationState }): React.ReactElement {
  return (
    <>
      {draft.envPreviewPlan && (
        <div className="grid gap-2">
          <PlanList plan={draft.envPreviewPlan} />
          <p role="status" className="text-[13px] text-muted-foreground">
            {draft.envPreviewQuotaOk
              ? 'Quota ok: this plan can register without hitting the tenant cap.'
              : 'Quota exceeded: registering this plan would hit the tenant cap. Delete or stop another environment first.'}
          </p>
        </div>
      )}
      {draft.envPreviewError && <InlineAlert>{draft.envPreviewError}</InlineAlert>}
    </>
  );
}

function EnvironmentRegisterFeedback({ draft }: { draft: RegistrationState }): React.ReactElement {
  return (
    <>
      {draft.envRegisterConflict && (
        <p role="status" className="text-[13px] text-muted-foreground">
          {draft.envRegisterConflict}
        </p>
      )}
      {draft.envRegisterError && <InlineAlert>{draft.envRegisterError}</InlineAlert>}
    </>
  );
}

function registerButtonLabel(draft: RegistrationState): string {
  if (draft.envRegistering) {
    return 'Registering…';
  }
  return draft.envAdopt ? 'Record environment' : 'Register environment';
}

function EnvironmentFormActions({ draft }: { draft: RegistrationState }): React.ReactElement {
  const dispatch = useAppDispatch();
  const previewBusy = draft.envPreviewing;
  const registerBusy = draft.envRegistering;
  const canSubmit =
    draft.envName.trim() !== '' && (!draft.envAdopt || draft.envKubernetesContext.trim() !== '');
  return (
    <div className="flex gap-2">
      <Button
        type="button"
        variant="outline"
        disabled={previewBusy || registerBusy || !canSubmit}
        onClick={() => {
          void dispatch(previewPlatformEnvironment());
        }}
      >
        {previewBusy && <LoaderCircle className="animate-spin" aria-hidden="true" />}
        {previewBusy ? 'Previewing…' : 'Preview plan'}
      </Button>
      <Button type="submit" disabled={previewBusy || registerBusy || !canSubmit}>
        {registerBusy && <LoaderCircle className="animate-spin" aria-hidden="true" />}
        <Plus aria-hidden="true" />
        {registerButtonLabel(draft)}
      </Button>
    </div>
  );
}

// EnvironmentSection is the Registration tab's one hosted-environment form:
// Preview (outline, never a write) always precedes Register (rule #3), and
// both submit the same fields, so registering is never one click past a
// preview that described something else.
export function EnvironmentSection({
  data,
  draft,
}: {
  data: TenantDashboardData;
  draft: RegistrationState;
}): React.ReactElement | null {
  const dispatch = useAppDispatch();
  if (data?.canRegisterEnvironment !== true) {
    return null;
  }
  return (
    <section className="grid gap-3">
      <h3 className="text-sm font-medium text-foreground">Hosted environment</h3>
      <form
        className="grid max-w-md gap-3"
        onSubmit={(event) => {
          event.preventDefault();
          void dispatch(registerPlatformEnvironment());
        }}
      >
        <EnvironmentFormFields data={data} draft={draft} />
        <EnvironmentFormActions draft={draft} />
      </form>
      <EnvironmentPreviewFeedback draft={draft} />
      <EnvironmentRegisterFeedback draft={draft} />
    </section>
  );
}
