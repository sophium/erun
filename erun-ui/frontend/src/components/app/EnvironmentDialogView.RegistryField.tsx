import { Checkbox, EditableComboField, Label, uniqueSuggestions } from 'erun-kit';
import * as React from 'react';

import { updateEnvironmentDialog } from '@/app/environmentDialogThunks';
import { useAppDispatch } from '@/app/hooks';
import type { AppState } from '@/app/state';
import { loadSavedPastContainerRegistries } from '@/app/storage';

type EnvironmentDialog = AppState['environmentDialog'];

function isHostedRegistryUsable(dialog: EnvironmentDialog): boolean {
  return dialog.hostedRegistry?.available === true && dialog.useErunRegistry;
}

// Disabled until the reachability probe confirms the host; an unreachable
// registry would only fail at the first build.
function HostedRegistryToggle({ dialog }: { dialog: EnvironmentDialog }): React.ReactElement {
  const dispatch = useAppDispatch();
  const hosted = dialog.hostedRegistry;
  const hostedAvailable = hosted?.available === true;
  return (
    <div className="grid gap-1">
      <label
        htmlFor="environment-use-erun-registry"
        className="flex items-center gap-2 text-sm font-normal"
      >
        <Checkbox
          id="environment-use-erun-registry"
          checked={dialog.useErunRegistry}
          disabled={dialog.busy || !hostedAvailable}
          onCheckedChange={(value) => {
            // The choices are mutually exclusive, which `erun init` enforces.
            dispatch(
              updateEnvironmentDialog({
                useErunRegistry: value === true,
                ...(value === true ? { useClusterRegistry: false } : {}),
              }),
            );
          }}
        />
        Use erun&apos;s hosted registry
      </label>
      {hosted === null && (
        <p className="text-[12px] leading-[1.4] text-muted-foreground">
          Checking whether erun&apos;s hosted registry is reachable…
        </p>
      )}
      {hosted && !hosted.available && (
        <p className="text-[12px] leading-[1.4] text-muted-foreground">
          erun&apos;s hosted registry {hosted.host} {hosted.reason}. {hosted.recovery}
        </p>
      )}
    </div>
  );
}

// ContainerRegistryField defaults to the detected in-cluster registry; there is
// no hardcoded default host.
export function ContainerRegistryField({
  dialog,
}: {
  dialog: EnvironmentDialog;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const containerRegistrySuggestions = React.useMemo(
    () => uniqueSuggestions([dialog.containerRegistry, ...loadSavedPastContainerRegistries()]),
    [dialog.containerRegistry],
  );
  const cluster = dialog.clusterRegistry;
  const clusterAvailable = cluster?.deployed === true;
  const useCluster = clusterAvailable && dialog.useClusterRegistry;
  const useHosted = isHostedRegistryUsable(dialog);
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

  const hostedToggle = <HostedRegistryToggle dialog={dialog} />;

  if (useHosted) {
    return (
      <div className="grid gap-2">
        <Label htmlFor="environment-use-erun-registry">Container registry</Label>
        {hostedToggle}
        <p className="text-[12px] leading-[1.4] text-muted-foreground">
          Images push to and pull from erun&apos;s hosted registry under this tenant, authenticated
          by the tenant&apos;s own API token — no registry address or credentials to manage.
        </p>
      </div>
    );
  }

  if (useCluster) {
    return (
      <div className="grid gap-2">
        <Label htmlFor="environment-use-cluster-registry">Container registry</Label>
        {clusterToggle}
        {hostedToggle}
        <p className="text-[12px] leading-[1.4] text-muted-foreground">
          Resolved from {cluster.service}.{cluster.namespace}:{cluster.port} via this
          environment&apos;s Kubernetes context — pushed and pulled in-cluster, no host address
          needed.
        </p>
      </div>
    );
  }

  const showRegistryHelp = !clusterAvailable && containerRegistrySuggestions.length === 0;
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
      {hostedToggle}
      {showRegistryHelp && (
        <p className="text-[12px] leading-[1.4] text-muted-foreground">
          Where images push to and pull from, e.g. ghcr.io/your-org or docker.io/your-namespace. If
          the selected Kubernetes context has an in-cluster erun-registry, ERun detects it
          automatically and offers it above instead — provision one via Settings → Cloud aliases →
          Add AWS account → Cloud contexts → Init.
        </p>
      )}
    </div>
  );
}
