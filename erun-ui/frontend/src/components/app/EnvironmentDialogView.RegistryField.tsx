import { Checkbox, EditableComboField, Label, uniqueSuggestions } from 'erun-kit';
import * as React from 'react';

import { updateEnvironmentDialog } from '@/app/environmentDialogThunks';
import { useAppDispatch } from '@/app/hooks';
import type { AppState } from '@/app/state';
import { loadSavedPastContainerRegistries } from '@/app/storage';

type EnvironmentDialog = AppState['environmentDialog'];

// ContainerRegistryField offers the in-cluster erun-registry (resolved from the
// selected Kubernetes context) as the default when one is detected, and falls
// back to a free-text registry otherwise. There is no hardcoded default host —
// the deployed cluster registry is the default, not a placeholder like erunpaas.
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

  const hostedToggle = (
    <label
      htmlFor="environment-use-erun-registry"
      className="flex items-center gap-2 text-sm font-normal"
    >
      <Checkbox
        id="environment-use-erun-registry"
        checked={dialog.useErunRegistry}
        disabled={dialog.busy}
        onCheckedChange={(value) => {
          // The three choices are mutually exclusive, so selecting this one
          // releases the cluster toggle rather than leaving both set and
          // letting `erun init` reject the pair after the operator commits.
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
  );

  if (dialog.useErunRegistry) {
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

  // A fresh install has no cluster registry detected and no past values to
  // suggest, so the field would otherwise offer nothing to go on — no
  // placeholder, no helper, no suggestions (Nielsen #6, recognition over
  // recall). Name the format with an example and the route that detects a
  // registry automatically instead of typing one.
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
