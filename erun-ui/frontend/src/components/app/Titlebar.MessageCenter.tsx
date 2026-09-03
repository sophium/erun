import { Button, IconTooltip } from 'erun-kit';
import { AlertCircle, AlertTriangle, CheckCircle2, History, Info } from 'lucide-react';
import * as React from 'react';

import { useAppSelector } from '@/app/hooks';
import { notificationKindLabel, TITLEBAR_ICON_KINDS } from '@/app/notificationCenter';
import { selectNotificationHistoryCount, selectNotificationUnreadCounts } from '@/app/selectors';
import type { AppNotificationKind } from '@/app/state';
import { TitlebarMessageCenterDialog } from '@/components/app/Titlebar.MessageCenter.Dialog';

const kindIcons: Record<AppNotificationKind, typeof AlertCircle> = {
  error: AlertCircle,
  warning: AlertTriangle,
  info: Info,
  success: CheckCircle2,
  // Never rendered as a titlebar icon (see TITLEBAR_ICON_KINDS) -- present so
  // this stays a total map over the kind union.
  debug: Info,
};

const kindButtonClassName: Record<AppNotificationKind, string> = {
  error: 'text-destructive hover:bg-destructive/10',
  warning: 'text-[oklch(0.58_0.15_65)] hover:bg-accent',
  info: 'text-muted-foreground hover:bg-accent hover:text-accent-foreground',
  success: 'text-[oklch(0.52_0.15_150)] hover:bg-accent',
  debug: 'text-muted-foreground hover:bg-accent hover:text-accent-foreground',
};

// TitlebarMessageCenter replaces the old single-pill notification display
// with one icon per class carrying an unread count -- a class
// with nothing unread renders no icon at all, so the titlebar stays quiet
// until there is something to review. Clicking an icon opens the dialog
// already filtered to that class; the dialog itself owns the full session
// history, the debug-visibility toggle, and every per-message action.
export function TitlebarMessageCenter(): React.ReactElement | null {
  const counts = useAppSelector(selectNotificationUnreadCounts);
  const historyCount = useAppSelector(selectNotificationHistoryCount);
  const [openFilter, setOpenFilter] = React.useState<AppNotificationKind | 'all' | null>(null);
  const visibleKinds = TITLEBAR_ICON_KINDS.filter((kind) => counts[kind] > 0);
  // Every class fully read but the session still has history -- render one
  // muted entry point rather than leaving that history unreachable (root
  // AGENTS.md "Smooth, Seamless, No Dead Ends": a capability with no way in
  // is a defect, and dismissal is meaningless if the dialog it feeds can
  // never be reopened).
  const showHistoryFallback = visibleKinds.length === 0 && historyCount > 0;

  return (
    <>
      {(visibleKinds.length > 0 || showHistoryFallback) && (
        <div className="pointer-events-auto flex items-center gap-1 [--wails-draggable:no-drag]">
          {visibleKinds.map((kind) => (
            <MessageCenterIconButton
              key={kind}
              kind={kind}
              count={counts[kind]}
              onClick={() => {
                setOpenFilter(kind);
              }}
            />
          ))}
          {showHistoryFallback && (
            <IconTooltip label="Message history">
              <Button
                type="button"
                variant="ghost"
                size="icon"
                className="size-7 flex-none rounded-[var(--radius)] border-0 bg-transparent text-muted-foreground [&_svg]:size-[17px] hover:bg-accent hover:text-accent-foreground"
                aria-label="Message history"
                onClick={() => {
                  setOpenFilter('all');
                }}
              >
                <History aria-hidden="true" />
              </Button>
            </IconTooltip>
          )}
        </div>
      )}
      <TitlebarMessageCenterDialog
        initialFilter={openFilter}
        onClose={() => {
          setOpenFilter(null);
        }}
      />
    </>
  );
}

function MessageCenterIconButton({
  kind,
  count,
  onClick,
}: {
  kind: AppNotificationKind;
  count: number;
  onClick: () => void;
}): React.ReactElement {
  const Icon = kindIcons[kind];
  const label = `${notificationKindLabel(kind)}: ${String(count)} unread`;
  return (
    <IconTooltip label={label}>
      <Button
        type="button"
        variant="ghost"
        size="icon"
        className={`relative size-7 flex-none rounded-[var(--radius)] border-0 bg-transparent [&_svg]:size-[17px] ${kindButtonClassName[kind]}`}
        aria-label={label}
        onClick={onClick}
      >
        <Icon aria-hidden="true" />
        {/* A badge tinted with the icon's own currentColor (e.g. red-on-red
            for an error icon) has almost no visual separation from the glyph
            it overlaps -- bg-foreground/text-background is fixed regardless
            of kind, so the badge always contrasts against the icon beneath
            it. The digit is the signal; this is only its container. */}
        <span className="absolute -top-1.5 -right-1.5 inline-flex h-[1.125rem] min-w-[1.125rem] items-center justify-center rounded-full bg-foreground px-1 text-[11px] font-semibold leading-none text-background">
          {count > 9 ? '9+' : count}
        </span>
      </Button>
    </IconTooltip>
  );
}
