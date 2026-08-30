import * as React from 'react';

// The type scale both sidebar hover cards (EnvHoverCard, OrchestratorHoverCard)
// render through -- extracted so the two cards cannot re-drift into their own
// per-row treatments the way they did before #1694 (six treatments in the env
// card, three in its value column alone). Exactly three treatments exist in
// this family, and every row goes through one of them:
//
//   - label/caption (`HOVER_CARD_CAPTION_CLASS`, 12px/text-xs, muted): the
//     `dt` in every HoverCardRow, and any secondary caption line under a
//     value (a usage reading's age, an environment's status/usage sub-line)
//     -- a caption is secondary information exactly like a label is, so it
//     shares the label's size rather than inventing its own.
//   - value (`HoverCardRow`'s `dd`, 14px/text-sm, the card's own base size):
//     every value's primary content. Nothing shrinks or grows a value's own
//     font-size; a value that is a literal identifier (a version, a branch)
//     may add `font-mono` on top of this same size -- mono is a face choice,
//     never a size choice. The prior bug was exactly a differently-sized
//     mono value (12px mono against 14px sans in the same column).
//   - badge (`HoverCardBadge`, 10px pill): the one deliberate exception,
//     reserved for the small border pill (Host/Local/Transient).
//
// `HoverCardTitle` reuses the value treatment's size (14px) rather than a
// fourth size/face combination -- it earns its emphasis by weight only
// (font-medium), the same way a row's own name can (see OrchestratorHoverCard
// environment names).
export const HOVER_CARD_CAPTION_CLASS = 'text-xs text-muted-foreground';

export function HoverCardRow({
  label,
  wide = false,
  children,
}: {
  label: string;
  // wide stacks the label above the value across both grid columns, for a
  // value that needs the card's full content width rather than sharing it
  // with the label column.
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
            ? 'col-span-2 min-w-0 break-words text-sm text-foreground'
            : 'min-w-0 break-words text-sm text-foreground'
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
      className="flex-none rounded-[calc(var(--radius)-4px)] border border-border px-1 py-px text-[10px] font-medium uppercase leading-none tracking-wide text-muted-foreground"
      aria-label={ariaLabel}
    >
      {children}
    </span>
  );
}

export function HoverCardTitle({ children }: { children: React.ReactNode }): React.ReactElement {
  return <span className="min-w-0 truncate text-sm font-medium">{children}</span>;
}

export function HoverCardMuted({ children }: { children: React.ReactNode }): React.ReactElement {
  return <span className="text-muted-foreground">{children}</span>;
}
