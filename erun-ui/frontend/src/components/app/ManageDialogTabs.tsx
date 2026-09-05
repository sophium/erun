import { Button, Tabs, TabsContent, TabsList, TabsTrigger } from 'erun-kit';
import { AlertTriangle, Rocket } from 'lucide-react';
import * as React from 'react';

import { readError } from '@/app/errors';
import { useAppDispatch, useAppSelector } from '@/app/hooks';
import {
  closeManageDialog,
  manageDialogTabHasUnsavedChanges,
  setManageTab,
  submitManageDeploy,
} from '@/app/manageEnvironmentThunks';
import { showTerminalError } from '@/app/notificationThunks';
import { runtimeResourceValidation } from '@/app/runtimeResources';
import type { AppState } from '@/app/state';
import { ClaudeSettingsSection } from '@/components/app/ManageDialogAITab';
import { DeleteConfirmationFields } from '@/components/app/ManageDialogDeleteTab';
import { GeneralTab } from '@/components/app/ManageDialogGeneralTab';
import { HistoryTab } from '@/components/app/ManageDialogHistoryTab';
import { JobsTab } from '@/components/app/ManageDialogJobsTab';
import { PortsTab } from '@/components/app/ManageDialogPortsTab';
import { RuntimeTab } from '@/components/app/ManageDialogRuntimeTab';
import {
  DiagnosticsSection,
  SSHAccessSection,
  WorkspaceSyncSection,
} from '@/components/app/ManageDialogSSHTab';
import type { ManageEditTab, ManageTab } from '@/types';

export type ManageDialogState = AppState['manageDialog'];

export function ManageDialogContent({
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
          <DirtyAwareTabsTrigger value="ssh" label="Access" dialog={dialog} />
          <DirtyAwareTabsTrigger value="jobs" label="Jobs" dialog={dialog} />
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
            <WorkspaceSyncSection dialog={dialog} />
            <DiagnosticsSection dialog={dialog} />
          </TabsContent>
          <TabsContent value="jobs" className="grid gap-3">
            <JobsTab selection={dialog.selection} open={dialog.open && editTab === 'jobs'} />
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
  dialog: ManageDialogState;
}): React.ReactElement {
  const dirty = manageDialogTabHasUnsavedChanges(value, dialog.config, dialog.initialConfig);
  // The Runtime tab is the only tab with field validation today; surface its
  // state on the trigger so a disabled Save points the user to the right tab
  // (NN #6 recognition over recall). Icon shape — not just color — distinguishes
  // error/warning from the unsaved-changes dot (WCAG 1.4.1 use of color).
  const { blockingError, capacityWarning } =
    value === 'runtime'
      ? runtimeResourceValidation(dialog.config.runtimePod, dialog.resourceStatus)
      : { blockingError: '', capacityWarning: '' };
  const ariaLabel = blockingError
    ? `${label}, has an error`
    : capacityWarning
      ? `${label}, has a warning`
      : dirty
        ? `${label}, has unsaved changes`
        : label;
  return (
    <TabsTrigger value={value} aria-label={ariaLabel}>
      <span className="inline-flex items-center gap-1">
        {label}
        {blockingError ? (
          <AlertTriangle aria-hidden="true" className="size-3 text-destructive" />
        ) : capacityWarning ? (
          <AlertTriangle aria-hidden="true" className="size-3 text-amber-700 dark:text-amber-400" />
        ) : dirty ? (
          <span aria-hidden="true" className="size-1.5 rounded-full bg-primary" />
        ) : null}
      </span>
    </TabsTrigger>
  );
}

function RedeployBanner({ dialog }: { dialog: ManageDialogState }): React.ReactElement {
  const dispatch = useAppDispatch();
  const deploying = dialog.busyAction === 'save' || dialog.busy;
  return (
    <div
      role="alert"
      aria-live="polite"
      className="grid grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-3 rounded-[var(--radius)] border-l-[3px] border-l-amber-500 border border-amber-500/40 bg-amber-500/10 px-3 py-2.5 text-[13px] leading-[1.35]"
    >
      <AlertTriangle
        className="size-[18px] text-amber-700 dark:text-amber-400"
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
              dispatch(showTerminalError(readError(error)));
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
