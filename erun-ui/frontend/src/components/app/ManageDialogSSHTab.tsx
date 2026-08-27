import { Button, cn, Input, Label, StatusBadge } from 'erun-kit';
import { FolderOpen, Server, Stethoscope } from 'lucide-react';
import * as React from 'react';

import { environmentTypeIsRemoteWorktree } from '@/app/environmentType';
import { readError } from '@/app/errors';
import { useAppDispatch, useAppSelector } from '@/app/hooks';
import {
  chooseWorkspaceSyncLocalFolder,
  enableManageSSHD,
  startManageDoctor,
  updateManageSSHDConfig,
} from '@/app/manageEnvironmentThunks';
import { showTerminalError } from '@/app/notificationThunks';
import type { AppState } from '@/app/state';
import { selectionKey } from '@/app/versionSuggestions';
import { CheckboxField, ReadonlyField } from '@/components/app/ManageDialog.fields';
import {
  relativeTimeFromNow,
  workspaceSyncStatusTone,
} from '@/components/app/ManageDialog.helpers';

type ManageDialog = AppState['manageDialog'];

function useLastSSHDInitOutcome(
  selection: ManageDialog['selection'],
): AppState['lastSSHDInitBySelection'][string] | undefined {
  const lastSSHDInitBySelection = useAppSelector((state) => state.sshdInit.lastSSHDInitBySelection);
  return selection ? lastSSHDInitBySelection[selectionKey(selection)] : undefined;
}

export function SSHAccessSection({ dialog }: { dialog: ManageDialog }): React.ReactElement {
  const config = dialog.config;
  const lastSSHDInit = useLastSSHDInitOutcome(dialog.selection);
  return (
    <div className="grid gap-3 rounded-[var(--radius)] border border-border p-3">
      <SSHAccessHeader dialog={dialog} />
      {lastSSHDInit && <SSHDInitLastRun outcome={lastSSHDInit} />}
      <ReadonlyField
        id="environment-config-sshd-enabled"
        label="SSHD"
        value={config.sshd.enabled ? 'Enabled' : 'Disabled'}
      />
      <ReadonlyField
        id="environment-config-sshd-localport"
        label="Local port"
        value={config.sshd.localPort > 0 ? String(config.sshd.localPort) : ''}
      />
      <ReadonlyField
        id="environment-config-sshd-publickeypath"
        label="Public key"
        value={config.sshd.publicKeyPath}
      />
    </div>
  );
}

// Workspace sync rides over the SSH connection, but it is a distinct user
// concept (mirroring a local Git folder into the pod's worktree) from SSH
// shell access itself, so it gets its own titled section rather than sitting
// as an unlabeled checkbox inside "SSH access" (recognition over recall).
export function WorkspaceSyncSection({ dialog }: { dialog: ManageDialog }): React.ReactElement {
  const dispatch = useAppDispatch();
  const config = dialog.config;
  const syncPathRequired =
    config.sshd.workspaceSyncEnabled && !(config.sshd.workspaceSyncLocalPath ?? '').trim();
  return (
    <div className="grid gap-3 rounded-[var(--radius)] border border-border p-3">
      <div className="text-xs leading-[1.2] font-semibold tracking-normal text-muted-foreground uppercase">
        Workspace sync
      </div>
      <CheckboxField
        id="environment-config-sshd-sync-enabled"
        label="Enable workspace sync"
        checked={config.sshd.workspaceSyncEnabled}
        disabled={dialog.busy || dialog.configLoading || !config.sshd.enabled}
        onChange={(workspaceSyncEnabled) => {
          dispatch(updateManageSSHDConfig({ workspaceSyncEnabled }));
        }}
      />
      {!config.sshd.enabled && (
        <div className="text-[13px] leading-[1.35] text-muted-foreground">
          Requires SSH access to be enabled.
        </div>
      )}
      {config.sshd.workspaceSyncEnabled && (
        <>
          <WorkspaceSyncStatus sshd={config.sshd} />
          <LocalSyncFolderField
            dialog={dialog}
            error={syncPathRequired ? 'Choose a local Git folder before saving.' : ''}
          />
        </>
      )}
    </div>
  );
}

function SSHAccessHeader({ dialog }: { dialog: ManageDialog }): React.ReactElement {
  const dispatch = useAppDispatch();
  const config = dialog.config;
  return (
    <div className="flex items-center justify-between gap-3">
      <div className="text-xs leading-[1.2] font-semibold tracking-normal text-muted-foreground uppercase">
        SSH access
      </div>
      {!config.sshd.enabled && (
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={
            dialog.busy || dialog.configLoading || !environmentTypeIsRemoteWorktree(config.type)
          }
          onClick={() =>
            void dispatch(enableManageSSHD()).catch((error: unknown) => {
              dispatch(showTerminalError(readError(error)));
            })
          }
        >
          <Server aria-hidden="true" />
          Enable SSHD
        </Button>
      )}
    </div>
  );
}

function LocalSyncFolderField({
  dialog,
  error,
}: {
  dialog: ManageDialog;
  error: string;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const disabled = dialog.busy || dialog.configLoading;
  const describedBy = error ? 'environment-config-sshd-sync-localpath-error' : undefined;
  return (
    <div className="grid gap-2">
      <Label htmlFor="environment-config-sshd-sync-localpath">Local sync folder</Label>
      <div className="flex gap-2">
        <Input
          id="environment-config-sshd-sync-localpath"
          className="min-w-0 flex-1"
          value={dialog.config.sshd.workspaceSyncLocalPath ?? ''}
          type="text"
          autoComplete="off"
          spellCheck={false}
          disabled={disabled}
          aria-invalid={Boolean(error)}
          aria-describedby={describedBy}
          onChange={(event) => {
            dispatch(updateManageSSHDConfig({ workspaceSyncLocalPath: event.target.value }));
          }}
        />
        <Button
          type="button"
          variant="outline"
          size="icon"
          aria-label="Select local sync folder"
          disabled={disabled}
          onClick={() =>
            void dispatch(chooseWorkspaceSyncLocalFolder()).catch((error: unknown) => {
              dispatch(showTerminalError(readError(error)));
            })
          }
        >
          <FolderOpen aria-hidden="true" />
        </Button>
      </div>
      {error && (
        <div
          id="environment-config-sshd-sync-localpath-error"
          className="text-[13px] leading-[1.35] text-destructive"
          role="alert"
        >
          {error}
        </div>
      )}
    </div>
  );
}

function WorkspaceSyncStatus({
  sshd,
}: {
  sshd: ManageDialog['config']['sshd'];
}): React.ReactElement | null {
  const status = (sshd.workspaceSyncStatus ?? '').trim();
  const message = (sshd.workspaceSyncStatusMessage ?? '').trim();
  if (!status) {
    return null;
  }
  return (
    <div
      className="grid grid-cols-[auto_minmax(0,1fr)] items-center gap-2 rounded-[var(--radius)] border border-border bg-muted/35 px-3 py-2 text-[13px] leading-[1.35]"
      role={status === 'error' ? 'alert' : 'status'}
    >
      <StatusBadge tone={workspaceSyncStatusTone(status)} label={status.replace(/_/g, ' ')} />
      <span
        className={cn(
          'min-w-0 [overflow-wrap:anywhere]',
          message ? 'text-muted-foreground' : 'text-foreground',
        )}
      >
        {message || status.replace(/_/g, ' ')}
      </span>
    </div>
  );
}

function SSHDInitLastRun({
  outcome,
}: {
  outcome: AppState['lastSSHDInitBySelection'][string];
}): React.ReactElement {
  const ago = relativeTimeFromNow(outcome.ranAt);
  const tone: 'success' | 'destructive' = outcome.success ? 'success' : 'destructive';
  const summary = outcome.success ? 'SSHD enabled' : outcome.message || 'enabling SSHD failed';
  return (
    <div
      role={outcome.success ? 'status' : 'alert'}
      className={cn(
        'grid grid-cols-[auto_minmax(0,1fr)] items-start gap-2 rounded-[var(--radius)] border px-3 py-2 text-[13px] leading-[1.4]',
        tone === 'success'
          ? 'border-green-600/35 bg-green-600/10 text-foreground'
          : 'border-destructive/40 bg-[color-mix(in_oklch,var(--destructive)_8%,transparent)] text-foreground',
      )}
    >
      <span
        className={cn(
          'mt-px size-1.5 rounded-full',
          tone === 'success' ? 'bg-green-600' : 'bg-destructive',
        )}
        aria-hidden="true"
      />
      <span className="min-w-0 [overflow-wrap:anywhere]">
        <span className="font-medium">Last run {ago}</span>
        <span className="text-muted-foreground"> — {summary}</span>
      </span>
    </div>
  );
}

export function DiagnosticsSection({ dialog }: { dialog: ManageDialog }): React.ReactElement {
  const dispatch = useAppDispatch();
  const lastDoctorBySelection = useAppSelector((state) => state.doctor.lastDoctorBySelection);
  const lastDoctor = dialog.selection
    ? lastDoctorBySelection[selectionKey(dialog.selection)]
    : undefined;
  return (
    <div className="grid gap-2 rounded-[var(--radius)] border border-border p-3">
      <div className="flex items-center justify-between gap-3">
        <div className="min-w-0">
          <div className="text-xs leading-[1.2] font-semibold tracking-normal text-muted-foreground uppercase">
            Diagnostics
          </div>
        </div>
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={dialog.busy || dialog.configLoading}
          onClick={() =>
            void dispatch(startManageDoctor()).catch((error: unknown) => {
              dispatch(showTerminalError(readError(error)));
            })
          }
        >
          <Stethoscope aria-hidden="true" />
          Run Doctor
        </Button>
      </div>
      {lastDoctor && <DoctorLastRun outcome={lastDoctor} />}
    </div>
  );
}

function DoctorLastRun({
  outcome,
}: {
  outcome: AppState['lastDoctorBySelection'][string];
}): React.ReactElement {
  const ago = relativeTimeFromNow(outcome.ranAt);
  const tone: 'success' | 'destructive' = outcome.success ? 'success' : 'destructive';
  const summary = outcome.success ? 'all checks passed' : outcome.message || 'doctor failed';
  return (
    <div
      role={outcome.success ? 'status' : 'alert'}
      className={cn(
        'grid grid-cols-[auto_minmax(0,1fr)] items-start gap-2 rounded-[var(--radius)] border px-3 py-2 text-[13px] leading-[1.4]',
        tone === 'success'
          ? 'border-green-600/35 bg-green-600/10 text-foreground'
          : 'border-destructive/40 bg-[color-mix(in_oklch,var(--destructive)_8%,transparent)] text-foreground',
      )}
    >
      <span
        className={cn(
          'mt-px size-1.5 rounded-full',
          tone === 'success' ? 'bg-green-600' : 'bg-destructive',
        )}
        aria-hidden="true"
      />
      <span className="min-w-0 [overflow-wrap:anywhere]">
        <span className="font-medium">Last run {ago}</span>
        <span className="text-muted-foreground"> — {summary}</span>
      </span>
    </div>
  );
}
