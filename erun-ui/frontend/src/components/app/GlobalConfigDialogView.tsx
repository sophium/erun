import {
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  SelectField,
  type SelectFieldOption,
} from 'erun-kit';
import { LoaderCircle, Save } from 'lucide-react';
import * as React from 'react';

import { readError } from '@/app/errors';
import {
  closeGlobalConfigDialog,
  submitGlobalConfig,
  updateGlobalConfig,
} from '@/app/globalConfigThunks';
import { useAppDispatch, useAppSelector } from '@/app/hooks';
import { showTerminalError } from '@/app/notificationThunks';
import type { AppState } from '@/app/state';
import { useController } from '@/app/useController';
import { CloudAliasesSection } from '@/components/app/GlobalConfigDialog.CloudAliases';
import { CloudContextsSection } from '@/components/app/GlobalConfigDialog.CloudContexts';
import { NOT_CONFIGURED_VALUE, optionValues } from '@/components/app/GlobalConfigDialog.helpers';
import { DialogError } from '@/components/app/GlobalConfigDialog.shared';

type GlobalConfigDialog = AppState['globalConfigDialog'];

export function GlobalConfigDialogView(): React.ReactElement {
  const controller = useController();
  const dispatch = useAppDispatch();
  const dialog = useAppSelector((state) => state.globalConfigDialog);

  return (
    <Dialog
      open={dialog.open}
      onOpenChange={(open) => {
        if (!open) dispatch(closeGlobalConfigDialog());
      }}
    >
      <DialogContent
        // Bound the panel to the viewport and let the body scroll, same shape as
        // EnvironmentDialogView — cloud aliases and cloud contexts grow this
        // dialog's content without limit, so a plain centered grid overflows off
        // both edges with nothing scrollable.
        className="flex max-h-[85vh] flex-col gap-0 overflow-hidden p-0 sm:max-w-xl"
        onCloseAutoFocus={(event) => {
          event.preventDefault();
          controller.focusTerminalSoon();
        }}
      >
        <form
          className="flex min-h-0 flex-1 flex-col"
          onSubmit={(event) => {
            event.preventDefault();
            void dispatch(submitGlobalConfig()).catch((error: unknown) => {
              dispatch(showTerminalError(readError(error)));
            });
          }}
        >
          <div className="shrink-0 px-6 pt-6 pb-4">
            <DialogHeader>
              <DialogTitle>ERun settings</DialogTitle>
              <DialogDescription>
                Default tenant, cloud aliases, and cloud contexts shared across the app.
              </DialogDescription>
            </DialogHeader>
          </div>
          <div className="grid min-h-0 flex-1 gap-4 overflow-y-auto px-6 pb-1">
            <GlobalConfigBody />
            <DialogError error={dialog.error} />
          </div>
          <div className="shrink-0 border-t px-6 pt-4 pb-6">
            <GlobalConfigFooter dialog={dialog} />
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function GlobalConfigBody(): React.ReactElement {
  const dispatch = useAppDispatch();
  const dialog = useAppSelector((state) => state.globalConfigDialog);
  const tenants = useAppSelector((state) => state.tenants.tenants);
  if (dialog.configLoading) {
    return (
      <div className="rounded-[var(--radius)] border border-dashed border-border px-3 py-2.5 text-[13px] leading-[1.35] text-muted-foreground">
        Loading config...
      </div>
    );
  }
  const tenantNames = optionValues(
    tenants.map((tenant) => tenant.name),
    dialog.config.defaultTenant,
  );
  const tenantOptions: SelectFieldOption[] =
    tenantNames.length === 0
      ? []
      : [
          { value: NOT_CONFIGURED_VALUE, label: 'Not configured' },
          ...tenantNames.map((name) => ({ value: name, label: name })),
        ];
  return (
    <div className="grid gap-3">
      <SelectField
        id="global-config-defaulttenant"
        label="Default tenant"
        value={dialog.config.defaultTenant || NOT_CONFIGURED_VALUE}
        options={tenantOptions}
        emptyLabel="No tenants"
        disabled={dialog.busy}
        onChange={(value) => {
          dispatch(
            updateGlobalConfig({ defaultTenant: value === NOT_CONFIGURED_VALUE ? '' : value }),
          );
        }}
      />
      <CloudAliasesSection dialog={dialog} />
      <CloudContextsSection dialog={dialog} />
    </div>
  );
}

function GlobalConfigFooter({ dialog }: { dialog: GlobalConfigDialog }): React.ReactElement {
  const dispatch = useAppDispatch();
  const saving = dialog.busyAction === 'save';
  return (
    <DialogFooter>
      <Button
        type="button"
        variant="outline"
        size="sm"
        disabled={dialog.busy}
        onClick={() => {
          dispatch(closeGlobalConfigDialog());
        }}
      >
        Cancel
      </Button>
      <Button type="submit" size="sm" disabled={dialog.busy || dialog.configLoading}>
        {saving ? (
          <LoaderCircle className="animate-spin" aria-hidden="true" />
        ) : (
          <Save aria-hidden="true" />
        )}
        {saving ? 'Saving...' : 'Save settings'}
      </Button>
    </DialogFooter>
  );
}
