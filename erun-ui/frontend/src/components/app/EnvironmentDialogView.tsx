import { FolderPlus, LoaderCircle, Rocket } from 'lucide-react';
import * as React from 'react';

import { refreshKubernetesContexts } from '@/app/dialogContextsThunks';
import {
  closeEnvironmentDialog,
  selectEnvironmentVersionSuggestion,
  setEnvironmentVersionChoicesOpen,
  submitEnvironmentDialog,
  updateEnvironmentDialog,
} from '@/app/environmentDialogThunks';
import { readError } from '@/app/errors';
import { useAppDispatch, useAppSelector } from '@/app/hooks';
import { showTerminalMessage } from '@/app/notificationThunks';
import { runtimeResourceLimitMessage } from '@/app/runtimeResources';
import type { AppState } from '@/app/state';
import {
  loadSavedPastContainerRegistries,
  loadSavedPastEnvironments,
  loadSavedPastTenants,
} from '@/app/storage';
import { useController } from '@/app/useController';
import { findVersionSuggestion, selectedVersionSourceText } from '@/app/versionSuggestions';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Label } from '@/components/ui/label';

import { EditableComboField } from './EditableComboField';
import { uniqueSuggestions } from './EditableComboField.helpers';
import { EmptyState } from './EmptyState';
import { ReadonlyField } from './ManageDialog.fields';
import { RuntimeResourceControls } from './RuntimeResourceControls';
import { SelectField } from './SelectField';
import { VersionField } from './VersionField';

const dialogErrorClassName =
  'rounded-[var(--radius)] border border-[color-mix(in_oklch,var(--destructive)_36%,transparent)] bg-[color-mix(in_oklch,var(--destructive)_8%,transparent)] px-[11px] py-[9px] text-[13px] leading-[1.35] text-destructive [overflow-wrap:anywhere]';

type EnvironmentDialog = AppState['environmentDialog'];

export function EnvironmentDialogView(): React.ReactElement {
  const controller = useController();
  const dispatch = useAppDispatch();
  const dialog = useAppSelector((state) => state.environmentDialog);
  const tenantRef = React.useRef<HTMLInputElement>(null);
  const environmentRef = React.useRef<HTMLInputElement>(null);

  // tenantRefValue mirrors dialog.tenant via a separate effect so the
  // focus-on-open effect below stays scoped to dialog.open. Re-running on
  // every dialog.tenant change would yank focus while the user is typing.
  const tenantValueRef = React.useRef(dialog.tenant);
  React.useEffect(() => {
    tenantValueRef.current = dialog.tenant;
  }, [dialog.tenant]);

  React.useEffect(() => {
    if (!dialog.open) {
      return undefined;
    }
    const timeout = window.setTimeout(() => {
      const target = tenantValueRef.current ? environmentRef.current : tenantRef.current;
      target?.focus();
      target?.select();
    }, 0);
    return () => {
      window.clearTimeout(timeout);
    };
  }, [dialog.open]);

  return (
    <Dialog
      open={dialog.open}
      onOpenChange={(open) => {
        if (!open) dispatch(closeEnvironmentDialog());
      }}
    >
      <DialogContent
        className="sm:max-w-md"
        onCloseAutoFocus={(event) => {
          event.preventDefault();
          controller.focusTerminalSoon();
        }}
      >
        <form
          className="grid gap-4"
          onSubmit={(event) => {
            event.preventDefault();
            void dispatch(submitEnvironmentDialog(event.currentTarget)).catch((error: unknown) => {
              dispatch(showTerminalMessage(readError(error)));
            });
          }}
        >
          <EnvironmentDialogHeader dialog={dialog} />
          <EnvironmentDialogFields tenantRef={tenantRef} environmentRef={environmentRef} />
          <DialogError error={dialog.error} />
          <EnvironmentDialogFooter dialog={dialog} />
        </form>
      </DialogContent>
    </Dialog>
  );
}

function EnvironmentDialogHeader({ dialog }: { dialog: EnvironmentDialog }): React.ReactElement {
  const isDeploy = dialog.actionMode === 'deploy';
  return (
    <DialogHeader>
      <DialogTitle>{isDeploy ? 'Deploy environment' : 'New environment'}</DialogTitle>
      <DialogDescription>{environmentDialogDescription(dialog, isDeploy)}</DialogDescription>
    </DialogHeader>
  );
}

function environmentDialogDescription(dialog: EnvironmentDialog, isDeploy: boolean): string {
  if (isDeploy) {
    if (dialog.tenant && dialog.environment) {
      return `Roll out a new version to ${dialog.tenant} / ${dialog.environment}.`;
    }
    return 'Roll out a new version to the selected environment.';
  }
  if (dialog.tenant && dialog.environment) {
    return `Create ${dialog.tenant} / ${dialog.environment}.`;
  }
  return 'Enter the tenant and environment name to create.';
}

function EnvironmentDialogFields({
  tenantRef,
  environmentRef,
}: {
  tenantRef: React.Ref<HTMLInputElement>;
  environmentRef: React.Ref<HTMLInputElement>;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const dialog = useAppSelector((state) => state.environmentDialog);
  const versionSuggestions = useAppSelector((state) => state.tenants.versionSuggestions);
  const isDeploy = dialog.actionMode === 'deploy';
  return (
    <>
      <EnvironmentNameFields tenantRef={tenantRef} environmentRef={environmentRef} />
      <VersionField
        id="environment-version"
        value={dialog.version}
        sourceText={selectedVersionSourceText(
          findVersionSuggestion(versionSuggestions, dialog.version, dialog.versionImage),
        )}
        suggestions={versionSuggestions}
        choicesOpen={dialog.choicesOpen}
        required={isDeploy}
        disabled={dialog.busy}
        onValueChange={(version) => {
          dispatch(updateEnvironmentDialog({ version }));
        }}
        onChoicesOpenChange={(open) => {
          dispatch(setEnvironmentVersionChoicesOpen(open));
        }}
        onSelect={(suggestion) => {
          dispatch(selectEnvironmentVersionSuggestion(suggestion));
        }}
      />
      {!isDeploy && <EnvironmentCreateFields dialog={dialog} />}
    </>
  );
}

function EnvironmentNameFields({
  tenantRef,
  environmentRef,
}: {
  tenantRef: React.Ref<HTMLInputElement>;
  environmentRef: React.Ref<HTMLInputElement>;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const dialog = useAppSelector((state) => state.environmentDialog);
  const tenants = useAppSelector((state) => state.tenants.tenants);
  const isDeploy = dialog.actionMode === 'deploy';
  const tenantSuggestions = React.useMemo(
    () =>
      uniqueSuggestions([
        dialog.tenant,
        ...tenants.map((tenant) => tenant.name),
        ...loadSavedPastTenants(),
      ]),
    [dialog.tenant, tenants],
  );
  const environmentSuggestions = React.useMemo(
    () => environmentNameSuggestions(tenants, dialog),
    [dialog, tenants],
  );

  // In deploy mode the tenant + environment are already selected and the
  // user cannot change them, so render them as ReadonlyField (matching the
  // ManageDialog General tab) instead of disabled editable inputs that
  // imply editability. AGENTS.md "empty states must not look like disabled
  // inputs" — same anti-pattern for fixed values.
  if (isDeploy) {
    return (
      <>
        <ReadonlyField id="environment-tenant" label="Tenant" value={dialog.tenant} />
        <ReadonlyField id="environment-name" label="Environment" value={dialog.environment} />
      </>
    );
  }
  return (
    <>
      <EditableComboField
        id="environment-tenant"
        inputRef={tenantRef}
        label="Tenant"
        value={dialog.tenant}
        suggestions={tenantSuggestions}
        required
        disabled={dialog.busy}
        onValueChange={(tenant) => {
          dispatch(updateEnvironmentDialog({ tenant }));
        }}
      />
      <EditableComboField
        id="environment-name"
        inputRef={environmentRef}
        label="Environment"
        value={dialog.environment}
        suggestions={environmentSuggestions}
        required
        disabled={dialog.busy}
        onValueChange={(environment) => {
          dispatch(updateEnvironmentDialog({ environment }));
        }}
      />
    </>
  );
}

function EnvironmentCreateFields({ dialog }: { dialog: EnvironmentDialog }): React.ReactElement {
  const dispatch = useAppDispatch();
  const containerRegistrySuggestions = React.useMemo(
    () =>
      uniqueSuggestions([
        dialog.containerRegistry,
        ...loadSavedPastContainerRegistries(),
        'erunpaas',
      ]),
    [dialog.containerRegistry],
  );

  return (
    <>
      <KubernetesContextSelect dialog={dialog} />
      <RuntimePodFields dialog={dialog} />
      <EditableComboField
        id="environment-container-registry"
        label="Container registry"
        value={dialog.containerRegistry}
        suggestions={containerRegistrySuggestions}
        required
        disabled={dialog.busy}
        onValueChange={(containerRegistry) => {
          dispatch(updateEnvironmentDialog({ containerRegistry }));
        }}
      />
      <EnvironmentCreateChecks dialog={dialog} />
    </>
  );
}

function KubernetesContextSelect({ dialog }: { dialog: EnvironmentDialog }): React.ReactElement {
  const dispatch = useAppDispatch();
  const items = dialog.kubernetesContexts.map((context) => ({ value: context, label: context }));
  const placeholder = dialog.kubernetesContextsLoading
    ? 'Loading contexts...'
    : 'Select Kubernetes context';
  if (!dialog.kubernetesContextsLoading && dialog.kubernetesContexts.length === 0) {
    const body =
      "ERun runs `kubectl config get-contexts` using the PATH and KUBECONFIG it inherits from your login shell at startup. If your terminal sees contexts that don't appear here, set KUBECONFIG in ~/.zshenv (or ~/.bash_profile) so it applies to GUI launches too, then restart ERun. If kubectl is not yet installed, install it with `brew install kubectl`.";
    const errorDetail = dialog.error?.trim() ?? '';
    return (
      <div className="grid gap-2">
        <Label htmlFor="environment-kubernetes-context">Kubernetes context</Label>
        <EmptyState
          heading="No Kubernetes contexts found"
          body={errorDetail !== '' ? `${body}\n\nLast error from kubectl:\n${errorDetail}` : body}
          action={
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => {
                void dispatch(refreshKubernetesContexts());
              }}
            >
              Rescan
            </Button>
          }
        />
      </div>
    );
  }
  return (
    <SelectField
      id="environment-kubernetes-context"
      label="Kubernetes context"
      value={dialog.kubernetesContext}
      options={items}
      placeholder={placeholder}
      emptyLabel="No Kubernetes contexts"
      disabled={dialog.busy || dialog.kubernetesContextsLoading}
      required
      onChange={(kubernetesContext) => {
        dispatch(updateEnvironmentDialog({ kubernetesContext }));
      }}
    />
  );
}

function RuntimePodFields({ dialog }: { dialog: EnvironmentDialog }): React.ReactElement {
  const dispatch = useAppDispatch();
  return (
    <RuntimeResourceControls
      idPrefix="environment-runtime"
      value={dialog.runtimePod}
      status={dialog.resourceStatus}
      loading={dialog.resourceStatusLoading}
      disabled={dialog.busy}
      onChange={(runtimePod) => {
        dispatch(updateEnvironmentDialog({ runtimePod }));
      }}
    />
  );
}

function EnvironmentCreateChecks({ dialog }: { dialog: EnvironmentDialog }): React.ReactElement {
  const dispatch = useAppDispatch();
  return (
    <div className="grid gap-3">
      <CheckboxField
        id="environment-default-tenant"
        label="Set as default tenant"
        checked={dialog.setDefaultTenant}
        disabled={dialog.busy}
        onCheckedChange={(setDefaultTenant) => {
          dispatch(updateEnvironmentDialog({ setDefaultTenant }));
        }}
      />
      <CheckboxField
        id="environment-no-git"
        label="Initialize without Git checkout"
        checked={dialog.noGit}
        disabled={dialog.busy}
        onCheckedChange={(noGit) => {
          dispatch(updateEnvironmentDialog({ noGit }));
        }}
      />
      <CheckboxField
        id="environment-bootstrap"
        label="Create tenant DevOps repository"
        helper="Generates the tenant's shared DevOps module — Helm values and deployment templates reused by every environment in this tenant."
        checked={dialog.bootstrap}
        disabled={dialog.busy}
        onCheckedChange={(bootstrap) => {
          dispatch(updateEnvironmentDialog({ bootstrap }));
        }}
      />
    </div>
  );
}

function CheckboxField({
  id,
  label,
  helper,
  checked,
  disabled,
  onCheckedChange,
}: {
  id: string;
  label: string;
  helper?: string;
  checked: boolean;
  disabled: boolean;
  onCheckedChange: (checked: boolean) => void;
}): React.ReactElement {
  const helperId = helper ? `${id}-helper` : undefined;
  return (
    <div className="grid gap-1">
      <div className="flex items-center gap-2">
        <Checkbox
          id={id}
          checked={checked}
          disabled={disabled}
          onCheckedChange={(value) => {
            onCheckedChange(value === true);
          }}
          aria-describedby={helperId}
        />
        <Label htmlFor={id} className="text-sm font-normal">
          {label}
        </Label>
      </div>
      {helper && (
        <p id={helperId} className="text-[12px] leading-[1.4] text-muted-foreground pl-6">
          {helper}
        </p>
      )}
    </div>
  );
}

function DialogError({ error }: { error: string }): React.ReactElement | null {
  return error ? (
    <div className={dialogErrorClassName} role="alert">
      {error}
    </div>
  ) : null;
}

interface EnvironmentSubmitGate {
  disabled: boolean;
  reason: string;
}

function environmentDialogSubmitGate(
  dialog: EnvironmentDialog,
  isDeploy: boolean,
): EnvironmentSubmitGate {
  if (dialog.busy) {
    return { disabled: true, reason: '' };
  }
  if (isDeploy) {
    return { disabled: false, reason: '' };
  }
  return environmentCreateSubmitGate(dialog);
}

// environmentCreateSubmitGate isolates the create-mode preconditions so
// the outer gate stays within the eslint complexity ceiling. The
// returned reason is rendered next to the disabled submit button to
// satisfy Nielsen #5 (error prevention) — users see why submit is
// blocked instead of guessing. Preconditions are checked in order and
// the first match wins.
function environmentCreateSubmitGate(dialog: EnvironmentDialog): EnvironmentSubmitGate {
  const blocker = kubernetesContextBlocker(dialog) ?? runtimeCapacityBlocker(dialog);
  return blocker ? { disabled: true, reason: blocker } : { disabled: false, reason: '' };
}

function kubernetesContextBlocker(dialog: EnvironmentDialog): string | null {
  if (dialog.kubernetesContextsLoading) {
    return 'Loading Kubernetes contexts…';
  }
  if (dialog.kubernetesContexts.length === 0) {
    return 'No Kubernetes contexts available.';
  }
  return null;
}

function runtimeCapacityBlocker(dialog: EnvironmentDialog): string | null {
  if (dialog.resourceStatusLoading) {
    return 'Checking runtime capacity…';
  }
  if (!dialog.resourceStatus?.available) {
    const fallback = dialog.resourceStatus?.message?.trim() ?? '';
    return fallback || 'Runtime capacity is unavailable.';
  }
  const limitMessage = runtimeResourceLimitMessage(dialog.runtimePod, dialog.resourceStatus);
  return limitMessage || null;
}

function EnvironmentDialogFooter({ dialog }: { dialog: EnvironmentDialog }): React.ReactElement {
  const dispatch = useAppDispatch();
  const isDeploy = dialog.actionMode === 'deploy';
  const gate = environmentDialogSubmitGate(dialog, isDeploy);
  return (
    <DialogFooter className="items-center sm:justify-between">
      <p
        id="environment-dialog-submit-reason"
        className="text-left text-[12px] leading-[1.35] text-muted-foreground [overflow-wrap:anywhere] sm:max-w-[60%]"
        role="status"
        aria-live="polite"
      >
        {gate.reason}
      </p>
      <div className="flex justify-end gap-2">
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={dialog.busy}
          onClick={() => {
            dispatch(closeEnvironmentDialog());
          }}
        >
          Cancel
        </Button>
        <Button
          type="submit"
          size="sm"
          disabled={gate.disabled}
          aria-describedby={gate.reason ? 'environment-dialog-submit-reason' : undefined}
        >
          <EnvironmentSubmitIcon dialog={dialog} />
          {dialog.busy
            ? isDeploy
              ? 'Deploying...'
              : 'Creating...'
            : isDeploy
              ? 'Deploy'
              : 'Create'}
        </Button>
      </div>
    </DialogFooter>
  );
}

function EnvironmentSubmitIcon({ dialog }: { dialog: EnvironmentDialog }): React.ReactElement {
  if (dialog.busy) {
    return <LoaderCircle className="animate-spin" aria-hidden="true" />;
  }
  return dialog.actionMode === 'deploy' ? (
    <Rocket aria-hidden="true" />
  ) : (
    <FolderPlus aria-hidden="true" />
  );
}

function environmentNameSuggestions(
  tenants: AppState['tenants'],
  dialog: EnvironmentDialog,
): string[] {
  const selectedTenant = tenants.find(
    (tenant) => tenant.name.toLowerCase() === dialog.tenant.trim().toLowerCase(),
  );
  const selectedTenantEnvironments =
    selectedTenant?.environments.map((environment) => environment.name) ?? [];
  const allEnvironments = tenants.flatMap((tenant) =>
    tenant.environments.map((environment) => environment.name),
  );
  return uniqueSuggestions([
    dialog.environment,
    ...selectedTenantEnvironments,
    ...loadSavedPastEnvironments(),
    ...allEnvironments,
  ]);
}
