import { History } from 'lucide-react';
import * as React from 'react';

import { readError } from '@/app/errors';
import type { UILastStopEvent, UILastStopMarker, UISelection } from '@/types';

import { LoadStopHistory } from '../../../wailsjs/go/main/App';

// HistoryTab renders the last N auto-stop events for the selected
// env (Go-side cap is stopHistoryCap = 10, newest first). The tab is
// read-only — it pulls fresh data from disk every time the user opens
// it so a stop that fired while the dialog was open shows up the next
// time they switch back to this tab. Refresh is cheap (one small JSON
// read from local config) so we don't bother caching.
//
// The "why did my env stop?" question motivated this surface — issue
// #410 follow-up. Each row breaks out the per-marker idle/active
// state captured at grace-arm time, so the user can tell at a glance
// whether a recurring stop is caused by genuine inactivity or by a
// specific marker (e.g., ssh-proxy quiet but cli still active means
// the ssh-proxy activity reporter is misconfigured).
export function HistoryTab({
  selection,
  open,
}: {
  selection: UISelection | null;
  open: boolean;
}): React.ReactElement {
  const [history, setHistory] = React.useState<UILastStopEvent[]>([]);
  const [loading, setLoading] = React.useState(false);
  const [error, setError] = React.useState<string>('');

  React.useEffect(() => {
    if (!open || !selection) {
      return;
    }
    let cancelled = false;
    setLoading(true);
    setError('');
    LoadStopHistory(selection)
      .then((events) => {
        if (cancelled) return;
        setHistory(events);
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        setError(readError(err));
      })
      .finally(() => {
        if (cancelled) return;
        setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [open, selection]);

  if (!selection) {
    return <HistoryEmptyState message="Select an environment to see its auto-stop history." />;
  }
  if (loading) {
    return <HistoryEmptyState message="Loading auto-stop history…" />;
  }
  if (error) {
    return (
      <div
        role="alert"
        aria-live="polite"
        className="rounded-[var(--radius)] border border-destructive/40 bg-destructive/10 px-3 py-2 text-[13px] text-destructive"
      >
        Failed to load auto-stop history: {error}
      </div>
    );
  }
  if (history.length === 0) {
    return (
      <HistoryEmptyState message="No auto-stops recorded yet. The desktop logs each idle-timeout stop here so you can see why an env was paused." />
    );
  }
  return (
    <div className="flex flex-col gap-2" data-testid="manage-history-list">
      <p className="text-[13px] text-muted-foreground">
        Showing the {history.length} most recent auto-stops, newest first.
      </p>
      {history.map((event) => (
        <StopHistoryRow key={`${event.stoppedAt}-${event.reason}`} event={event} />
      ))}
    </div>
  );
}

function HistoryEmptyState({ message }: { message: string }): React.ReactElement {
  return (
    <div
      className="flex items-center gap-3 rounded-[var(--radius)] border border-border bg-muted/40 px-3 py-3 text-[13px] text-muted-foreground"
      data-testid="manage-history-empty"
    >
      <History className="size-4 shrink-0" aria-hidden="true" />
      <span>{message}</span>
    </div>
  );
}

function StopHistoryRow({ event }: { event: UILastStopEvent }): React.ReactElement {
  const when = formatStoppedAt(event.stoppedAt);
  const idleMarkers = (event.markers ?? []).filter((m) => m.idle);
  const activeMarkers = (event.markers ?? []).filter((m) => !m.idle);
  return (
    <article
      className="grid gap-1.5 rounded-[var(--radius)] border border-border bg-background px-3 py-2.5"
      data-testid="manage-history-row"
    >
      <header className="flex items-baseline justify-between gap-3 text-[13px]">
        <span className="font-medium text-foreground" data-testid="manage-history-row-when">
          {when}
        </span>
        <span className="text-[12px] text-muted-foreground">
          Grace {String(event.graceSeconds)}s
        </span>
      </header>
      <div className="text-[13px] text-foreground" data-testid="manage-history-row-reason">
        {event.reason || 'Auto-stop fired.'}
      </div>
      {idleMarkers.length > 0 && <MarkerList label="Idle markers" markers={idleMarkers} />}
      {activeMarkers.length > 0 && (
        <MarkerList label="Still-active markers" markers={activeMarkers} />
      )}
    </article>
  );
}

function MarkerList({
  label,
  markers,
}: {
  label: string;
  markers: UILastStopMarker[];
}): React.ReactElement {
  return (
    <div className="text-[12px] text-muted-foreground">
      <span className="font-medium">{label}:</span>{' '}
      {markers
        .map((m) => {
          const idleFor = m.secondsIdleFor ? ` (${formatSecondsAgo(m.secondsIdleFor)})` : '';
          return `${m.name}${idleFor}`;
        })
        .join(', ')}
    </div>
  );
}

function formatStoppedAt(timestamp: string): string {
  if (!timestamp) return 'Unknown time';
  const parsed = new Date(timestamp);
  if (Number.isNaN(parsed.getTime())) {
    return timestamp;
  }
  // ISO-style local time, e.g., "2026-05-31 12:34" — readable across
  // locales without burning the file or React tree on Intl.
  const year = parsed.getFullYear();
  const month = String(parsed.getMonth() + 1).padStart(2, '0');
  const day = String(parsed.getDate()).padStart(2, '0');
  const hour = String(parsed.getHours()).padStart(2, '0');
  const minute = String(parsed.getMinutes()).padStart(2, '0');
  return `${String(year)}-${month}-${day} ${hour}:${minute}`;
}

function formatSecondsAgo(seconds: number): string {
  if (seconds < 60) return `${String(seconds)}s idle`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${String(minutes)}m idle`;
  const hours = Math.floor(minutes / 60);
  return `${String(hours)}h idle`;
}
