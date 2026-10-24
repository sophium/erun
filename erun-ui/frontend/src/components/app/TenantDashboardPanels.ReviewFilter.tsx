import { cn } from 'erun-kit';
import * as React from 'react';

import { reviewStatuses } from '../../app/reviewDetailState';

// ReviewFilterSegmentedControl groups the Mine and Waiting-on-me chips; each
// still toggles independently, since a review can be both.
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

// ReviewStatusFilterControl narrows the list by status. All chips off means
// show everything, so the panel can never be stranded empty.
export function ReviewStatusFilterControl({
  statuses,
  counts,
  onToggle,
}: {
  statuses: string[];
  counts: Record<string, number>;
  onToggle: (status: string) => void;
}): React.ReactElement {
  return (
    <div
      role="group"
      aria-label="Filter reviews by status"
      className="flex flex-wrap items-center gap-1 rounded-[var(--radius)] border border-input bg-background p-1 text-[13px]"
    >
      {reviewStatuses.map((status) => (
        <ReviewFilterToggle
          key={status}
          label={status}
          count={counts[status] ?? 0}
          active={statuses.includes(status)}
          onClick={() => {
            onToggle(status);
          }}
        />
      ))}
    </div>
  );
}

// ReviewFilterToggle keeps the count in the accessible name so screen readers hear it.
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
