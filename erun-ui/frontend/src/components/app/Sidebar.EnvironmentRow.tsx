import { LoaderCircle, MoreHorizontal } from 'lucide-react';
import * as React from 'react';

import { readError } from '@/app/errors';
import { useAppDispatch, useAppSelector } from '@/app/hooks';
import { openManageDialog } from '@/app/manageEnvironmentThunks';
import { showTerminalMessage } from '@/app/notificationThunks';
import { openSelection } from '@/app/sessionThunks';
import { envKey } from '@/app/slices/sessionsSlice';
import { selectionKey } from '@/app/versionSuggestions';
import { IconTooltip } from '@/components/app/IconTooltip';
import { deriveEnvironmentRow } from '@/components/app/Sidebar.helpers';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import type { UISelection } from '@/types';

function LocalEnvBadge({ selected }: { selected: boolean }): React.ReactElement {
  return (
    <span
      className={cn(
        'flex-none rounded-[calc(var(--radius)-4px)] border px-1 py-px text-[10px] font-medium uppercase leading-none tracking-wide',
        selected
          ? 'border-primary-foreground/40 text-primary-foreground/85'
          : 'border-border text-muted-foreground',
      )}
      aria-label="Local environment"
    >
      Local
    </span>
  );
}

function EnvironmentRowEditButton({
  tenantName,
  environmentName,
  selected,
  selection,
}: {
  tenantName: string;
  environmentName: string;
  selected: boolean;
  selection: UISelection;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  return (
    <IconTooltip label="Edit environment settings">
      <Button
        type="button"
        className={cn(
          'pointer-events-none size-[26px] flex-none cursor-pointer border-0 bg-transparent text-current opacity-0 transition-[opacity,background-color,color] duration-150 hover:bg-[color-mix(in_oklch,currentColor_12%,transparent)] hover:text-current group-hover:pointer-events-auto group-hover:opacity-100 group-focus-within:pointer-events-auto group-focus-within:opacity-100 [&_svg]:size-4',
          selected && 'pointer-events-auto opacity-100',
        )}
        variant="ghost"
        size="icon"
        aria-label={`Edit ${tenantName} / ${environmentName} settings`}
        onClick={(event) => {
          event.stopPropagation();
          dispatch(openManageDialog(selection));
        }}
      >
        <MoreHorizontal />
      </Button>
    </IconTooltip>
  );
}

function BusyRowSpinner({ label }: { label: string }): React.ReactElement {
  return (
    <LoaderCircle
      className="size-3.5 flex-none animate-spin text-current opacity-75"
      aria-label={label || undefined}
      aria-hidden={label ? undefined : true}
      role={label ? 'status' : undefined}
    />
  );
}

export function EnvironmentRow({
  tenantName,
  environmentName,
}: {
  tenantName: string;
  environmentName: string;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const selectedSelection = useAppSelector((state) => state.selection.selected);
  const tenants = useAppSelector((state) => state.tenants.tenants);
  const isOpening = useAppSelector(
    (state) => state.sessions.openingByEnv[envKey(tenantName, environmentName)] === true,
  );
  // runningCommand is the first 'running' activity command attached to
  // this env (deploy / init / sshd-init / doctor / build / push /
  // release). Picking the first entry keeps the selector primitive-
  // returning so React-Redux's default equality short-circuits row
  // re-renders when unrelated activity churns. The activity slice is
  // additive, so once one entry transitions to running the row stays
  // busy until its status flips.
  const runningCommand = useAppSelector((state) => {
    for (const entry of state.activity.entries) {
      if (
        entry.tenant === tenantName &&
        entry.environment === environmentName &&
        entry.status === 'running'
      ) {
        return entry.command;
      }
    }
    return '';
  });
  const aiBusy = useAppSelector(
    (state) =>
      state.aiActivity.aiBusyByEnv[
        selectionKey({ tenant: tenantName, environment: environmentName })
      ] === true,
  );
  const { selected, busy, busyLabel, isLocal, selection } = deriveEnvironmentRow(
    tenantName,
    environmentName,
    selectedSelection,
    tenants,
    isOpening,
    runningCommand,
    aiBusy,
  );

  return (
    <div
      className={cn(
        'group relative mr-1 ml-1 flex h-8 items-center rounded-md pr-1.5 text-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground',
        selected &&
          'bg-primary text-primary-foreground shadow-sm hover:bg-primary hover:text-primary-foreground',
      )}
    >
      <button
        type="button"
        className={cn(
          'flex h-8 min-w-0 flex-1 cursor-pointer items-center gap-1.5 border-0 bg-transparent py-0 pr-2 pl-10 text-left text-sm leading-[1.2] tracking-normal text-inherit',
          selected ? 'font-medium' : 'font-normal',
        )}
        title={`${tenantName} / ${environmentName}${isLocal ? ' (local)' : ''}`}
        aria-current={selected ? 'page' : undefined}
        onClick={() => {
          void dispatch(openSelection(selection)).catch((error: unknown) => {
            dispatch(showTerminalMessage(readError(error)));
          });
        }}
      >
        <span className="min-w-0 truncate">{environmentName}</span>
        {isLocal && <LocalEnvBadge selected={selected} />}
        {busy && <BusyRowSpinner label={busyLabel} />}
      </button>
      <EnvironmentRowEditButton
        tenantName={tenantName}
        environmentName={environmentName}
        selected={selected}
        selection={selection}
      />
    </div>
  );
}

// PendingEnvironmentRow renders an optimistic, non-interactive
// placeholder row for an environment that is currently being created
// by `erun init`. It exists to satisfy Nielsen #1 (visibility of
// system status) for the ~1–2 min init runs: without it,
// state.selected is set but produces no visible affordance because
// the env is not in state.tenants yet. Italic name + "Creating"
// badge + spinner + aria-busy communicate the in-flight state
// without inviting interaction.
export function PendingEnvironmentRow({
  tenantName,
  environmentName,
}: {
  tenantName: string;
  environmentName: string;
}): React.ReactElement {
  return (
    <div
      className="group relative mr-1 ml-1 flex h-8 items-center rounded-md pr-1.5 text-muted-foreground"
      aria-busy="true"
      aria-live="polite"
      aria-label={`Creating ${tenantName} / ${environmentName}`}
    >
      <div className="flex h-8 min-w-0 flex-1 items-center gap-1.5 py-0 pr-2 pl-10 text-left text-sm leading-[1.2]">
        <span className="min-w-0 truncate italic">{environmentName}</span>
        <span
          className="flex-none rounded-[calc(var(--radius)-4px)] border border-border px-1 py-px text-[10px] font-medium uppercase leading-none tracking-wide text-muted-foreground"
          aria-hidden="true"
        >
          Creating
        </span>
        <LoaderCircle
          className="size-3.5 flex-none animate-spin text-current opacity-75"
          aria-hidden="true"
        />
      </div>
    </div>
  );
}
