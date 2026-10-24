import {
  Button,
  Checkbox,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  EditableComboField,
  Label,
  uniqueSuggestions,
} from 'erun-kit';
import { FolderPlus, LoaderCircle } from 'lucide-react';
import * as React from 'react';

import {
  environmentDialogResourceLimitMessage,
  missingRequiredFieldReason,
} from '@/app/environmentDialogState';
import {
  closeEnvironmentDialog,
  selectEnvironmentVersionSuggestion,
  setEnvironmentVersionChoicesOpen,
  submitEnvironmentDialog,
  updateEnvironmentDialog,
} from '@/app/environmentDialogThunks';
import { environmentTypeIsHost } from '@/app/environmentType';
import { readError } from '@/app/errors';
import { useAppDispatch, useAppSelector } from '@/app/hooks';
import { showTerminalError } from '@/app/notificationThunks';
import type { AppState } from '@/app/state';
import { loadSavedPastEnvironments, loadSavedPastTenants } from '@/app/storage';
import { useController } from '@/app/useController';
import { findVersionSuggestion, selectedVersionSourceText } from '@/app/versionSuggestions';
import { ContainerRegistryField } from '@/components/app/EnvironmentDialogView.RegistryField';
import { InlineAlert } from '@/components/app/InlineAlert';

import { EnvironmentTypeSelect, LocalRepoPathField } from './EnvironmentTypeFields';
import { KubernetesContextSelect } from './KubernetesContextSelect';
import { RuntimeResourceControls } from './RuntimeResourceControls';
import { VersionField } from './VersionField';

type EnvironmentDialog = AppState['environmentDialog'];

export function EnvironmentDialogView(): React.ReactElement {
  const controller = useController();
  const dispatch = useAppDispatch();
  const dialog = useAppSelector((state) => state.environmentDialog);
  const tenantRef = React.useRef<HTMLInputElement>(null);
  const environmentRef = React.useRef<HTMLInputElement>(null);

  // Read through a ref so typing in tenant does not re-run the focus-on-open effect.
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
        // Bound to the viewport with a scrolling field region so the top fields
        // and footer stay reachable on short windows.
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
              dispatch(showTerminalError(readError(error)));
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
  // A host env has no pod or cluster, so cluster fields are hidden.
  const isHost = dialog.envType === 'host';
  return (
    <>
      <EnvironmentTypeSelect dialog={dialog} />
      {(dialog.envType === 'local-agent' || isHost) && <LocalRepoPathField dialog={dialog} />}
      {!isHost && (
        <>
          <KubernetesContextSelect dialog={dialog} />
          <RuntimePodFields dialog={dialog} />
          <ContainerRegistryField dialog={dialog} />
        </>
      )}
      <EnvironmentCreateChecks dialog={dialog} />
    </>
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
  const skipsRemoteCheckoutAlready = dialog.envType === 'local-agent' || dialog.envType === 'host';
  return (
    <div className="grid gap-3">
      <CheckboxField
        id="environment-default-tenant"
        label="Set as default tenant"
        helper="Off by default — creating an environment won't repoint your default tenant unless you opt in."
        checked={dialog.setDefaultTenant}
        disabled={dialog.busy}
        onCheckedChange={(setDefaultTenant) => {
          dispatch(updateEnvironmentDialog({ setDefaultTenant }));
        }}
      />
      {!skipsRemoteCheckoutAlready && (
        <CheckboxField
          id="environment-no-git"
          label="Skip Git checkout"
          helper="Create the environment without cloning a Git repo into it — skips the SSH-key import and remote-worktree setup."
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
  return error ? <InlineAlert>{error}</InlineAlert> : null;
}

interface EnvironmentSubmitGate {
  disabled: boolean;
  reason: string;
}

function environmentDialogSubmitGate(dialog: EnvironmentDialog): EnvironmentSubmitGate {
  if (dialog.busy) {
    return { disabled: true, reason: '' };
  }
  // Environmental blockers win over missingRequiredFieldReason, which would
  // otherwise ask to select a context when none exist. Neither applies to a host env.
  const isHost = environmentTypeIsHost(dialog.envType);
  const blocker =
    (isHost ? null : kubernetesContextBlocker(dialog)) ??
    missingRequiredFieldReason(dialog) ??
    (isHost ? null : runtimeCapacityBlocker(dialog));
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
  const limitMessage = environmentDialogResourceLimitMessage(dialog);
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
