import { Button, IconTooltip, Popover, PopoverAnchor, PopoverContent, StatusBadge } from 'erun-kit';
import { LoaderCircle, Spline, X } from 'lucide-react';
import * as React from 'react';

import { useAppDispatch } from '@/app/hooks';
import { whipNow, type WhipOutcome } from '@/app/whipThunks';
import { InlineAlert } from '@/components/app/InlineAlert';
import { whipOutcomeLabel, whipOutcomeTone } from '@/components/app/Titlebar.WhipAction.helpers';

import type { main } from '../../../wailsjs/go/models';

const whipButtonClassName =
  'size-7 flex-none cursor-pointer rounded-[var(--radius)] border-0 bg-transparent text-muted-foreground [--wails-draggable:no-drag] hover:bg-accent hover:text-accent-foreground [&_svg]:size-[18px]';

const whipLabel = 'Whip: push every live orchestrator and environment to keep moving';

// TitlebarWhipAction is the operator-triggered whip control:
// one click pushes every live orchestrator and every configured
// environment's own AI session with the pacing nudge, and reports exactly who
// was pushed and who was skipped, and why -- the report a whip is judged by
// (root AGENTS.md's "Smooth, Seamless, No Dead Ends": a control that runs and
// reports nothing is the exact dead end this closes).
//
// Whip is global -- every orchestrator, every environment -- so it lives in
// the titlebar rather than on a per-row control, beside the other
// action-with-a-reported-outcome affordances the titlebar's status surface
// already carries (Titlebar.Status.tsx's StatusWaitAction /
// StatusRestartOrchestratorAction). Its outcome renders inline in a popover
// anchored to the button that triggered it, rather than as a detached
// notification or an Activity Queue entry: erun-ui/AGENTS.md's Design-Language
// Decision Record reserves a detached surface for outcomes that resolve after
// the user has moved on, and a whip pass resolves in seconds while the button
// is still on screen. Whip is not destructive, so it runs directly on click
// with no confirmation.
export function TitlebarWhipAction(): React.ReactElement {
  const dispatch = useAppDispatch();
  const [open, setOpen] = React.useState(false);
  const [pending, setPending] = React.useState(false);
  const [outcome, setOutcome] = React.useState<WhipOutcome | null>(null);

  const onWhip = React.useCallback(() => {
    setOpen(true);
    setPending(true);
    void dispatch(whipNow()).then((result) => {
      setOutcome(result);
      setPending(false);
    });
  }, [dispatch]);

  return (
    <Popover open={open} onOpenChange={setOpen}>
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
            onClick={onWhip}
          >
            {pending ? (
              <LoaderCircle aria-hidden="true" className="animate-spin" />
            ) : (
              <Spline aria-hidden="true" />
            )}
          </Button>
        </IconTooltip>
      </PopoverAnchor>
      <PopoverContent align="end" className="w-[360px] p-0">
        <WhipReportPanel
          pending={pending}
          outcome={outcome}
          onClose={() => {
            setOpen(false);
          }}
        />
      </PopoverContent>
    </Popover>
  );
}

function WhipReportPanel({
  pending,
  outcome,
  onClose,
}: {
  pending: boolean;
  outcome: WhipOutcome | null;
  onClose: () => void;
}): React.ReactElement {
  return (
    <div className="flex max-h-[60vh] flex-col">
      <div className="flex items-center justify-between border-b px-3 py-2">
        <h2 className="text-sm font-semibold">Whip</h2>
        <Button
          type="button"
          variant="ghost"
          size="icon"
          aria-label="Close whip report"
          onClick={onClose}
        >
          <X aria-hidden="true" className="size-3.5" />
        </Button>
      </div>
      <div
        className="flex-1 overflow-y-auto p-3"
        role="status"
        aria-live="polite"
        aria-label="Whip results"
      >
        <WhipReportBody pending={pending} outcome={outcome} />
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
    return (
      <p className="text-xs text-muted-foreground">
        Pushing every live orchestrator and environment…
      </p>
    );
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
    return <p className="text-xs text-muted-foreground">Nothing configured to whip.</p>;
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
