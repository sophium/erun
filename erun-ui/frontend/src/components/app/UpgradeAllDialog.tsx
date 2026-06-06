import { ArrowUp, LoaderCircle } from 'lucide-react';
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
            <tr
              key={`${item.tenant}/${item.environment}`}
              className={item.lagging ? 'text-foreground' : 'text-muted-foreground'}
            >
              <td className="py-1 pr-3">
                {item.tenant} / {item.environment}
              </td>
              <td className="py-1 pr-3">{item.channel}</td>
              <td className="py-1 font-mono text-[12px]">
                {displayUpgradeVersion(item.current)} → {displayUpgradeVersion(item.target)}
                {item.lagging ? (
                  <span className="ml-2 font-sans text-[11px] text-primary">will upgrade</span>
                ) : (
                  <span className="ml-2 font-sans text-[11px]">up to date</span>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      <p className="pt-2 text-[12px] text-muted-foreground">
        {laggingCount} of {items.length} will be redeployed.
      </p>
    </div>
  );
}

function displayUpgradeVersion(value: string): string {
  return value.trim() === '' ? '(unset)' : value;
}
