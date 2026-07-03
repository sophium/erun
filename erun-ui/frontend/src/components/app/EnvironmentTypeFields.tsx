import * as React from 'react';

import { updateEnvironmentDialog } from '@/app/environmentDialogThunks';
import { useAppDispatch } from '@/app/hooks';
import type { AppState } from '@/app/state';

import { LocalRepoPathInput } from './LocalRepoPathInput';
import { SelectField } from './SelectField';

type EnvironmentDialog = AppState['environmentDialog'];

// EnvironmentTypeSelect picks the env's fundamental shape: whether the worktree
// lives on the host, on a cluster PVC, or not at all (deploy-only runtime pod).
export function EnvironmentTypeSelect({
  dialog,
}: {
  dialog: EnvironmentDialog;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  return (
    <SelectField
      id="environment-type"
      label="Environment type"
      value={dialog.envType}
      options={[
        { value: 'remote-agent', label: 'Remote agent' },
        { value: 'local-agent', label: 'Local agent' },
        { value: 'runtime', label: 'Runtime' },
      ]}
      placeholder="Select environment type"
      emptyLabel=""
      helper={environmentTypeHelper(dialog.envType)}
      disabled={dialog.busy}
      required
      onChange={(value) => {
        dispatch(updateEnvironmentDialog({ envType: value as EnvironmentDialog['envType'] }));
      }}
    />
  );
}

function environmentTypeHelper(envType: EnvironmentDialog['envType']): string {
  switch (envType) {
    case 'local-agent':
      return 'Worktree on this machine, mounted into the agent pod. Builds happen in the cluster.';
    case 'remote-agent':
      return 'Worktree on a PVC inside the cluster. Builds happen in the cluster.';
    case 'runtime':
      return 'No agent worktree. Deploy-only — the pod just receives built images.';
    default:
      return '';
  }
}

// LocalRepoPathField's path only applies to local-agent envs; remote-agent uses
// a cluster PVC and runtime has no worktree.
export function LocalRepoPathField({ dialog }: { dialog: EnvironmentDialog }): React.ReactElement {
  const dispatch = useAppDispatch();
  return (
    <LocalRepoPathInput
      id="environment-local-repo-path"
      label="Local repo path"
      helper="Absolute path on this machine. Mounted into the agent pod as the worktree."
      value={dialog.localRepoPath}
      disabled={dialog.busy}
      onChange={(value) => {
        dispatch(updateEnvironmentDialog({ localRepoPath: value }));
      }}
    />
  );
}
