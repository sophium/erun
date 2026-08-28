import { Button, FieldLabel, Input, SelectField } from 'erun-kit';
import { LoaderCircle, Plus } from 'lucide-react';
import * as React from 'react';

import { useAppDispatch } from '@/app/hooks';
import { contextOptions, ENV_TYPE_OPTIONS, NO_CONTEXT } from '@/app/tenantRegistrationFormOptions';
import type { RegistrationState } from '@/app/tenantRegistrationState';
import {
  previewProvision,
  registerPlatformEnvironment,
  updateRegistrationDraft,
} from '@/app/tenantRegistrationThunks';

import { InlineAlert } from './InlineAlert';
import { type TenantDashboardData } from './TenantDashboardMessage';
import { PlanList } from './TenantDashboardPanels.Registration';

// TenantDashboardPanels.RegistrationForms.tsx holds the Registration tab's
// two "before you create anything" forms — split out of
// TenantDashboardPanels.RegistrationEnvironments.tsx to keep that file under
// eslint's 500-line cap. ProvisionPreviewSection always previews (rule #3:
// shown before any register action); RegisterEnvironmentSection is the
// distinct, deliberate register action that follows it.

function ProvisionPreviewFields({ draft }: { draft: RegistrationState }): React.ReactElement {
  const dispatch = useAppDispatch();
  const busy = draft.previewing;
  return (
    <>
      <div className="grid gap-2">
        <FieldLabel htmlFor="preview-env-name" required>
          Environment name
        </FieldLabel>
        <Input
          id="preview-env-name"
          value={draft.previewEnvName}
          disabled={busy}
          onChange={(event) => {
            dispatch(updateRegistrationDraft({ previewEnvName: event.target.value }));
          }}
        />
      </div>
      <SelectField
        id="preview-env-type"
        label="Type"
        value={draft.previewEnvType}
        options={ENV_TYPE_OPTIONS}
        disabled={busy}
        onChange={(value) => {
          dispatch(updateRegistrationDraft({ previewEnvType: value }));
        }}
      />
      <div className="grid gap-2">
        <FieldLabel htmlFor="preview-kube-context">Kubernetes context (optional)</FieldLabel>
        <Input
          id="preview-kube-context"
          value={draft.previewKubernetesContext}
          disabled={busy}
          onChange={(event) => {
            dispatch(updateRegistrationDraft({ previewKubernetesContext: event.target.value }));
          }}
        />
      </div>
    </>
  );
}

function ProvisionPreviewFeedback({ draft }: { draft: RegistrationState }): React.ReactElement {
  return (
    <>
      {draft.previewPlan && (
        <div className="grid gap-2">
          <PlanList plan={draft.previewPlan} />
          <p role="status" className="text-[13px] text-muted-foreground">
            {draft.previewQuotaOk
              ? 'Quota ok: this plan can register without hitting the tenant cap.'
              : 'Quota exceeded: registering this plan would hit the tenant cap. Delete or stop another environment first.'}
          </p>
        </div>
      )}
      {draft.previewError && <InlineAlert>{draft.previewError}</InlineAlert>}
    </>
  );
}

// ProvisionPreviewSection resolves the ordered plan — quota, placement,
// namespace, register, deploy — for a drafted environment without creating
// anything (rule #3). RegisterEnvironmentSection is deliberately a distinct
// form/action below, so registering is never one click past a preview the
// operator has not seen.
export function ProvisionPreviewSection({
  data,
  draft,
}: {
  data: TenantDashboardData;
  draft: RegistrationState;
}): React.ReactElement | null {
  const dispatch = useAppDispatch();
  if (data?.canPreviewProvision !== true) {
    return null;
  }
  const busy = draft.previewing;
  const canSubmit = draft.previewEnvName.trim() !== '';
  return (
    <section className="grid gap-3">
      <h3 className="text-sm font-medium text-foreground">
        Preview provisioning a hosted environment
      </h3>
      <form
        className="grid max-w-md gap-3"
        onSubmit={(event) => {
          event.preventDefault();
          void dispatch(previewProvision());
        }}
      >
        <ProvisionPreviewFields draft={draft} />
        <Button
          type="submit"
          variant="outline"
          disabled={busy || !canSubmit}
          className="justify-self-start"
        >
          {busy && <LoaderCircle className="animate-spin" aria-hidden="true" />}
          {busy ? 'Previewing…' : 'Preview provisioning plan'}
        </Button>
      </form>
      <ProvisionPreviewFeedback draft={draft} />
    </section>
  );
}

function RegisterEnvironmentFields({
  data,
  draft,
}: {
  data: TenantDashboardData;
  draft: RegistrationState;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const busy = draft.registering;
  return (
    <>
      <div className="grid gap-2">
        <FieldLabel htmlFor="register-env-name" required>
          Name
        </FieldLabel>
        <Input
          id="register-env-name"
          value={draft.registerName}
          disabled={busy}
          onChange={(event) => {
            dispatch(updateRegistrationDraft({ registerName: event.target.value }));
          }}
        />
      </div>
      <SelectField
        id="register-env-type"
        label="Type"
        value={draft.registerType}
        options={ENV_TYPE_OPTIONS}
        disabled={busy}
        onChange={(value) => {
          dispatch(updateRegistrationDraft({ registerType: value }));
        }}
      />
      <SelectField
        id="register-env-context"
        label="Cloud context"
        value={draft.registerContextId || NO_CONTEXT}
        options={contextOptions(data)}
        disabled={busy}
        onChange={(value) => {
          dispatch(
            updateRegistrationDraft({ registerContextId: value === NO_CONTEXT ? '' : value }),
          );
        }}
      />
      <div className="grid gap-2">
        <FieldLabel htmlFor="register-env-kube-context">
          Kubernetes context (if not using a cloud context above)
        </FieldLabel>
        <Input
          id="register-env-kube-context"
          value={draft.registerKubernetesContext}
          disabled={busy}
          onChange={(event) => {
            dispatch(updateRegistrationDraft({ registerKubernetesContext: event.target.value }));
          }}
        />
      </div>
      <div className="grid gap-2">
        <FieldLabel htmlFor="register-env-runtime-version">
          Runtime version (runtime environments only)
        </FieldLabel>
        <Input
          id="register-env-runtime-version"
          placeholder="1.2.3"
          value={draft.registerRuntimeVersion}
          disabled={busy}
          onChange={(event) => {
            dispatch(updateRegistrationDraft({ registerRuntimeVersion: event.target.value }));
          }}
        />
      </div>
    </>
  );
}

function RegisterEnvironmentFeedback({ draft }: { draft: RegistrationState }): React.ReactElement {
  return (
    <>
      {draft.registerConflict && (
        <p role="status" className="text-[13px] text-muted-foreground">
          {draft.registerConflict}
        </p>
      )}
      {draft.registerError && <InlineAlert>{draft.registerError}</InlineAlert>}
    </>
  );
}

// registerPlatformEnvironment is a quota-cap refusal renders as
// registerConflict, the recoverable state (5): the operator's next action is
// deleting or stopping another environment, not retrying blindly.
export function RegisterEnvironmentSection({
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
  const busy = draft.registering;
  const canSubmit = draft.registerName.trim() !== '';
  return (
    <section className="grid gap-3">
      <h3 className="text-sm font-medium text-foreground">Register a hosted environment</h3>
      <form
        className="grid max-w-md gap-3"
        onSubmit={(event) => {
          event.preventDefault();
          void dispatch(registerPlatformEnvironment());
        }}
      >
        <RegisterEnvironmentFields data={data} draft={draft} />
        <Button type="submit" disabled={busy || !canSubmit} className="justify-self-start">
          {busy && <LoaderCircle className="animate-spin" aria-hidden="true" />}
          <Plus aria-hidden="true" />
          {busy ? 'Registering…' : 'Register environment'}
        </Button>
      </form>
      <RegisterEnvironmentFeedback draft={draft} />
    </section>
  );
}
