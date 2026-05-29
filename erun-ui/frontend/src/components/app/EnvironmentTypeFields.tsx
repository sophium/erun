import * as React from 'react';

import { useChooseLocalRepoPathMutation } from '@/app/api/environmentApi';
import { updateEnvironmentDialog } from '@/app/environmentDialogThunks';
import { readError } from '@/app/errors';
import { useAppDispatch } from '@/app/hooks';
import { showTerminalMessage } from '@/app/notificationThunks';
import type { AppState } from '@/app/state';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';

import { SelectField } from './SelectField';

type EnvironmentDialog = AppState['environmentDialog'];

// EnvironmentTypeSelect drives the env's `type` (local-agent, remote-agent,
// runtime) — a fundamental shape choice that controls whether the worktree
// lives on the host, on a PVC inside the cluster, or doesn't exist at all
// (deploy-only runtime pod). See erun-common/config.go:EnvironmentType.
// The trigger shows the short noun so it fits the dialog width; the
// per-type description renders as helper text below, preserving
// recognition-over-recall without truncating the trigger label.
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

// LocalRepoPathField captures the host path mounted into the agent pod for
// `local-agent` envs. The CLI passes this through to `--project-root`,
// which becomes EnvConfig.LocalRepoPath. Only meaningful for local-agent —
// remote-agent uses a PVC and runtime has no worktree. The Browse button
// opens a native directory picker (Wails OpenDirectoryDialog) so users do
// not have to type absolute paths by hand.
export function LocalRepoPathField({ dialog }: { dialog: EnvironmentDialog }): React.ReactElement {
  const dispatch = useAppDispatch();
  const [chooseLocalRepoPath, { isLoading: choosing }] = useChooseLocalRepoPathMutation();
  const helperId = 'environment-local-repo-path-helper';
  const onBrowse = async (): Promise<void> => {
    try {
      const picked = await chooseLocalRepoPath(dialog.localRepoPath).unwrap();
      if (picked.trim()) {
        dispatch(updateEnvironmentDialog({ localRepoPath: picked }));
      }
    } catch (error) {
      dispatch(showTerminalMessage(readError(error)));
    }
  };
  return (
    <div className="grid gap-2">
      <Label htmlFor="environment-local-repo-path">Local repo path</Label>
      <div className="flex items-stretch gap-2">
        <Input
          id="environment-local-repo-path"
          value={dialog.localRepoPath}
          type="text"
          autoComplete="off"
          spellCheck={false}
          placeholder="/Users/you/code/your-project"
          aria-describedby={helperId}
          disabled={dialog.busy}
          onChange={(event) => {
            dispatch(updateEnvironmentDialog({ localRepoPath: event.target.value }));
          }}
          className="flex-1"
        />
        <Button
          type="button"
          variant="outline"
          disabled={dialog.busy || choosing}
          onClick={() => {
            void onBrowse();
          }}
        >
          Browse…
        </Button>
      </div>
      <p id={helperId} className="text-[12px] leading-[1.4] text-muted-foreground">
        Absolute path on this machine. Mounted into the agent pod as the worktree.
      </p>
    </div>
  );
}
