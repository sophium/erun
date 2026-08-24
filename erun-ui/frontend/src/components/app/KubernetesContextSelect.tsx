import { Button, EmptyState, Label, SelectField } from 'erun-kit';
import * as React from 'react';

import { refreshKubernetesContexts } from '@/app/dialogContextsThunks';
import { updateEnvironmentDialog } from '@/app/environmentDialogThunks';
import { useAppDispatch } from '@/app/hooks';
import { isMacPlatform, isWindowsPlatform } from '@/app/platform';
import type { AppState } from '@/app/state';

type EnvironmentDialog = AppState['environmentDialog'];

// noContextsShellSetupHint names the shell-config file and the kubectl
// install command for the host's own platform. The Windows branch matters
// because this dialog IS the documented Windows getting-started path
// (erun-docs/docs/getting-started/first-environment.md): a Windows reader who
// gets macOS-only shell/package-manager advice here has nowhere else to look.
function noContextsShellSetupHint(): string {
  if (isWindowsPlatform) {
    return 'set KUBECONFIG under your account environment variables (Start menu → "Edit environment variables for your account"), then restart ERun. If kubectl is not yet installed, install it with `winget install -e --id Kubernetes.kubectl`.';
  }
  if (isMacPlatform) {
    return 'set KUBECONFIG in ~/.zshenv (or ~/.bash_profile) so it applies to GUI launches too, then restart ERun. If kubectl is not yet installed, install it with `brew install kubectl`.';
  }
  return "set KUBECONFIG in ~/.profile (or ~/.bashrc) so it applies to GUI launches too, then restart ERun. If kubectl is not yet installed, install it with your distribution's package manager (e.g. `apt install kubectl`).";
}

// noContextsBody also names the in-app route that skips local kubectl
// entirely — a managed cloud cluster ERun provisions — since that route
// exists today but was never surfaced from this dialog.
function noContextsBody(): string {
  return (
    'ERun runs `kubectl config get-contexts` using the PATH and KUBECONFIG it inherits from your login shell at startup. ' +
    `If your terminal sees contexts that don't appear here, ${noContextsShellSetupHint()}\n\n` +
    'Or skip local kubectl entirely: in ERun settings, add a cloud alias under Cloud aliases (Add AWS account), then provision a cluster under Cloud contexts (Init).'
  );
}

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
    const body = noContextsBody();
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
