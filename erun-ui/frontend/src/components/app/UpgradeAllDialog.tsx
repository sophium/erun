import { ArrowRight, ArrowUp, LoaderCircle, TriangleAlert } from 'lucide-react';
import * as React from 'react';

import { useAppDispatch, useAppSelector } from '@/app/hooks';
import { setUpgradeAllChoice } from '@/app/slices/upgradeAllSlice';
import { closeUpgradeAllDialog, confirmUpgradeAll } from '@/app/upgradeThunks';
import { selectionKey } from '@/app/versionSuggestions';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import type { UIUpgradePlanItem } from '@/types';

// rowKey is the per-env identity used for the choices map; it matches the key
// confirmUpgradeAll resolves the picked version under.
function rowKey(item: UIUpgradePlanItem): string {
  return selectionKey({ tenant: item.tenant, environment: item.environment });
}

// UpgradeAllDialog previews the cross-env "Upgrade all" plan and gates the
// deploy behind explicit confirmation (Nielsen #1 visibility of system status,
// #5 error prevention before a high-blast-radius action). It lists every
// opted-in env with its channel and current → target, marking which will be
// redeployed; envs whose registries offer more than one newer version get a
// per-row picker. Upgrade is enabled once at least one env will
// be redeployed (a lagging env, or one the operator has picked a version for).
export function UpgradeAllDialog(): React.ReactElement {
  const dispatch = useAppDispatch();
  const { open, loading, error, items, choices } = useAppSelector((state) => state.upgradeAll);
  const upgradeCount = countUpgrades(items, choices);
  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) {
          dispatch(closeUpgradeAllDialog());
        }
      }}
    >
      <DialogContent className="sm:max-w-2xl">
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
          choices={choices}
          upgradeCount={upgradeCount}
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
            disabled={loading || upgradeCount === 0}
            onClick={() => {
              void dispatch(confirmUpgradeAll());
            }}
          >
            <ArrowUp aria-hidden="true" />
            {upgradeCount > 0 ? `Upgrade ${String(upgradeCount)}` : 'Upgrade'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// countUpgrades is how many envs Upgrade will redeploy: every lagging env plus
// every ambiguous env the operator has picked a version for.
function countUpgrades(items: UIUpgradePlanItem[], choices: Record<string, string>): number {
  let count = 0;
  for (const item of items) {
    const state = upgradeRowState(item);
    if (state === 'lagging') {
      count += 1;
    } else if (state === 'pick' && (choices[rowKey(item)] ?? '').trim() !== '') {
      count += 1;
    }
  }
  return count;
}

function UpgradeAllBody({
  loading,
  error,
  items,
  choices,
  upgradeCount,
}: {
  loading: boolean;
  error: string;
  items: UIUpgradePlanItem[];
  choices: Record<string, string>;
  upgradeCount: number;
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
    <div className="max-h-80 overflow-y-auto">
      <table className="w-full text-sm" aria-label="Upgrade plan">
        <thead>
          <tr className="text-left text-[11px] tracking-wide text-muted-foreground uppercase">
            <th scope="col" className="pr-4 pb-2 font-medium">
              Environment
            </th>
            <th scope="col" className="pr-4 pb-2 font-medium">
              Channel
            </th>
            <th scope="col" className="pr-4 pb-2 font-medium">
              Version
            </th>
            <th scope="col" className="pb-2 text-right font-medium">
              Status
            </th>
          </tr>
        </thead>
        <tbody>
          {items.map((item) => (
            <UpgradePlanRow
              key={`${item.tenant}/${item.environment}`}
              item={item}
              chosen={choices[rowKey(item)] ?? ''}
            />
          ))}
        </tbody>
      </table>
      <p className="pt-3 text-[12px] text-muted-foreground">
        {upgradeCount} of {items.length} will be redeployed.
      </p>
      {unresolvedCount > 0 ? (
        <p className="flex items-start gap-1.5 pt-1.5 text-[12px] text-amber-600 dark:text-amber-500">
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

function UpgradePlanRow({
  item,
  chosen,
}: {
  item: UIUpgradePlanItem;
  chosen: string;
}): React.ReactElement {
  const state = upgradeRowState(item);
  return (
    <tr className="border-t border-border/60 align-top">
      <td className="py-2.5 pr-4">
        <div className="leading-tight font-medium break-words text-foreground">
          {item.environment}
        </div>
        <div className="text-[11px] break-words text-muted-foreground">{item.tenant}</div>
      </td>
      <td className="py-2.5 pr-4 whitespace-nowrap text-muted-foreground">{item.channel}</td>
      <td className="py-2.5 pr-4">
        <UpgradeVersionCell item={item} state={state} chosen={chosen} />
      </td>
      <td className="py-2.5 text-right">
        <UpgradePlanRowStatus state={state} reason={item.unresolvedReason} chosen={chosen} />
      </td>
    </tr>
  );
}

// UpgradeVersionCell shows the env's runtime version. A lagging env stacks
// current → target. An ambiguous env (more than one newer version across its
// registries) stacks current → a picker the operator chooses from, each option
// labelled with its source registry. Up-to-date and unresolved
// envs render the single current version, with the Status column carrying the
// rest.
function UpgradeVersionCell({
  item,
  state,
  chosen,
}: {
  item: UIUpgradePlanItem;
  state: UpgradeRowState;
  chosen: string;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const current = displayUpgradeVersion(item.current);
  if (state === 'pick') {
    return (
      <div className="font-mono text-[12px] leading-snug">
        <div className="flex items-center gap-1.5 whitespace-nowrap text-muted-foreground">
          <span className="w-3 flex-none" aria-hidden="true" />
          <span>{current}</span>
        </div>
        <div className="flex items-center gap-1.5">
          <ArrowRight className="size-3 flex-none text-muted-foreground" aria-hidden="true" />
          <Select
            value={chosen}
            onValueChange={(version) => {
              dispatch(setUpgradeAllChoice({ key: rowKey(item), version }));
            }}
          >
            <SelectTrigger
              size="sm"
              className="h-7 font-mono text-[12px]"
              aria-label={`Pick a version for ${item.environment}`}
            >
              <SelectValue placeholder="Pick a version" />
            </SelectTrigger>
            <SelectContent>
              {(item.candidates ?? []).map((candidate) => (
                <SelectItem key={candidate.version} value={candidate.version}>
                  {candidate.version}
                  {candidate.registry ? ` · ${candidate.registry}` : ''}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      </div>
    );
  }
  if (state !== 'lagging') {
    return <span className="font-mono text-[12px] whitespace-nowrap">{current}</span>;
  }
  return (
    <div className="font-mono text-[12px] leading-snug">
      <div className="flex items-center gap-1.5 whitespace-nowrap text-muted-foreground">
        <span className="w-3 flex-none" aria-hidden="true" />
        <span>{current}</span>
      </div>
      <div className="flex items-center gap-1.5 whitespace-nowrap text-foreground">
        <ArrowRight className="size-3 flex-none text-muted-foreground" aria-hidden="true" />
        <span>{displayUpgradeVersion(item.target)}</span>
      </div>
    </div>
  );
}

function UpgradePlanRowStatus({
  state,
  reason,
  chosen,
}: {
  state: UpgradeRowState;
  reason?: string;
  chosen: string;
}): React.ReactElement {
  if (state === 'lagging') {
    return (
      <span className="text-[11px] font-medium whitespace-nowrap text-primary">will upgrade</span>
    );
  }
  if (state === 'pick') {
    // More than one newer version across the env's registries — the operator
    // must pick one before it can be redeployed. Once picked it
    // joins the upgrade set; until then it is amber + icon (non-color-only,
    // WCAG) so the dialog is honest that nothing happens for it yet.
    if (chosen.trim() !== '') {
      return (
        <span className="text-[11px] font-medium whitespace-nowrap text-primary">will upgrade</span>
      );
    }
    return (
      <span className="inline-flex items-center justify-end gap-1 whitespace-nowrap text-[11px] font-medium text-amber-600 dark:text-amber-500">
        <TriangleAlert className="size-3 flex-none" aria-hidden="true" />
        pick a version
      </span>
    );
  }
  if (state === 'unresolved') {
    // Distinct from "up to date": the channel's latest could not be resolved,
    // so we can't tell whether this env lags. Amber + icon (not muted, not the
    // success-coloured "up to date") keeps the status honest and non-color-only
    // (WCAG). Mirrors the CLI's "(target unresolved)"; the reason under it is
    // the same one the CLI traces, so the operator sees why
    // (e.g. a registry 403) without leaving the dialog.
    return (
      <span className="inline-flex flex-col items-end gap-0.5">
        <span className="inline-flex items-center gap-1 whitespace-nowrap text-[11px] font-medium text-amber-600 dark:text-amber-500">
          <TriangleAlert className="size-3 flex-none" aria-hidden="true" />
          latest unknown
        </span>
        {reason ? (
          <span className="max-w-56 text-right text-[10px] leading-snug break-words text-muted-foreground">
            {reason}
          </span>
        ) : null}
      </span>
    );
  }
  return <span className="text-[11px] whitespace-nowrap text-muted-foreground">up to date</span>;
}

type UpgradeRowState = 'lagging' | 'pick' | 'upToDate' | 'unresolved';

// upgradeRowState mirrors the outcomes the CLI's `erun upgrade` renders, plus
// the desktop-only "pick" state: an opted-in env either lags a
// known channel latest (will be redeployed), has more than one newer version
// across its registries (the operator picks one), already sits at the known
// latest (up to date), or has no resolvable target — the registry lookup
// failed or returned no matching tags ("(target unresolved)"). The desktop must
// not collapse the unresolved or pick states into "up to date": doing so
// mislabels a failed lookup or an un-picked env as success.
function upgradeRowState(item: UIUpgradePlanItem): UpgradeRowState {
  if (item.lagging) {
    return 'lagging';
  }
  if ((item.candidates?.length ?? 0) > 1) {
    return 'pick';
  }
  if (item.target.trim() === '') {
    return 'unresolved';
  }
  return 'upToDate';
}

function displayUpgradeVersion(value: string): string {
  return value.trim() === '' ? '(unset)' : value;
}
