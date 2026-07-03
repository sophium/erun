import { History } from 'lucide-react';
import * as React from 'react';

import { readError } from '@/app/errors';
import type { UIIdlePolicy, UILastStopEvent, UILastStopMarker, UISelection } from '@/types';

import { LoadStopHistory } from '../../../wailsjs/go/main/App';

// Source values shipped from EnvironmentStopHistoryEntry.Source.
// Kept here as constants (rather than an enum import) because the
// Go side ships them as strings and the matching renders are local
// to this component. Empty source ("") is the legacy fallback for
// rows written before the field existed.
const STOP_SOURCE_POD_MONITOR = 'pod-monitor';
const STOP_SOURCE_HOST_MANUAL = 'host-manual';

// HistoryTab renders the last N stop events for the selected env
// (Go-side cap is stopHistoryCap = 10, newest first). The tab is
// read-only — it pulls fresh data from disk every time the user
// opens it so a stop that fired while the dialog was open shows up
// the next time they switch back to this tab. Refresh is cheap
// (one MCP call against the in-pod tool) so we don't cache.
//
// The "why did my env stop?" question motivated this surface.
// Each row carries: the source (in-pod idle
// monitor vs. desktop manual stop), the resolved idle policy
// snapshot at fire time, the grace-armed timestamp alongside the
// AWS-stop timestamp, and the per-marker idle/active state captured
// at grace-arm time. Together that lets the user distinguish "I
// clicked Stop" from "the idle policy fired" from "a specific
// marker (e.g., ssh-proxy) was incorrectly quiet."
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
      <HistoryEmptyState message="No stops recorded yet. The desktop logs each idle-timeout stop and each manual Stop here so you can see why an env was paused." />
    );
  }
  return (
    <div className="flex flex-col gap-2" data-testid="manage-history-list">
      <p className="text-[13px] text-muted-foreground">
        Showing the {history.length} most recent stops, newest first.
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
  const stoppedAt = formatStoppedAt(event.stoppedAt);
  const armedAt = event.armedAt ? formatStoppedAt(event.armedAt) : '';
  const idleMarkers = (event.markers ?? []).filter((m) => m.idle);
  const activeMarkers = (event.markers ?? []).filter((m) => !m.idle);
  const sourceLabel = formatSourceLabel(event.source);
  const policyLine = formatPolicyLine(event.policy);
  const reason = formatReason(event);
  return (
    <article
      className="grid gap-1.5 rounded-[var(--radius)] border border-border bg-background px-3 py-2.5"
      data-testid="manage-history-row"
    >
      <header className="flex items-baseline justify-between gap-3 text-[13px]">
        <span className="font-medium text-foreground" data-testid="manage-history-row-when">
          {stoppedAt}
        </span>
        <span className="text-[12px] text-muted-foreground" data-testid="manage-history-row-source">
          {sourceLabel}
          {event.graceSeconds > 0 ? ` · Grace ${String(event.graceSeconds)}s` : ''}
        </span>
      </header>
      <div className="text-[13px] text-foreground" data-testid="manage-history-row-reason">
        {reason}
      </div>
      {armedAt && (
        <div className="text-[12px] text-muted-foreground" data-testid="manage-history-row-armed">
          Grace armed at {armedAt}, fired at {stoppedAt}.
        </div>
      )}
      {policyLine && (
        <div className="text-[12px] text-muted-foreground" data-testid="manage-history-row-policy">
          {policyLine}
        </div>
      )}
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

function formatSourceLabel(source: string | undefined): string {
  switch (source) {
    case STOP_SOURCE_POD_MONITOR:
      return 'In-pod idle monitor';
    case STOP_SOURCE_HOST_MANUAL:
      return 'Desktop manual stop';
    default:
      // Legacy entries written before the source field existed —
      // we still want a sensible row header rather than nothing.
      return 'Auto-stop';
  }
}

function formatReason(event: UILastStopEvent): string {
  const trimmed = event.reason.trim();
  if (trimmed) return trimmed;
  if (event.source === STOP_SOURCE_HOST_MANUAL) return 'Manual stop.';
  return 'Auto-stop fired.';
}

function formatPolicyLine(policy: UIIdlePolicy | undefined): string {
  if (!policy) return '';
  const parts: string[] = [];
  if (policy.timeoutSeconds > 0) {
    parts.push(`timeout ${formatDurationSeconds(policy.timeoutSeconds)}`);
  }
  const workingHours = (policy.workingHours ?? '').trim();
  if (workingHours) {
    parts.push(`working hours ${workingHours}`);
  }
  const timezone = (policy.timezone ?? '').trim();
  if (timezone) {
    parts.push(timezone);
  }
  if (parts.length === 0) return '';
  return `Policy: ${parts.join(', ')}`;
}

function formatDurationSeconds(seconds: number): string {
  if (seconds < 60) return `${String(seconds)}s`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) {
    const remainderSeconds = seconds - minutes * 60;
    return remainderSeconds === 0
      ? `${String(minutes)}m`
      : `${String(minutes)}m ${String(remainderSeconds)}s`;
  }
  const hours = Math.floor(minutes / 60);
  const remainderMinutes = minutes - hours * 60;
  return remainderMinutes === 0
    ? `${String(hours)}h`
    : `${String(hours)}h ${String(remainderMinutes)}m`;
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
