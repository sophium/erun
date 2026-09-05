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

import type { main } from '../../../wailsjs/go/models';

const whipButtonClassName =
  'size-7 flex-none cursor-pointer rounded-[var(--radius)] border-0 bg-transparent text-muted-foreground [--wails-draggable:no-drag] hover:bg-accent hover:text-accent-foreground [&_svg]:size-[18px]';

const whipLabel = 'Whip: push the focused target, or choose which ones to push';

// useWhipAutoDismiss is the whip popover's auto-dismiss gate, pulled out of
// TitlebarWhipAction so that component stays under the file's function-length
// budget. A fully successful report auto-dismisses after the app's
// established transient duration, matching notificationThunks' own
// success/info rule -- anything else (capped/failed/skipped, or a call
// failure) stays on screen until the operator closes it, since that is the
// one place a push's real outcome is reported. Keyed on
// [outcome, pending, hovered] rather than starting the timer from onWhip
// directly: a fresh open nulls outcome (TitlebarWhipAction's onOpenChange),
// which reruns this effect and clears any timer left over from a previous
// report before the new pass even starts, so a second whip issued before the
// first timer fires can never dismiss the new report early.
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

// TitlebarWhipAction is the operator-triggered whip control. Whipping is a
// write into a live AI session, not a read, so a click never fans out to
// every configured environment and orchestrator on its own (erun#1700):
// opening the popover preselects only the sidebar's current focus, and the
// selection surface underneath (TitlebarWhipTargetPicker) is where "actually,
// more than that" becomes a deliberate choice, with the three group shortcuts
// and a primary action that states the count before it acts. Nothing here
// persists between opens -- every open starts over from the current focus,
// never a previous invocation's selection.
//
// It lives in the titlebar rather than on a per-row control, beside the other
// action-with-a-reported-outcome affordances the titlebar's status surface
// already carries (Titlebar.Status.tsx's StatusWaitAction /
// StatusRestartOrchestratorAction). Its outcome renders inline in the same
// popover rather than as a detached notification or an Activity Queue entry:
// erun-ui/AGENTS.md's Design-Language Decision Record reserves a detached
// surface for outcomes that resolve after the user has moved on, and a whip
// pass resolves in seconds while the button is still on screen.
export function TitlebarWhipAction(): React.ReactElement {
  const dispatch = useAppDispatch();
  const defaultTarget = useAppSelector(selectWhipDefaultTarget);
  const [open, setOpen] = React.useState(false);
  const [pending, setPending] = React.useState(false);
  const [outcome, setOutcome] = React.useState<WhipOutcome | null>(null);
  const [targets, setTargets] = React.useState<main.uiWhipTargetList | null>(null);
  const [targetsLoading, setTargetsLoading] = React.useState(false);
  const [selection, setSelection] = React.useState<WhipTargetSelection>(
    defaultWhipTargetSelection(null),
  );
  const [hovered, setHovered] = React.useState(false);
  // setOpen is a stable dispatch reference, so this callback never changes
  // identity -- useWhipAutoDismiss's effect must not re-run (and restart its
  // timer) on every unrelated re-render.
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
      // A fresh open always starts over: no leftover selection or report from
      // a previous invocation (erun#1700's "remember nothing across
      // invocations"), and the default is recomputed from whatever the
      // sidebar is focused on right now.
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
    // Re-resolve the population immediately before pushing, not just at open
    // or at the last "select all" click: an "all" category follows whoever is
    // eligible right now, including an orchestrator that started after the
    // popover opened (erun#1700's "group selects follow the population, not a
    // snapshot").
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
      {/* No asChild here: PopoverAnchor's Slot-based asChild clones its
          single child looking for one forwarded DOM ref, and IconTooltip is
          a plain component that does not forward one, so the anchor rect
          Popper measures against silently stays unset and the content
          renders off-position. Rendering PopoverAnchor's own real wrapper div
          keeps a genuine DOM node to anchor against. */}
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
  targets: main.uiWhipTargetList | null;
  targetsLoading: boolean;
  selection: WhipTargetSelection;
  setSelection: React.Dispatch<React.SetStateAction<WhipTargetSelection>>;
  count: number;
  onWhip: () => void;
  onClose: () => void;
}): React.ReactElement {
  // The report view replaces the picker (and its own selection controls)
  // below, so the header's whip action follows the same gate: it only makes
  // sense to show while there is a selection left to act on.
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

function WhipResultRow({ result }: { result: main.uiWhipResult }): React.ReactElement {
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
