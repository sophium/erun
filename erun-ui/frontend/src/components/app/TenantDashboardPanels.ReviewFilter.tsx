// The Reviews tab's own filter chip pair, split out of
// TenantDashboardPanels.Reviews.tsx to keep that file inside eslint's
// lines-budget. Behaviour is unchanged: two independently-toggling chips in
// one grouped pill.
import { cn } from 'erun-kit';
import * as React from 'react';

// ReviewFilterSegmentedControl is one grouped control, not two independent
// buttons: Mine and Waiting-on-me visually merge into a single pill,
// matching the DiffSourceButton segmented-toggle pattern the review panel's
// Env/ERun source switch already uses (Nielsen #4, consistency). Each side
// still toggles independently — a review can be both — so this is a grouped
// filter chip pair, not a mutually-exclusive tab strip. The count on each
// side is the discovery signal itself: which pile has work in it is visible
// before either is clicked, rather than only after.
export function ReviewFilterSegmentedControl({
  mine,
  waitingOnMe,
  mineCount,
  waitingOnMeCount,
  onToggleMine,
  onToggleWaitingOnMe,
}: {
  mine: boolean;
  waitingOnMe: boolean;
  mineCount: number | undefined;
  waitingOnMeCount: number | undefined;
  onToggleMine: () => void;
  onToggleWaitingOnMe: () => void;
}): React.ReactElement {
  return (
    <div className="flex items-center gap-1 rounded-[var(--radius)] border border-input bg-background p-1 text-[13px]">
      <ReviewFilterToggle label="Mine" count={mineCount} active={mine} onClick={onToggleMine} />
      <ReviewFilterToggle
        label="Waiting on me"
        count={waitingOnMeCount}
        active={waitingOnMe}
        onClick={onToggleWaitingOnMe}
      />
    </div>
  );
}

// ReviewFilterToggle is a one-click discovery affordance, not a form field:
// clicking answers "which are mine" or "which are waiting on me" directly.
// The count renders inside the button's own accessible name (e.g. "Mine 2")
// so a screen reader announces the same distribution a sighted operator sees.
export function ReviewFilterToggle({
  label,
  count,
  active,
  onClick,
}: {
  label: string;
  count: number | undefined;
  active: boolean;
  onClick: () => void;
}): React.ReactElement {
  return (
    <button
      type="button"
      aria-pressed={active}
      onClick={onClick}
      className={cn(
        'flex cursor-pointer items-center gap-1.5 rounded-[calc(var(--radius)-2px)] px-2.5 py-1 transition-colors focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-1 focus-visible:outline-none',
        active
          ? 'bg-primary text-primary-foreground'
          : 'text-muted-foreground hover:bg-accent hover:text-accent-foreground',
      )}
    >
      {label}
      {count !== undefined && (
        <span
          className={cn(
            'inline-flex min-w-[1.25rem] items-center justify-center rounded-full px-1 text-[11px] leading-4 font-semibold',
            active ? 'bg-primary-foreground/20' : 'bg-muted text-foreground',
          )}
        >
          {count}
        </span>
      )}
    </button>
  );
}
