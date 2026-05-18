import { AlertTriangle } from 'lucide-react';
import * as React from 'react';

import { useAppDispatch } from '@/app/hooks';
import { updateManageDialog } from '@/app/manageEnvironmentThunks';
import type { AppState } from '@/app/state';
import { TextField } from '@/components/app/ManageDialog.fields';

type ManageDialog = AppState['manageDialog'];

export function DeleteConfirmationFields({
  dialog,
  confirmationRef,
  expected,
}: {
  dialog: ManageDialog;
  confirmationRef: React.Ref<HTMLInputElement>;
  expected: string;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  return (
    <div className="grid gap-3">
      <DeleteWarning expected={expected} />
      <TextField
        id="manage-confirmation"
        label="Confirmation"
        value={dialog.confirmation}
        disabled={dialog.busy}
        inputRef={confirmationRef}
        onChange={(confirmation) => {
          dispatch(updateManageDialog({ confirmation }));
        }}
      />
    </div>
  );
}

function DeleteWarning({ expected }: { expected: string }): React.ReactElement {
  return (
    <div className="grid grid-cols-[18px_minmax(0,1fr)] items-start gap-[9px] rounded-[var(--radius)] border border-[color-mix(in_oklch,var(--destructive)_30%,var(--border))] bg-[color-mix(in_oklch,var(--destructive)_7%,transparent)] px-[11px] py-2.5 text-[13px] leading-[1.35] text-foreground">
      <AlertTriangle className="mt-px size-[17px] text-destructive" aria-hidden="true" />
      <span>
        Type{' '}
        <code className="rounded-[calc(var(--radius)-4px)] bg-[color-mix(in_oklch,var(--destructive)_12%,transparent)] px-1 py-px font-mono text-xs text-destructive">
          {expected}
        </code>{' '}
        to confirm.
      </span>
    </div>
  );
}
