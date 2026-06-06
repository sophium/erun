import { ArrowUp, LoaderCircle, TriangleAlert } from 'lucide-react';
import * as React from 'react';

import { useAppDispatch, useAppSelector } from '@/app/hooks';
import { closeUpgradeAllDialog, confirmUpgradeAll } from '@/app/upgradeThunks';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import type { UIUpgradePlanItem } from '@/types';

// UpgradeAllDialog previews the cross-env "Upgrade all" plan and gates the
// deploy behind explicit confirmation (Nielsen #1 visibility of system status,
// #5 error prevention before a high-blast-radius action). It lists every
// opted-in env with its channel and current → target, marking which will be
// redeployed, and only enables Upgrade when at least one env lags.
export function UpgradeAllDialog(): React.ReactElement {
  const dispatch = useAppDispatch();
  const { open, loading, error, items } = useAppSelector((state) => state.upgradeAll);
  const lagging = items.filter((item) => item.lagging);
  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) {
          dispatch(closeUpgradeAllDialog());
        }
      }}
    >
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Upgrade all environments</DialogTitle>
          <DialogDescription>
            Redeploys every environment opted into Upgrade all whose version lags the latest for its
            channel. Review the plan before confirming — this rolls out new runtime images and
            restarts pods.
          </DialogDescription>
        </DialogHeader>
        <UpgradeAllBody
          loading={loading}
          error={error}
          items={items}
          laggingCount={lagging.length}
        />
        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            onClick={() => {
              dispatch(closeUpgradeAllDialog());
            }}
          >
            Cancel
          </Button>
          <Button
            type="button"
            disabled={loading || lagging.length === 0}
            onClick={() => {
              void dispatch(confirmUpgradeAll());
            }}
          >
            <ArrowUp aria-hidden="true" />
            {lagging.length > 0 ? `Upgrade ${String(lagging.length)}` : 'Upgrade'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function UpgradeAllBody({
  loading,
  error,
  items,
  laggingCount,
}: {
  loading: boolean;
  error: string;
  items: UIUpgradePlanItem[];
  laggingCount: number;
}): React.ReactElement {
  if (loading) {
    return (
      <div className="flex items-center gap-2 py-4 text-sm text-muted-foreground">
        <LoaderCircle className="size-4 animate-spin" aria-hidden="true" />
        Resolving upgrade plan…
      </div>
    );
  }
  if (error) {
    return (
      <p role="alert" className="py-4 text-sm text-destructive">
        {error}
      </p>
    );
  }
  if (items.length === 0) {
    return (
      <p className="py-4 text-sm text-muted-foreground">
        No environments are opted into Upgrade all. Turn on “Include in Upgrade all” in an
        environment’s Runtime settings.
      </p>
    );
  }
  const unresolvedCount = items.filter((item) => upgradeRowState(item) === 'unresolved').length;
  return (
    <div className="max-h-72 overflow-y-auto">
      <table className="w-full text-sm" aria-label="Upgrade plan">
        <thead>
          <tr className="text-left text-[12px] text-muted-foreground">
            <th className="py-1 pr-3 font-medium">Environment</th>
            <th className="py-1 pr-3 font-medium">Channel</th>
            <th className="py-1 font-medium">Current → target</th>
          </tr>
        </thead>
        <tbody>
          {items.map((item) => (
            <UpgradePlanRow key={`${item.tenant}/${item.environment}`} item={item} />
          ))}
        </tbody>
      </table>
      <p className="pt-2 text-[12px] text-muted-foreground">
        {laggingCount} of {items.length} will be redeployed.
      </p>
      {unresolvedCount > 0 ? (
        <p className="flex items-start gap-1.5 pt-1 text-[12px] text-amber-600 dark:text-amber-500">
          <TriangleAlert className="mt-0.5 size-3.5 flex-none" aria-hidden="true" />
          <span>
            {unresolvedCount === 1
              ? '1 environment couldn’t be checked against the latest version for its channel'
              : `${String(unresolvedCount)} environments couldn’t be checked against the latest version for their channel`}{' '}
            — the runtime image registry may be unreachable or have no matching tags, so there’s
            nothing to redeploy them to.
          </span>
        </p>
      ) : null}
    </div>
  );
}

function UpgradePlanRow({ item }: { item: UIUpgradePlanItem }): React.ReactElement {
  const state = upgradeRowState(item);
  return (
    <tr className={state === 'upToDate' ? 'text-muted-foreground' : 'text-foreground'}>
      <td className="py-1 pr-3">
        {item.tenant} / {item.environment}
      </td>
      <td className="py-1 pr-3">{item.channel}</td>
      <td className="py-1 font-mono text-[12px]">
        {displayUpgradeVersion(item.current)} → {displayUpgradeVersion(item.target)}
        <UpgradePlanRowStatus state={state} />
      </td>
    </tr>
  );
}

function UpgradePlanRowStatus({ state }: { state: UpgradeRowState }): React.ReactElement {
  if (state === 'lagging') {
    return <span className="ml-2 font-sans text-[11px] text-primary">will upgrade</span>;
  }
  if (state === 'unresolved') {
    // Distinct from "up to date": the channel's latest could not be resolved,
    // so we can't tell whether this env lags. Amber + icon (not muted, not the
    // success-coloured "up to date") keeps the status honest and non-color-only
    // (WCAG). Mirrors the CLI's "(target unresolved)".
    return (
      <span className="ml-2 inline-flex items-center gap-1 align-middle font-sans text-[11px] text-amber-600 dark:text-amber-500">
        <TriangleAlert className="size-3" aria-hidden="true" />
        latest unknown
      </span>
    );
  }
  return <span className="ml-2 font-sans text-[11px] text-muted-foreground">up to date</span>;
}

type UpgradeRowState = 'lagging' | 'upToDate' | 'unresolved';

// upgradeRowState mirrors the three-way outcome the CLI's `erun upgrade` already
// renders (see laggingSuffix): an opted-in env either lags a known channel
// latest (will be redeployed), already sits at the known latest (up to date),
// or has no resolvable target — the registry lookup failed or returned no
// matching tags, which the CLI reports as "(target unresolved)". The desktop
// must not collapse that third state into "up to date": doing so mislabels a
// failed/empty lookup as success and makes Upgrade all look like it is doing
// nothing.
function upgradeRowState(item: UIUpgradePlanItem): UpgradeRowState {
  if (item.lagging) {
    return 'lagging';
  }
  if (item.target.trim() === '') {
    return 'unresolved';
  }
  return 'upToDate';
}

function displayUpgradeVersion(value: string): string {
  return value.trim() === '' ? '(unset)' : value;
}
