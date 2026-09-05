import {
  Button,
  cn,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from 'erun-kit';
import { AlertTriangle, LoaderCircle, Save, Trash2 } from 'lucide-react';
import * as React from 'react';

import { readError } from '@/app/errors';
import { useAppDispatch, useAppSelector } from '@/app/hooks';
import {
  closeManageDialog,
  setManageTab,
  submitManageConfig,
  submitManageDelete,
} from '@/app/manageEnvironmentThunks';
import { showTerminalError } from '@/app/notificationThunks';
import { runtimeResourceValidation } from '@/app/runtimeResources';
import { useController } from '@/app/useController';
import { deleteConfirmationValue, normalizeDialogValue } from '@/app/versionSuggestions';
import { dialogErrorClassName } from '@/components/app/ManageDialog.helpers';
import { ManageDialogContent, type ManageDialogState } from '@/components/app/ManageDialogTabs';

export function ManageDialogView(): React.ReactElement {
  const controller = useController();
  const dispatch = useAppDispatch();
  const dialog = useAppSelector((state) => state.manageDialog);
  const confirmationRef = React.useRef<HTMLInputElement>(null);
  const selection = dialog.selection;
  const confirmingDelete = dialog.tab === 'delete';
  const expected = selection ? deleteConfirmationValue(selection) : '';
  const deleteEnabled = !dialog.busy && normalizeDialogValue(dialog.confirmation) === expected;

  React.useEffect(() => {
    if (!dialog.open || !confirmingDelete) {
      return;
    }
    window.setTimeout(() => {
      confirmationRef.current?.focus();
    }, 0);
  }, [dialog.open, confirmingDelete]);

  return (
    <Dialog
      open={dialog.open}
      onOpenChange={(open) => {
        if (!open) dispatch(closeManageDialog());
      }}
    >
      <DialogContent
        className="h-[min(85vh,800px)] sm:max-w-2xl"
        onCloseAutoFocus={(event) => {
          event.preventDefault();
          controller.focusTerminalSoon();
        }}
      >
        <form
          className="flex h-[calc(min(85vh,800px)-3rem)] min-h-0 flex-col gap-4"
          onSubmit={(event) => {
            event.preventDefault();
            if (confirmingDelete && deleteEnabled) {
              void dispatch(submitManageDelete());
            }
          }}
        >
          <DialogHeader>
            <DialogTitle>
              {selection ? `${selection.tenant}-${selection.environment}` : 'Environment'}
            </DialogTitle>
            <DialogDescription>
              Edit environment configuration, deploy a different runtime version, run diagnostics,
              or delete the environment.
            </DialogDescription>
          </DialogHeader>
          <ManageDialogContent
            confirmationRef={confirmationRef}
            expected={expected}
            confirmingDelete={confirmingDelete}
          />
          <DialogError error={dialog.error} />
          <ManageDialogFooter
            dialog={dialog}
            confirmingDelete={confirmingDelete}
            deleteEnabled={deleteEnabled}
          />
        </form>
      </DialogContent>
    </Dialog>
  );
}

function DialogError({ error }: { error: string }): React.ReactElement | null {
  return error ? (
    <div className={dialogErrorClassName} role="alert">
      {error}
    </div>
  ) : null;
}

function ManageDialogFooter({
  dialog,
  confirmingDelete,
  deleteEnabled,
}: {
  dialog: ManageDialogState;
  confirmingDelete: boolean;
  deleteEnabled: boolean;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const { blockingError, capacityWarning } = runtimeResourceValidation(
    dialog.config.runtimePod,
    dialog.resourceStatus,
  );
  const saveStatusId = 'manage-save-status';
  const hasSaveStatus = !confirmingDelete && Boolean(blockingError || capacityWarning);
  return (
    <div className="grid gap-2">
      {hasSaveStatus && (
        <ManageSaveBlocker
          id={saveStatusId}
          blockingError={blockingError}
          capacityWarning={capacityWarning}
          showGoToRuntime={dialog.tab !== 'runtime'}
          onGoToRuntime={() => {
            dispatch(setManageTab('runtime'));
          }}
        />
      )}
      <DialogFooter className="sm:justify-between">
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={dialog.busy}
          onClick={() => {
            dispatch(closeManageDialog());
          }}
        >
          Cancel
        </Button>
        <ManageDialogFooterActions
          dialog={dialog}
          confirmingDelete={confirmingDelete}
          deleteEnabled={deleteEnabled}
          blockingError={blockingError}
          hasSaveStatus={hasSaveStatus}
          saveStatusId={saveStatusId}
        />
      </DialogFooter>
    </div>
  );
}

function ManageDialogFooterActions({
  dialog,
  confirmingDelete,
  deleteEnabled,
  blockingError,
  hasSaveStatus,
  saveStatusId,
}: {
  dialog: ManageDialogState;
  confirmingDelete: boolean;
  deleteEnabled: boolean;
  blockingError: string;
  hasSaveStatus: boolean;
  saveStatusId: string;
}): React.ReactElement {
  if (confirmingDelete) {
    return <ConfirmDeleteActions dialog={dialog} deleteEnabled={deleteEnabled} />;
  }
  return (
    <EditActions
      dialog={dialog}
      blockingError={blockingError}
      hasSaveStatus={hasSaveStatus}
      saveStatusId={saveStatusId}
    />
  );
}

function ConfirmDeleteActions({
  dialog,
  deleteEnabled,
}: {
  dialog: ManageDialogState;
  deleteEnabled: boolean;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const deleting = dialog.busyAction === 'delete';
  return (
    <div className="flex items-center gap-2">
      <Button
        type="button"
        variant="ghost"
        size="sm"
        disabled={dialog.busy}
        onClick={() => {
          dispatch(setManageTab('general'));
        }}
      >
        Back to edit
      </Button>
      <Button
        type="button"
        variant="destructive"
        size="sm"
        disabled={dialog.busy || !deleteEnabled}
        onClick={() => void dispatch(submitManageDelete())}
      >
        {deleting ? (
          <LoaderCircle className="animate-spin" aria-hidden="true" />
        ) : (
          <Trash2 aria-hidden="true" />
        )}
        {deleting ? 'Deleting...' : 'Confirm delete'}
      </Button>
    </div>
  );
}

function EditActions({
  dialog,
  blockingError,
  hasSaveStatus,
  saveStatusId,
}: {
  dialog: ManageDialogState;
  blockingError: string;
  hasSaveStatus: boolean;
  saveStatusId: string;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const saving = dialog.busyAction === 'save';
  return (
    <div className="flex items-center gap-2">
      <Button
        type="button"
        variant="outline"
        size="sm"
        disabled={dialog.busy || dialog.configLoading}
        onClick={() => {
          dispatch(setManageTab('delete'));
        }}
      >
        <Trash2 aria-hidden="true" />
        Delete
      </Button>
      <Button
        type="button"
        size="sm"
        disabled={dialog.busy || dialog.configLoading || Boolean(blockingError)}
        aria-describedby={hasSaveStatus ? saveStatusId : undefined}
        onClick={() =>
          void dispatch(submitManageConfig()).catch((error: unknown) => {
            dispatch(showTerminalError(readError(error)));
          })
        }
      >
        {saving ? (
          <LoaderCircle className="animate-spin" aria-hidden="true" />
        ) : (
          <Save aria-hidden="true" />
        )}
        {saving ? 'Saving...' : 'Save'}
      </Button>
    </div>
  );
}

// Surfaces why Save is disabled (or a deploy is at risk) right where the user
// acts. A capacity warning still leaves Save enabled because saving only
// persists config; it never deploys.
function ManageSaveBlocker({
  id,
  blockingError,
  capacityWarning,
  showGoToRuntime,
  onGoToRuntime,
}: {
  id: string;
  blockingError: string;
  capacityWarning: string;
  showGoToRuntime: boolean;
  onGoToRuntime: () => void;
}): React.ReactElement {
  const isError = Boolean(blockingError);
  const message = blockingError || capacityWarning;
  return (
    <div
      id={id}
      role={isError ? 'alert' : 'status'}
      aria-live="polite"
      className={cn(
        'flex items-center gap-2 rounded-[var(--radius)] border border-l-[3px] px-3 py-2 text-[13px] leading-[1.35]',
        isError
          ? 'border-destructive/40 border-l-destructive bg-destructive/10'
          : 'border-amber-500/40 border-l-amber-500 bg-amber-500/10',
      )}
    >
      <AlertTriangle
        className={cn(
          'size-[18px] shrink-0',
          isError ? 'text-destructive' : 'text-amber-700 dark:text-amber-400',
        )}
        aria-hidden="true"
      />
      <span className="min-w-0 flex-1">
        <span className="font-semibold">{isError ? "Can't save" : 'Heads up'}</span>
        {` — ${message}`}
      </span>
      {showGoToRuntime && (
        <Button
          type="button"
          variant="ghost"
          size="sm"
          className="shrink-0"
          onClick={onGoToRuntime}
        >
          Go to Runtime
        </Button>
      )}
    </div>
  );
}
