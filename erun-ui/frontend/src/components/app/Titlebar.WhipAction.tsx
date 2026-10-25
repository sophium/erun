import { Button, IconTooltip, Popover, PopoverAnchor, PopoverContent, StatusBadge } from 'erun-kit';
import { LoaderCircle, Spline, X } from 'lucide-react';
import * as React from 'react';

import { useAppDispatch, useAppSelector } from '@/app/hooks';
import type { WhipTargetSelection } from '@/app/model';
import { selectWhipDefaultTarget } from '@/app/selectors';
import { TRANSIENT_DISMISS_MS } from '@/app/transientDismissDuration';
import { scheduleTransientDismiss } from '@/app/transientDismissTimer';
import {
  countWhipTargets,
  defaultWhipTargetSelection,
  resolveWhipTargetRefs,
  selectAllWhipEnvironments,
  selectAllWhipOrchestrators,
  selectAllWhipTargets,
  toggleWhipEnvironment,
  toggleWhipOrchestrator,
} from '@/app/whipTargetSelection';
import { whipNow, type WhipOutcome, whipTargets } from '@/app/whipThunks';
import { InlineAlert } from '@/components/app/InlineAlert';
import {
  whipOutcomeLabel,
  whipOutcomeTone,
  whipReportAllSucceeded,
} from '@/components/app/Titlebar.WhipAction.helpers';
import { TitlebarWhipTargetPicker } from '@/components/app/Titlebar.WhipAction.TargetPicker';

import type { orchestrator } from '../../../wailsjs/go/models';

const whipButtonClassName =
  'size-7 flex-none cursor-pointer rounded-[var(--radius)] border-0 bg-transparent text-muted-foreground [--wails-draggable:no-drag] hover:bg-accent hover:text-accent-foreground [&_svg]:size-[18px]';

const whipLabel = 'Whip: push the focused target, or choose which ones to push';

// useWhipAutoDismiss auto-dismisses only a fully successful report. A fresh open
// nulls outcome, rerunning the effect and clearing any earlier timer.
function useWhipAutoDismiss({
  pending,
  outcome,
  hovered,
  onDismiss,
}: {
  pending: boolean;
  outcome: WhipOutcome | null;
  hovered: boolean;
  onDismiss: () => void;
}): void {
  React.useEffect(() => {
    if (
      pending ||
      !outcome ||
      outcome.error ||
      hovered ||
      !whipReportAllSucceeded(outcome.report)
    ) {
      return;
    }
    return scheduleTransientDismiss(TRANSIENT_DISMISS_MS, onDismiss);
  }, [pending, outcome, hovered, onDismiss]);
}

function whipButtonLabel(count: number): string {
  return `Whip ${String(count)} target${count === 1 ? '' : 's'}`;
}

// TitlebarWhipAction writes into live AI sessions, so each open preselects only
// the sidebar's current focus; widening the selection is deliberate.
export function TitlebarWhipAction(): React.ReactElement {
  const dispatch = useAppDispatch();
  const defaultTarget = useAppSelector(selectWhipDefaultTarget);
  const [open, setOpen] = React.useState(false);
  const [pending, setPending] = React.useState(false);
  const [outcome, setOutcome] = React.useState<WhipOutcome | null>(null);
  const [targets, setTargets] = React.useState<orchestrator.WhipTargetList | null>(null);
  const [targetsLoading, setTargetsLoading] = React.useState(false);
  const [selection, setSelection] = React.useState<WhipTargetSelection>(
    defaultWhipTargetSelection(null),
  );
  const [hovered, setHovered] = React.useState(false);
  // Stable identity so useWhipAutoDismiss does not restart its timer.
  const onAutoDismiss = React.useCallback(() => {
    setOpen(false);
  }, []);
  useWhipAutoDismiss({ pending, outcome, hovered, onDismiss: onAutoDismiss });

  const onOpenChange = React.useCallback(
    (next: boolean) => {
      setOpen(next);
      if (!next) {
        return;
      }
      setOutcome(null);
      setSelection(defaultWhipTargetSelection(defaultTarget));
      setTargetsLoading(true);
      void dispatch(whipTargets()).then((list) => {
        setTargets(list);
        setTargetsLoading(false);
      });
    },
    [dispatch, defaultTarget],
  );

  const onWhip = React.useCallback(() => {
    setPending(true);
    // Re-resolve right before pushing so group selects follow the current population.
    void dispatch(whipTargets()).then((freshTargets) => {
      const refs = resolveWhipTargetRefs(selection, freshTargets);
      void dispatch(whipNow(refs)).then((result) => {
        setOutcome(result);
        setPending(false);
      });
    });
  }, [dispatch, selection]);

  const count = targets ? countWhipTargets(selection, targets) : 0;

  return (
    <Popover open={open} onOpenChange={onOpenChange}>
      {/* No asChild: IconTooltip does not forward a ref, so the anchor would stay unset. */}
      <PopoverAnchor>
        <IconTooltip label={whipLabel}>
          <Button
            className={whipButtonClassName}
            type="button"
            variant="ghost"
            size="icon"
            aria-label={whipLabel}
            onClick={() => {
              onOpenChange(true);
            }}
          >
            {pending ? (
              <LoaderCircle aria-hidden="true" className="animate-spin" />
            ) : (
              <Spline aria-hidden="true" />
            )}
          </Button>
        </IconTooltip>
      </PopoverAnchor>
      <PopoverContent
        align="end"
        className="w-[360px] p-0"
        onMouseEnter={() => {
          setHovered(true);
        }}
        onMouseLeave={() => {
          setHovered(false);
        }}
      >
        <WhipPopoverBody
          pending={pending}
          outcome={outcome}
          targets={targets}
          targetsLoading={targetsLoading}
          selection={selection}
          setSelection={setSelection}
          count={count}
          onWhip={onWhip}
          onClose={() => {
            setOpen(false);
          }}
        />
      </PopoverContent>
    </Popover>
  );
}

function WhipPopoverBody({
  pending,
  outcome,
  targets,
  targetsLoading,
  selection,
  setSelection,
  count,
  onWhip,
  onClose,
}: {
  pending: boolean;
  outcome: WhipOutcome | null;
  targets: orchestrator.WhipTargetList | null;
  targetsLoading: boolean;
  selection: WhipTargetSelection;
  setSelection: React.Dispatch<React.SetStateAction<WhipTargetSelection>>;
  count: number;
  onWhip: () => void;
  onClose: () => void;
}): React.ReactElement {
  const showPicker = !outcome && !pending;
  return (
    <div role="region" aria-label="Whip" className="flex max-h-[70vh] flex-col">
      <div className="flex items-center justify-between gap-2 border-b px-3 py-2">
        <h2 className="min-w-0 flex-1 truncate text-sm font-semibold">Whip</h2>
        {showPicker && (
          <Button
            type="button"
            variant="default"
            size="sm"
            disabled={targetsLoading || count === 0}
            onClick={onWhip}
          >
            {whipButtonLabel(count)}
          </Button>
        )}
        <Button type="button" variant="ghost" size="icon" aria-label="Close whip" onClick={onClose}>
          <X aria-hidden="true" className="size-3.5" />
        </Button>
      </div>
      <div className="flex-1 overflow-y-auto p-3">
        {showPicker ? (
          <TitlebarWhipTargetPicker
            targets={targets}
            targetsLoading={targetsLoading}
            selection={selection}
            onToggleEnvironment={(id, checked) => {
              setSelection((prev) => toggleWhipEnvironment(prev, id, checked));
            }}
            onToggleOrchestrator={(id, checked) => {
              setSelection((prev) => toggleWhipOrchestrator(prev, id, checked));
            }}
            onSelectAllEnvironments={() => {
              setSelection((prev) => selectAllWhipEnvironments(prev));
            }}
            onSelectAllOrchestrators={() => {
              setSelection((prev) => selectAllWhipOrchestrators(prev));
            }}
            onSelectAll={() => {
              setSelection((prev) => selectAllWhipTargets(prev));
            }}
          />
        ) : (
          <div role="status" aria-live="polite" aria-label="Whip results">
            <WhipReportBody pending={pending} outcome={outcome} />
          </div>
        )}
      </div>
    </div>
  );
}

function WhipReportBody({
  pending,
  outcome,
}: {
  pending: boolean;
  outcome: WhipOutcome | null;
}): React.ReactElement | null {
  if (pending) {
    return <p className="text-xs text-muted-foreground">Whipping the selected targets…</p>;
  }
  if (!outcome) {
    return null;
  }
  if (outcome.error) {
    return (
      <InlineAlert>
        <p className="text-xs">{outcome.error}</p>
      </InlineAlert>
    );
  }
  const results = outcome.report?.results ?? [];
  if (results.length === 0) {
    return <p className="text-xs text-muted-foreground">Nothing was targeted.</p>;
  }
  return (
    <ul className="space-y-1.5">
      {results.map((result) => (
        <WhipResultRow key={`${result.kind}:${result.id}`} result={result} />
      ))}
    </ul>
  );
}

function WhipResultRow({ result }: { result: orchestrator.WhipResult }): React.ReactElement {
  return (
    <li className="flex flex-col gap-0.5 text-xs">
      <div className="flex items-center justify-between gap-2">
        <span className="min-w-0 truncate font-medium" title={result.name}>
          {result.name}
        </span>
        <StatusBadge
          tone={whipOutcomeTone(result.outcome)}
          label={whipOutcomeLabel(result.outcome)}
        />
      </div>
      {result.reason && (
        <p className="text-muted-foreground">
          {result.reason}
          {result.error ? `: ${result.error}` : ''}
        </p>
      )}
    </li>
  );
}
