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
  // Never a titlebar icon; present to keep the map total.
  debug: Info,
};

const kindButtonClassName: Record<AppNotificationKind, string> = {
  error: 'text-destructive hover:bg-destructive/10',
  warning: 'text-[oklch(0.58_0.15_65)] hover:bg-accent',
  info: 'text-muted-foreground hover:bg-accent hover:text-accent-foreground',
  success: 'text-[oklch(0.52_0.15_150)] hover:bg-accent',
  debug: 'text-muted-foreground hover:bg-accent hover:text-accent-foreground',
};

// TitlebarMessageCenter renders one icon per class with unread messages.
export function TitlebarMessageCenter(): React.ReactElement | null {
  const counts = useAppSelector(selectNotificationUnreadCounts);
  const historyCount = useAppSelector(selectNotificationHistoryCount);
  const [openFilter, setOpenFilter] = React.useState<AppNotificationKind | 'all' | null>(null);
  const visibleKinds = TITLEBAR_ICON_KINDS.filter((kind) => counts[kind] > 0);
  // Keep the history reachable once every class is read.
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
        {/* Fixed foreground/background so the badge contrasts with any icon colour. */}
        <span className="absolute -top-1.5 -right-1.5 inline-flex h-[1.125rem] min-w-[1.125rem] items-center justify-center rounded-full bg-foreground px-1 text-[11px] font-semibold leading-none text-background">
          {count > 9 ? '9+' : count}
        </span>
      </Button>
    </IconTooltip>
  );
}
