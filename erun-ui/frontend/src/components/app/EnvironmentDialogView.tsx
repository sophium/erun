import { FolderPlus, LoaderCircle } from 'lucide-react';
import * as React from 'react';

import { missingRequiredFieldReason } from '@/app/environmentDialogState';
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
import { EnvironmentTypeSelect, LocalRepoPathField } from './EnvironmentTypeFields';
import { KubernetesContextSelect } from './KubernetesContextSelect';
import { RuntimeResourceControls } from './RuntimeResourceControls';
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

  // The focus-on-open effect below reads the tenant through this ref so it stays
  // scoped to dialog.open; adding dialog.tenant to its deps would yank focus while the user types.
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
        // Bound the panel to the viewport and let the field region scroll, so the
        // top fields (Tenant/Environment) stay reachable and the footer stays
        // visible on short windows — a plain centered grid overflowed off both
        // edges with nothing scrollable. Overrides the shadcn base grid/gap/p via
        // tailwind-merge; no generated ui/dialog.tsx edit needed.
        className="flex max-h-[85vh] flex-col gap-0 overflow-hidden p-0 sm:max-w-md"
        onCloseAutoFocus={(event) => {
          event.preventDefault();
          controller.focusTerminalSoon();
        }}
      >
        <form
          className="flex min-h-0 flex-1 flex-col"
          onSubmit={(event) => {
            event.preventDefault();
            void dispatch(submitEnvironmentDialog(event.currentTarget)).catch((error: unknown) => {
              dispatch(showTerminalMessage(readError(error)));
            });
          }}
        >
          <div className="shrink-0 px-6 pt-6 pb-4">
            <EnvironmentDialogHeader dialog={dialog} />
          </div>
          <div className="grid min-h-0 flex-1 gap-4 overflow-y-auto px-6 pb-1">
            <EnvironmentDialogFields tenantRef={tenantRef} environmentRef={environmentRef} />
            <DialogError error={dialog.error} />
          </div>
          <div className="shrink-0 border-t px-6 pt-4 pb-6">
            <EnvironmentDialogFooter dialog={dialog} />
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function EnvironmentDialogHeader({ dialog }: { dialog: EnvironmentDialog }): React.ReactElement {
  return (
    <DialogHeader>
      <DialogTitle>New environment</DialogTitle>
      <DialogDescription>{environmentDialogDescription(dialog)}</DialogDescription>
    </DialogHeader>
  );
}

function environmentDialogDescription(dialog: EnvironmentDialog): string {
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
  const versionNotices = useAppSelector((state) => state.tenants.versionSuggestionNotices);
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
        notices={versionNotices}
        choicesOpen={dialog.choicesOpen}
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
      <EnvironmentCreateFields dialog={dialog} />
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
  return (
    <>
      <EnvironmentTypeSelect dialog={dialog} />
      {dialog.envType === 'local-agent' && <LocalRepoPathField dialog={dialog} />}
      <KubernetesContextSelect dialog={dialog} />
      <RuntimePodFields dialog={dialog} />
      <ContainerRegistryField dialog={dialog} />
      <EnvironmentCreateChecks dialog={dialog} />
    </>
  );
}

// ContainerRegistryField offers the in-cluster erun-registry (resolved from the
// selected Kubernetes context) as the default when one is detected, and falls
// back to a free-text registry otherwise. There is no hardcoded default host —
// the deployed cluster registry is the default, not a placeholder like erunpaas.
function ContainerRegistryField({ dialog }: { dialog: EnvironmentDialog }): React.ReactElement {
  const dispatch = useAppDispatch();
  const containerRegistrySuggestions = React.useMemo(
    () => uniqueSuggestions([dialog.containerRegistry, ...loadSavedPastContainerRegistries()]),
    [dialog.containerRegistry],
  );
  const cluster = dialog.clusterRegistry;
  const clusterAvailable = cluster?.deployed === true;
  const useCluster = clusterAvailable && dialog.useClusterRegistry;
  const clusterToggle = clusterAvailable ? (
    <label className="flex items-center gap-2 text-sm font-normal">
      <Checkbox
        id="environment-use-cluster-registry"
        checked={dialog.useClusterRegistry}
        disabled={dialog.busy}
        onCheckedChange={(value) => {
          dispatch(updateEnvironmentDialog({ useClusterRegistry: value === true }));
        }}
      />
      Use in-cluster registry ({cluster.service ?? 'erun-registry'})
    </label>
  ) : null;

  if (useCluster) {
    return (
      <div className="grid gap-2">
        <Label htmlFor="environment-use-cluster-registry">Container registry</Label>
        {clusterToggle}
        <p className="text-[12px] leading-[1.4] text-muted-foreground">
          Resolved from {cluster.service}.{cluster.namespace}:{cluster.port} via this
          environment&apos;s Kubernetes context — pushed and pulled in-cluster, no host address
          needed.
        </p>
      </div>
    );
  }

  return (
    <div className="grid gap-2">
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
      {clusterToggle}
    </div>
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
  // "Initialize without Git checkout" is a no-op for local-agent envs, so hide it there.
  const isLocalAgent = dialog.envType === 'local-agent';
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
      {!isLocalAgent && (
        <CheckboxField
          id="environment-no-git"
          label="Initialize without Git checkout"
          checked={dialog.noGit}
          disabled={dialog.busy}
          onCheckedChange={(noGit) => {
            dispatch(updateEnvironmentDialog({ noGit }));
          }}
        />
      )}
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

// The returned reason is rendered next to the disabled submit button so users see
// why submit is blocked instead of guessing (Nielsen #5, error prevention).
function environmentDialogSubmitGate(dialog: EnvironmentDialog): EnvironmentSubmitGate {
  if (dialog.busy) {
    return { disabled: true, reason: '' };
  }
  // kubernetesContextBlocker (contexts loading / none discovered) is an
  // environmental blocker and must win over missingRequiredFieldReason, which
  // would otherwise say "Select a Kubernetes context" when there are none to
  // select. missingRequiredFieldReason then covers the value requirements —
  // including a context that is available but not yet selected — so the button's
  // enabled state matches exactly what submitEnvironmentDialog will accept.
  const blocker =
    kubernetesContextBlocker(dialog) ??
    missingRequiredFieldReason(dialog) ??
    runtimeCapacityBlocker(dialog);
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
  const gate = environmentDialogSubmitGate(dialog);
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
          {dialog.busy ? 'Creating...' : 'Create'}
        </Button>
      </div>
    </DialogFooter>
  );
}

function EnvironmentSubmitIcon({ dialog }: { dialog: EnvironmentDialog }): React.ReactElement {
  if (dialog.busy) {
    return <LoaderCircle className="animate-spin" aria-hidden="true" />;
  }
  return <FolderPlus aria-hidden="true" />;
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
