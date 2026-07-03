import * as React from 'react';

import { refreshKubernetesContexts } from '@/app/dialogContextsThunks';
import { updateEnvironmentDialog } from '@/app/environmentDialogThunks';
import { useAppDispatch } from '@/app/hooks';
import type { AppState } from '@/app/state';
import { Button } from '@/components/ui/button';
import { Label } from '@/components/ui/label';

import { EmptyState } from './EmptyState';
import { SelectField } from './SelectField';

type EnvironmentDialog = AppState['environmentDialog'];

// KubernetesContextSelect is the Kubernetes-context picker for the env-init dialog.
export function KubernetesContextSelect({
  dialog,
}: {
  dialog: EnvironmentDialog;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const items = dialog.kubernetesContexts.map((context) => ({ value: context, label: context }));
  const placeholder = dialog.kubernetesContextsLoading
    ? 'Loading contexts...'
    : 'Select Kubernetes context';
  if (!dialog.kubernetesContextsLoading && dialog.kubernetesContexts.length === 0) {
    const body =
      "ERun runs `kubectl config get-contexts` using the PATH and KUBECONFIG it inherits from your login shell at startup. If your terminal sees contexts that don't appear here, set KUBECONFIG in ~/.zshenv (or ~/.bash_profile) so it applies to GUI launches too, then restart ERun. If kubectl is not yet installed, install it with `brew install kubectl`.";
    const errorDetail = dialog.error.trim();
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
