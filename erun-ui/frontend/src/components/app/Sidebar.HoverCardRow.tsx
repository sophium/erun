import * as React from 'react';

// The type and layout contract both sidebar hover cards (EnvHoverCard,
// OrchestratorHoverCard) render through, extracted so the two cards cannot
// re-drift into their own per-row treatments (before this file existed, the
// env card alone carried six different size/face/weight combinations and two
// row layouts). This file states the invariant as a prohibition, not an
// enumeration -- an enumeration is what drifted last time.
//
// TYPE: one size, one face, always. A card element is distinguished from
// another by colour, weight and state -- never by size or face. Concretely:
//
//   - title (`HoverCardTitle`): 10px, `text-foreground`, weight 600 -- the
//     card's only static chrome above the row grid.
//   - label (`HOVER_CARD_CAPTION_CLASS`, every `dt`): 10px, muted, weight 400.
//   - value (`HOVER_CARD_VALUE_CLASS`, every `dd`'s primary content): 10px,
//     `text-foreground`, weight 400. Never `font-mono` -- a literal
//     identifier (a version, a branch) still renders in the shared sans face;
//     `tabular-nums` on the row grid (not per value) keeps digits aligned
//     without a face change.
//   - caption (`HOVER_CARD_CAPTION_CLASS`, a secondary line under a value:
//     a reading's age, a node's power state): the same treatment as a label.
//     What separates a caption from a label is position, not type -- a label
//     sits in the label column, a caption sits beneath its value.
//   - degraded (`HoverCardMuted` / `HOVER_CARD_CAPTION_DEGRADED_CLASS`): a
//     dynamic value or caption that is missing, unmeasurable or stale --
//     "Not yet observed.", "Resolving…", a stale usage reading. Muted plus
//     reduced opacity, same size. This is not a warning: nothing the operator
//     did caused it and no action follows from it, so it recedes rather than
//     alarms. (Also why a stale or unreadable usage figure must not gain
//     visual confidence it does not have -- see UsageState in
//     Sidebar.EnvHoverCard.tsx.)
//   - alert (amber + `<TriangleAlert>`, always icon-paired per WCAG 1.4.1 and
//     `StatusDotGlyph`'s own rule): reserved for a state that names something
//     actionable -- a runtime-image line mismatch, a stopped node. Never used
//     for "this number could not be measured", which is `degraded` instead.
//   - badge (`HoverCardBadge`): 10px, muted, weight 500, `uppercase` +
//     `tracking-wide` -- the one deliberate exception that spends two extra
//     axes, reserved for the small bordered pill (Host/Local/Transient).
//
// Removed entirely: `text-sm`, `text-xs`, `font-mono`, and any per-element
// size or face decision. Adding a new element to either card means picking
// one of the roles above, not inventing a new size/face pairing.
//
// SPACING: three levels, each with one job. Extending a card means reusing
// one of these three, never a fourth ad hoc gap value.
//
//   1. Intra-value (`HOVER_CARD_VALUE_STACK_CLASS`, `gap-0.5` / 2px): stacks a
//      value with its own caption or warning line underneath it. These lines
//      are one fact read together (a version and its release-line caption, a
//      node label and its power state) -- the tightest gap in the card.
//   2. Inter-row (`HOVER_CARD_GRID_CLASS`'s `gap-y-2` / 8px): separates one
//      row's fact from the next row's fact within a zone. Loose enough that
//      stacked value+caption pairs read as units instead of a single ladder
//      of equally-spaced lines -- the defect this spacing level guards
//      against.
//   3. Inter-zone (a hairline `border-t` plus its own padding step, applied by
//      the card composing more than one `dl` against this shared grid class):
//      separates a card's stable-identity rows from its live-state rows, so a
//      conditional row (Erun version, Line mismatch) changes only its own
//      zone's height rather than the whole card's rhythm. `EnvHoverCard`'s two
//      `dl`s are the only place this level is used today; a card with only one
//      zone (`OrchestratorHoverCard`) skips it rather than inventing a
//      one-zone version of it.
//
// HOVER_CARD_GRID_CLASS's `13ch` label column is fixed width, sized for the
// longest label across both cards ("Line mismatch", "Environments"), so every
// row shares one left edge regardless of which conditional rows are present.
// A label longer than that widens this one constant; it does not grow its own
// column.
export const HOVER_CARD_GRID_CLASS =
  'grid grid-cols-[13ch_minmax(0,1fr)] items-baseline gap-x-3 gap-y-2';

// HOVER_CARD_VALUE_STACK_CLASS is spacing level 1 (see above): a value and its
// own caption or warning line, read as one fact.
export const HOVER_CARD_VALUE_STACK_CLASS = 'grid gap-0.5';

export const HOVER_CARD_VALUE_CLASS = 'text-[10px] text-foreground';
// HOVER_CARD_TRUNCATE_CLASS is for a value that is a literal identifier
// (a version, a branch, a node label) truncated to one line with the full
// text in `title` (Phase 1's "truncate identifiers, don't wrap them"). Every
// `dd` already carries `break-words` (HoverCardRow) for the rows that
// genuinely need to wrap (prose like Activity, warnings) -- `overflow-wrap`
// is an inherited property, and it wins over `truncate`'s own `nowrap` in at
// least one tested engine for a single unbreakable token, which silently
// turns a "truncate to one line" identifier back into a two-line wrap.
// `break-normal` resets that inheritance for this element specifically.
export const HOVER_CARD_TRUNCATE_CLASS = 'block truncate break-normal';
export const HOVER_CARD_CAPTION_SIZE_CLASS = 'text-[10px]';
export const HOVER_CARD_CAPTION_CLASS = `${HOVER_CARD_CAPTION_SIZE_CLASS} text-muted-foreground`;
// HOVER_CARD_CAPTION_DEGRADED_CLASS is the caption-sized form of the degraded
// role (see TYPE above) for a spot that does not inherit `HoverCardMuted`'s
// ambient size -- e.g. a caption nested inside a row that already declared its
// own size for a sibling branch. Prefer `HoverCardMuted` when the surrounding
// element already sets the 10px size.
export const HOVER_CARD_CAPTION_DEGRADED_CLASS = `${HOVER_CARD_CAPTION_SIZE_CLASS} text-muted-foreground/70`;
// HOVER_CARD_ALERT_CLASS is the shared colour half of the alert role (see TYPE
// above) -- always paired with a `<TriangleAlert aria-hidden>` at the call
// site, never used alone. One declaration so the two cards' warnings (line
// mismatch / stopped node here, restart-required / nudge-cap in
// OrchestratorHoverCard) cannot drift into different amber shades or lose
// their dark-mode variant the way they had before this shared class existed.
export const HOVER_CARD_ALERT_CLASS = 'text-amber-700 dark:text-amber-400';

export function HoverCardRow({
  label,
  wide = false,
  children,
}: {
  label: string;
  // wide stacks the label above the value across both grid columns, for a
  // value that needs the card's full content width rather than sharing it
  // with the label column. Retired from EnvHoverCard (the type change that
  // removed `font-mono` freed enough width that every one of its rows fits
  // the fixed label column); still used by OrchestratorHoverCard's
  // "Environments" row, which genuinely needs the full width for a busy
  // detail like "held by gradle-build".
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
      className="flex-none rounded-[calc(var(--radius)-4px)] border border-border px-1 py-px text-[10px] font-medium uppercase leading-none tracking-wide text-muted-foreground"
      aria-label={ariaLabel}
    >
      {children}
    </span>
  );
}

export function HoverCardTitle({ children }: { children: React.ReactNode }): React.ReactElement {
  return <span className="min-w-0 truncate text-[10px] font-semibold">{children}</span>;
}

// HoverCardMuted is the degraded role (see TYPE above): a dynamic value or
// caption that is missing, unmeasurable or stale. Reduced opacity on top of
// the muted colour, at whatever size the surrounding element already set --
// most callers nest it inside a `dd` that already declares `HOVER_CARD_VALUE_CLASS`'s
// 10px, so this only needs to override colour.
export function HoverCardMuted({ children }: { children: React.ReactNode }): React.ReactElement {
  return <span className="text-muted-foreground/70">{children}</span>;
}
