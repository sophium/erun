import { AlertTriangle, LoaderCircle, Rocket, Save, Trash2 } from 'lucide-react';
import * as React from 'react';

import { readError } from '@/app/errors';
import { useAppDispatch, useAppSelector } from '@/app/hooks';
import {
  closeManageDialog,
  manageDialogTabHasUnsavedChanges,
  setManageTab,
  submitManageConfig,
  submitManageDelete,
  submitManageDeploy,
} from '@/app/manageEnvironmentThunks';
import { showTerminalMessage } from '@/app/notificationThunks';
import { runtimeResourceLimitMessage } from '@/app/runtimeResources';
import type { AppState } from '@/app/state';
import { useController } from '@/app/useController';
import { deleteConfirmationValue, normalizeDialogValue } from '@/app/versionSuggestions';
import { dialogErrorClassName } from '@/components/app/ManageDialog.helpers';
import { ClaudeSettingsSection } from '@/components/app/ManageDialogAITab';
import { DeleteConfirmationFields } from '@/components/app/ManageDialogDeleteTab';
import { GeneralTab } from '@/components/app/ManageDialogGeneralTab';
import { HistoryTab } from '@/components/app/ManageDialogHistoryTab';
import { PortsTab } from '@/components/app/ManageDialogPortsTab';
import { RuntimeTab } from '@/components/app/ManageDialogRuntimeTab';
import { DiagnosticsSection, SSHAccessSection } from '@/components/app/ManageDialogSSHTab';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import type { ManageEditTab, ManageTab } from '@/types';

type ManageDialog = AppState['manageDialog'];

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

function ManageDialogContent({
  confirmationRef,
  expected,
  confirmingDelete,
}: {
  confirmationRef: React.Ref<HTMLInputElement>;
  expected: string;
  confirmingDelete: boolean;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const dialog = useAppSelector((state) => state.manageDialog);
  if (dialog.configLoading) {
    return (
      <div className="flex flex-1 min-h-0 flex-col">
        <div className="rounded-[var(--radius)] border border-dashed border-border px-3 py-2.5 text-[13px] leading-[1.35] text-muted-foreground">
          Loading config...
        </div>
      </div>
    );
  }
  if (confirmingDelete) {
    return (
      <div className="flex flex-1 min-h-0 flex-col gap-3 overflow-auto pb-1">
        <DeleteConfirmationFields
          dialog={dialog}
          confirmationRef={confirmationRef}
          expected={expected}
        />
      </div>
    );
  }
  const editTab: ManageEditTab = dialog.tab === 'delete' ? 'general' : dialog.tab;
  return (
    <div className="flex flex-1 min-h-0 flex-col gap-3">
      {dialog.pendingRedeploy && <RedeployBanner dialog={dialog} />}
      <Tabs
        value={editTab}
        onValueChange={(value) => {
          dispatch(setManageTab(value as ManageTab));
        }}
        className="flex-1 min-h-0"
      >
        <TabsList className="w-full">
          <DirtyAwareTabsTrigger value="general" label="General" dialog={dialog} />
          <DirtyAwareTabsTrigger value="runtime" label="Runtime" dialog={dialog} />
          <DirtyAwareTabsTrigger value="ai" label="AI" dialog={dialog} />
          <DirtyAwareTabsTrigger value="ports" label="Ports" dialog={dialog} />
          <DirtyAwareTabsTrigger value="ssh" label="SSH" dialog={dialog} />
          <DirtyAwareTabsTrigger value="history" label="History" dialog={dialog} />
        </TabsList>
        <div className="-mx-1 min-h-0 flex-1 overflow-auto px-1 pb-1">
          <TabsContent value="general" className="grid gap-3">
            <GeneralTab />
          </TabsContent>
          <TabsContent value="runtime" className="grid gap-3">
            <RuntimeTab />
          </TabsContent>
          <TabsContent value="ai" className="grid gap-3">
            <ClaudeSettingsSection dialog={dialog} />
          </TabsContent>
          <TabsContent value="ports" className="grid gap-3">
            <PortsTab dialog={dialog} />
          </TabsContent>
          <TabsContent value="ssh" className="grid gap-3">
            <SSHAccessSection dialog={dialog} />
            <DiagnosticsSection dialog={dialog} />
          </TabsContent>
          <TabsContent value="history" className="grid gap-3">
            <HistoryTab selection={dialog.selection} open={dialog.open && editTab === 'history'} />
          </TabsContent>
        </div>
      </Tabs>
    </div>
  );
}

function DirtyAwareTabsTrigger({
  value,
  label,
  dialog,
}: {
  value: ManageEditTab;
  label: string;
  dialog: ManageDialog;
}): React.ReactElement {
  const dirty = manageDialogTabHasUnsavedChanges(value, dialog.config, dialog.initialConfig);
  return (
    <TabsTrigger value={value} aria-label={dirty ? `${label}, has unsaved changes` : label}>
      <span className="inline-flex items-center gap-1">
        {label}
        {dirty && <span aria-hidden="true" className="size-1.5 rounded-full bg-primary" />}
      </span>
    </TabsTrigger>
  );
}

function RedeployBanner({ dialog }: { dialog: ManageDialog }): React.ReactElement {
  const dispatch = useAppDispatch();
  const deploying = dialog.busyAction === 'save' || dialog.busy;
  return (
    <div
      role="alert"
      aria-live="polite"
      className="grid grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-3 rounded-[var(--radius)] border-l-[3px] border-l-amber-500 border border-amber-500/40 bg-amber-500/10 px-3 py-2.5 text-[13px] leading-[1.35]"
    >
      <AlertTriangle
        className="size-[18px] text-amber-600 dark:text-amber-400"
        aria-hidden="true"
      />
      <div className="min-w-0">
        <div className="font-semibold text-foreground">Pending redeploy</div>
        <div className="text-muted-foreground">
          Saved values are not yet applied to the running pod.
        </div>
      </div>
      <div className="flex items-center gap-2">
        <Button
          type="button"
          variant="ghost"
          size="sm"
          disabled={deploying}
          onClick={() => {
            dispatch(closeManageDialog());
          }}
        >
          Later
        </Button>
        <Button
          type="button"
          size="sm"
          disabled={deploying}
          onClick={() =>
            void dispatch(submitManageDeploy()).catch((error: unknown) => {
              dispatch(showTerminalMessage(readError(error)));
            })
          }
        >
          <Rocket aria-hidden="true" />
          Redeploy now
        </Button>
      </div>
    </div>
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
  dialog: ManageDialog;
  confirmingDelete: boolean;
  deleteEnabled: boolean;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const resourceError = runtimeResourceLimitMessage(
    dialog.config.runtimePod,
    dialog.resourceStatus,
  );
  const saving = dialog.busyAction === 'save';
  const deleting = dialog.busyAction === 'delete';
  return (
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
      <div className="flex items-center gap-2">
        {confirmingDelete ? (
          <>
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
          </>
        ) : (
          <>
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
              disabled={dialog.busy || dialog.configLoading || Boolean(resourceError)}
              onClick={() =>
                void dispatch(submitManageConfig()).catch((error: unknown) => {
                  dispatch(showTerminalMessage(readError(error)));
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
          </>
        )}
      </div>
    </DialogFooter>
  );
}
