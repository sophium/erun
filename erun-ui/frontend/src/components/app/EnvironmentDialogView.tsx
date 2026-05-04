import * as React from 'react';
import { FolderPlus, LoaderCircle, Rocket } from 'lucide-react';

import type { ERunUIController } from '@/app/ERunUIController';
import { readError } from '@/app/errors';
import { runtimeResourceLimitMessage } from '@/app/runtimeResources';
import type { AppState } from '@/app/state';
import { loadSavedPastContainerRegistries, loadSavedPastEnvironments, loadSavedPastTenants } from '@/app/storage';
import { findVersionSuggestion, selectedVersionSourceText } from '@/app/versionSuggestions';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Label } from '@/components/ui/label';
import { EditableComboField, uniqueSuggestions } from './EditableComboField';
import { RuntimeResourceControls } from './RuntimeResourceControls';
import { VersionField } from './VersionField';

const dialogErrorClassName =
  'rounded-[var(--radius)] border border-[color-mix(in_oklch,var(--destructive)_36%,transparent)] bg-[color-mix(in_oklch,var(--destructive)_8%,transparent)] px-[11px] py-[9px] text-[13px] leading-[1.35] text-destructive [overflow-wrap:anywhere]';

type EnvironmentDialog = AppState['environmentDialog'];

export function EnvironmentDialogView({ controller, state }: { controller: ERunUIController; state: AppState }): React.ReactElement {
  const dialog = state.environmentDialog;
  const tenantRef = React.useRef<HTMLInputElement>(null);
  const environmentRef = React.useRef<HTMLInputElement>(null);

  React.useEffect(() => {
    if (!dialog.open) {
      return undefined;
    }
    const timeout = window.setTimeout(() => {
      const target = dialog.tenant ? environmentRef.current : tenantRef.current;
      target?.focus();
      target?.select();
    }, 0);
    return () => window.clearTimeout(timeout);
  }, [dialog.open]);

  return (
    <Dialog open={dialog.open} onOpenChange={(open) => !open && controller.closeEnvironmentDialog()}>
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
            void controller.submitEnvironmentDialog(event.currentTarget).catch((error: unknown) => {
              controller.showTerminalMessage(readError(error));
            });
          }}
        >
          <EnvironmentDialogHeader dialog={dialog} />
          <EnvironmentDialogFields controller={controller} state={state} tenantRef={tenantRef} environmentRef={environmentRef} />
          <DialogError error={dialog.error} />
          <EnvironmentDialogFooter controller={controller} dialog={dialog} />
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
      <DialogDescription>
        {dialog.tenant && dialog.environment ? `${dialog.tenant} / ${dialog.environment}` : 'Enter the tenant and environment name.'}
      </DialogDescription>
    </DialogHeader>
  );
}

function EnvironmentDialogFields({
  controller,
  state,
  tenantRef,
  environmentRef,
}: {
  controller: ERunUIController;
  state: AppState;
  tenantRef: React.Ref<HTMLInputElement>;
  environmentRef: React.Ref<HTMLInputElement>;
}): React.ReactElement {
  const dialog = state.environmentDialog;
  const isDeploy = dialog.actionMode === 'deploy';
  return (
    <>
      <EnvironmentNameFields controller={controller} state={state} tenantRef={tenantRef} environmentRef={environmentRef} />
      <VersionField
        id="environment-version"
        value={dialog.version}
        sourceText={selectedVersionSourceText(findVersionSuggestion(state.versionSuggestions, dialog.version, dialog.versionImage))}
        suggestions={state.versionSuggestions}
        choicesOpen={dialog.choicesOpen}
        required={isDeploy}
        disabled={dialog.busy}
        onValueChange={(version) => controller.updateEnvironmentDialog({ version })}
        onChoicesOpenChange={(open) => controller.setEnvironmentVersionChoicesOpen(open)}
        onSelect={(suggestion) => controller.selectEnvironmentVersionSuggestion(suggestion)}
      />
      {!isDeploy && <EnvironmentCreateFields controller={controller} dialog={dialog} />}
    </>
  );
}

function EnvironmentNameFields({
  controller,
  state,
  tenantRef,
  environmentRef,
}: {
  controller: ERunUIController;
  state: AppState;
  tenantRef: React.Ref<HTMLInputElement>;
  environmentRef: React.Ref<HTMLInputElement>;
}): React.ReactElement {
  const dialog = state.environmentDialog;
  const isDeploy = dialog.actionMode === 'deploy';
  const tenantSuggestions = React.useMemo(
    () => uniqueSuggestions([dialog.tenant, ...state.tenants.map((tenant) => tenant.name), ...loadSavedPastTenants()]),
    [dialog.tenant, state.tenants],
  );
  const environmentSuggestions = React.useMemo(() => environmentNameSuggestions(state, dialog), [dialog, state]);

  return (
    <>
      <EditableComboField id="environment-tenant" inputRef={tenantRef} label="Tenant" value={dialog.tenant} suggestions={tenantSuggestions} required disabled={dialog.busy || isDeploy} onValueChange={(tenant) => controller.updateEnvironmentDialog({ tenant })} />
      <EditableComboField id="environment-name" inputRef={environmentRef} label="Environment" value={dialog.environment} suggestions={environmentSuggestions} required disabled={dialog.busy || isDeploy} onValueChange={(environment) => controller.updateEnvironmentDialog({ environment })} />
    </>
  );
}

function EnvironmentCreateFields({ controller, dialog }: { controller: ERunUIController; dialog: EnvironmentDialog }): React.ReactElement {
  const containerRegistrySuggestions = React.useMemo(
    () => uniqueSuggestions([dialog.containerRegistry, ...loadSavedPastContainerRegistries(), 'ghcr.io/rihards-freimanis']),
    [dialog.containerRegistry],
  );

  return (
    <>
      <KubernetesContextSelect controller={controller} dialog={dialog} />
      <RuntimePodFields controller={controller} dialog={dialog} />
      <EditableComboField id="environment-container-registry" label="Container registry" value={dialog.containerRegistry} suggestions={containerRegistrySuggestions} required disabled={dialog.busy} onValueChange={(containerRegistry) => controller.updateEnvironmentDialog({ containerRegistry })} />
      <EnvironmentCreateChecks controller={controller} dialog={dialog} />
    </>
  );
}

function KubernetesContextSelect({ controller, dialog }: { controller: ERunUIController; dialog: EnvironmentDialog }): React.ReactElement {
  return (
    <div className="grid gap-2">
      <Label htmlFor="environment-kubernetes-context">Kubernetes context</Label>
      <select
        id="environment-kubernetes-context"
        className="border-input bg-background ring-offset-background placeholder:text-muted-foreground focus-visible:ring-ring flex h-10 w-full rounded-[var(--radius)] border px-3 py-2 text-sm file:border-0 file:bg-transparent file:text-sm file:font-medium focus-visible:ring-2 focus-visible:ring-offset-2 focus-visible:outline-none disabled:cursor-not-allowed disabled:opacity-50"
        value={dialog.kubernetesContext}
        required
        disabled={dialog.busy || dialog.kubernetesContextsLoading || dialog.kubernetesContexts.length === 0}
        onChange={(event) => controller.updateEnvironmentDialog({ kubernetesContext: event.target.value })}
      >
        {kubernetesContextOptions(dialog)}
      </select>
    </div>
  );
}

function RuntimePodFields({ controller, dialog }: { controller: ERunUIController; dialog: EnvironmentDialog }): React.ReactElement {
  return (
    <RuntimeResourceControls
      idPrefix="environment-runtime"
      value={dialog.runtimePod}
      status={dialog.resourceStatus}
      loading={dialog.resourceStatusLoading}
      disabled={dialog.busy}
      onChange={(runtimePod) => controller.updateEnvironmentDialog({ runtimePod })}
    />
  );
}

function EnvironmentCreateChecks({ controller, dialog }: { controller: ERunUIController; dialog: EnvironmentDialog }): React.ReactElement {
  return (
    <div className="grid gap-2">
      <CheckboxField id="environment-default-tenant" label="Set as default tenant" checked={dialog.setDefaultTenant} disabled={dialog.busy} onCheckedChange={(setDefaultTenant) => controller.updateEnvironmentDialog({ setDefaultTenant })} />
      <CheckboxField id="environment-no-git" label="Initialize without Git checkout" checked={dialog.noGit} disabled={dialog.busy} onCheckedChange={(noGit) => controller.updateEnvironmentDialog({ noGit })} />
      <CheckboxField id="environment-bootstrap" label="Create tenant devops module" checked={dialog.bootstrap} disabled={dialog.busy} onCheckedChange={(bootstrap) => controller.updateEnvironmentDialog({ bootstrap })} />
    </div>
  );
}

function CheckboxField({ id, label, checked, disabled, onCheckedChange }: { id: string; label: string; checked: boolean; disabled: boolean; onCheckedChange: (checked: boolean) => void }): React.ReactElement {
  return (
    <div className="flex items-center gap-2">
      <Checkbox id={id} checked={checked} disabled={disabled} onCheckedChange={(value) => onCheckedChange(value === true)} />
      <Label htmlFor={id} className="text-sm font-normal">{label}</Label>
    </div>
  );
}

function DialogError({ error }: { error: string }): React.ReactElement | null {
  return error ? <div className={dialogErrorClassName} role="alert">{error}</div> : null;
}

function EnvironmentDialogFooter({ controller, dialog }: { controller: ERunUIController; dialog: EnvironmentDialog }): React.ReactElement {
  const isDeploy = dialog.actionMode === 'deploy';
  const resourceBlocked = dialog.resourceStatusLoading || !dialog.resourceStatus?.available || Boolean(runtimeResourceLimitMessage(dialog.runtimePod, dialog.resourceStatus));
  const disabled = dialog.busy || (!isDeploy && (dialog.kubernetesContextsLoading || dialog.kubernetesContexts.length === 0 || resourceBlocked));
  return (
    <DialogFooter>
      <Button type="button" variant="outline" size="sm" disabled={dialog.busy} onClick={() => controller.closeEnvironmentDialog()}>Cancel</Button>
      <Button type="submit" size="sm" disabled={disabled}>
        <EnvironmentSubmitIcon dialog={dialog} />
        {dialog.busy ? (isDeploy ? 'Deploying...' : 'Creating...') : isDeploy ? 'Deploy' : 'Create'}
      </Button>
    </DialogFooter>
  );
}

function EnvironmentSubmitIcon({ dialog }: { dialog: EnvironmentDialog }): React.ReactElement {
  if (dialog.busy) {
    return <LoaderCircle className="animate-spin" aria-hidden="true" />;
  }
  return dialog.actionMode === 'deploy' ? <Rocket aria-hidden="true" /> : <FolderPlus aria-hidden="true" />;
}

function environmentNameSuggestions(state: AppState, dialog: EnvironmentDialog): string[] {
  const selectedTenant = state.tenants.find((tenant) => tenant.name.toLowerCase() === dialog.tenant.trim().toLowerCase());
  const selectedTenantEnvironments = selectedTenant?.environments.map((environment) => environment.name) || [];
  const allEnvironments = state.tenants.flatMap((tenant) => tenant.environments.map((environment) => environment.name));
  return uniqueSuggestions([dialog.environment, ...selectedTenantEnvironments, ...loadSavedPastEnvironments(), ...allEnvironments]);
}

function kubernetesContextOptions(dialog: EnvironmentDialog): React.ReactNode {
  if (dialog.kubernetesContextsLoading) {
    return <option value="">Loading contexts...</option>;
  }
  if (dialog.kubernetesContexts.length === 0) {
    return <option value="">No Kubernetes contexts</option>;
  }
  return dialog.kubernetesContexts.map((context) => <option key={context} value={context}>{context}</option>);
}
