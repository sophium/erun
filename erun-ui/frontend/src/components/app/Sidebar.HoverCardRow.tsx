import * as React from 'react';

// Shared type and spacing contract for EnvHoverCard and OrchestratorHoverCard.
// TYPE: one 12px size and one sans face; elements differ by colour, weight and state only (a badge alone adds uppercase/tracking).
// Degraded (muted, reduced opacity) is for missing or stale data; alert (amber + icon) only for actionable states.
// SPACING: value stack gap-0.5, rows gap-y-2, zones a hairline border-t; never a fourth gap.
// The fixed 10ch label column keeps one left edge; widen this constant rather than a row's own column.
export const HOVER_CARD_GRID_CLASS =
  'grid grid-cols-[10ch_minmax(0,1fr)] items-baseline gap-x-3 gap-y-2';

// Spacing level 1: a value and its own caption.
export const HOVER_CARD_VALUE_STACK_CLASS = 'grid gap-0.5';

export const HOVER_CARD_VALUE_CLASS = 'text-[12px] text-foreground';
// HOVER_CARD_TRUNCATE_CLASS truncates an identifier to one line (full text in `title`).
// break-normal undoes the inherited break-words, which defeats truncate.
export const HOVER_CARD_TRUNCATE_CLASS = 'block truncate break-normal';
// HOVER_CARD_CLAMP_2_CLASS clamps unbounded text to two lines (full text in `title`).
// No `block`: Tailwind emits it after line-clamp and it would undo the clamp.
export const HOVER_CARD_CLAMP_2_CLASS = 'line-clamp-2 break-normal';
export const HOVER_CARD_CAPTION_SIZE_CLASS = 'text-[12px]';
export const HOVER_CARD_CAPTION_CLASS = `${HOVER_CARD_CAPTION_SIZE_CLASS} text-muted-foreground`;
// HOVER_CARD_CAPTION_DEGRADED_CLASS is the degraded role where HoverCardMuted's ambient size is not inherited.
export const HOVER_CARD_CAPTION_DEGRADED_CLASS = `${HOVER_CARD_CAPTION_SIZE_CLASS} text-muted-foreground/70`;
// HOVER_CARD_ALERT_CLASS is the alert colour; always paired with a TriangleAlert icon.
export const HOVER_CARD_ALERT_CLASS = 'text-amber-700 dark:text-amber-400';

export function HoverCardRow({
  label,
  wide = false,
  children,
}: {
  label: string;
  // Stacks the label above a value that needs the full width.
  wide?: boolean;
  children: React.ReactNode;
}): React.ReactElement {
  return (
    <>
      <dt className={wide ? `col-span-2 ${HOVER_CARD_CAPTION_CLASS}` : HOVER_CARD_CAPTION_CLASS}>
        {label}
      </dt>
      <dd
        className={
          wide
            ? `col-span-2 min-w-0 break-words ${HOVER_CARD_VALUE_CLASS}`
            : `min-w-0 break-words ${HOVER_CARD_VALUE_CLASS}`
        }
      >
        {children}
      </dd>
    </>
  );
}

export function HoverCardBadge({
  children,
  ariaLabel,
}: {
  children: React.ReactNode;
  ariaLabel?: string;
}): React.ReactElement {
  return (
    <span
      className="flex-none rounded-[calc(var(--radius)-4px)] border border-border px-1 py-px text-[12px] font-medium uppercase leading-none tracking-wide text-muted-foreground"
      aria-label={ariaLabel}
    >
      {children}
    </span>
  );
}

export function HoverCardTitle({ children }: { children: React.ReactNode }): React.ReactElement {
  return <span className="min-w-0 truncate text-[12px] font-semibold">{children}</span>;
}

// HoverCardMuted is the degraded role; it overrides colour only, inheriting size.
export function HoverCardMuted({ children }: { children: React.ReactNode }): React.ReactElement {
  return <span className="text-muted-foreground/70">{children}</span>;
}
