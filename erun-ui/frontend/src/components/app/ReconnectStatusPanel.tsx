import { AlertCircle, LoaderCircle, RefreshCw, X } from 'lucide-react';
import * as React from 'react';

import { useAppDispatch, useAppSelector } from '@/app/hooks';
import { reconnectCopy } from '@/app/reconnectCopy';
import { confirmReconnect, dismissReconnect } from '@/app/reviewThunks';
import { setSelected } from '@/app/slices/selectionSlice';
import { Button } from '@/components/ui/button';

// Pixel threshold (in scrollTop terms) for "user is at the bottom of the
// scroll buffer". A small fudge factor avoids us refusing to auto-scroll when
// the layout settles one or two pixels short of perfect alignment.
const STICK_TO_BOTTOM_PX = 16;

// ReconnectStatusPanel renders the in-flight ("running") and failure ("error")
// states of the reconnect flow as a non-modal floating panel anchored to the
// bottom-right of the app. It is scoped to the env recorded in
// state.review.reconnect.{tenant,environment} so other environments stay
// fully interactive while one is being reconnected.
//
// The line buffer renders as a scrollable monospace list; long redeploy
// output (e.g. docker build paths) wraps via [overflow-wrap:anywhere], and
// the panel auto-scrolls to the bottom on each new line unless the user has
// scrolled up to inspect history.
export function ReconnectStatusPanel(): React.ReactElement | null {
  const status = useAppSelector((state) => state.review.reconnect.status);
  const tenant = useAppSelector((state) => state.review.reconnect.tenant);
  const environment = useAppSelector((state) => state.review.reconnect.environment);
  const lines = useAppSelector((state) => state.review.reconnect.lines);
  const error = useAppSelector((state) => state.review.reconnect.error);

  if (status !== 'running' && status !== 'error') {
    return null;
  }

  const failed = status === 'error';
  const targetLabel = formatEnvLabel(tenant, environment);

  return (
    <div
      role="status"
      aria-live="polite"
      aria-label={
        failed
          ? `${reconnectCopy.errorStatusTitle} ${targetLabel}`
          : `${reconnectCopy.runningStatus} ${targetLabel}`
      }
      data-reconnect-status={status}
      className="fixed bottom-4 right-4 z-50 flex w-96 max-w-[calc(100vw-2rem)] flex-col overflow-hidden rounded-md border bg-background shadow-lg"
    >
      <ReconnectStatusHeader failed={failed} targetLabel={targetLabel} />
      <ReconnectStatusLines lines={lines} />
      {failed && (
        <ReconnectStatusErrorFooter error={error} tenant={tenant} environment={environment} />
      )}
    </div>
  );
}

function ReconnectStatusHeader({
  failed,
  targetLabel,
}: {
  failed: boolean;
  targetLabel: string;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  return (
    <header className="flex items-center gap-2 border-b px-3 py-2">
      {failed ? (
        <AlertCircle className="size-[16px] flex-none text-destructive" aria-hidden="true" />
      ) : (
        <LoaderCircle
          className="size-[16px] flex-none animate-spin text-muted-foreground"
          aria-hidden="true"
        />
      )}
      <div className="min-w-0 flex-1">
        <div className={failed ? 'text-sm font-medium text-destructive' : 'text-sm font-medium'}>
          {failed ? reconnectCopy.errorStatusTitle : reconnectCopy.runningStatus}
        </div>
        {targetLabel && (
          <div className="truncate text-xs text-muted-foreground" title={targetLabel}>
            {targetLabel}
          </div>
        )}
      </div>
      {failed && (
        <Button
          type="button"
          variant="ghost"
          size="icon"
          aria-label={reconnectCopy.dismiss}
          onClick={() => {
            dispatch(dismissReconnect());
          }}
        >
          <X aria-hidden="true" />
        </Button>
      )}
    </header>
  );
}

function ReconnectStatusLines({ lines }: { lines: readonly string[] }): React.ReactElement {
  const scrollerRef = React.useRef<HTMLDivElement>(null);
  // Tracks whether the user has scrolled away from the bottom. We only
  // auto-scroll on new output while the user is at the bottom — otherwise
  // the panel would yank them back every time the redeploy emits a line.
  const stickToBottomRef = React.useRef(true);

  const handleScroll = React.useCallback(() => {
    const node = scrollerRef.current;
    if (!node) {
      return;
    }
    const distanceFromBottom = node.scrollHeight - node.scrollTop - node.clientHeight;
    stickToBottomRef.current = distanceFromBottom <= STICK_TO_BOTTOM_PX;
  }, []);

  React.useEffect(() => {
    const node = scrollerRef.current;
    if (!node || !stickToBottomRef.current) {
      return;
    }
    node.scrollTop = node.scrollHeight;
  }, [lines.length]);

  return (
    <div
      ref={scrollerRef}
      onScroll={handleScroll}
      // max-h gives roughly 6 lines of monospace 12px text; min-h reserves
      // space for at least three lines so the panel never collapses to a
      // single-line strip when the buffer is short.
      className="min-h-[3.75rem] max-h-40 overflow-y-auto bg-muted/40 px-3 py-2 font-mono text-[12px] leading-[1.4]"
    >
      {lines.length === 0 ? (
        <div className="text-muted-foreground">{reconnectCopy.runningHint}</div>
      ) : (
        lines.map((line, idx) => (
          <div
            key={`${String(idx)}-${line}`}
            className="whitespace-pre-wrap [overflow-wrap:anywhere]"
          >
            {line}
          </div>
        ))
      )}
    </div>
  );
}

function ReconnectStatusErrorFooter({
  error,
  tenant,
  environment,
}: {
  error: string;
  tenant: string;
  environment: string;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const handleRetry = (): void => {
    if (tenant && environment) {
      dispatch(setSelected({ tenant, environment }));
    }
    void dispatch(confirmReconnect());
  };
  return (
    <div className="border-t px-3 py-2">
      {error && <div className="text-xs text-destructive [overflow-wrap:anywhere]">{error}</div>}
      <div className="mt-2 flex justify-end gap-2">
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => {
            dispatch(dismissReconnect());
          }}
        >
          {reconnectCopy.dismiss}
        </Button>
        <Button type="button" size="sm" onClick={handleRetry}>
          <RefreshCw aria-hidden="true" />
          {reconnectCopy.retry}
        </Button>
      </div>
    </div>
  );
}

function formatEnvLabel(tenant: string, environment: string): string {
  const t = tenant.trim();
  const e = environment.trim();
  if (t && e) {
    return `${t} / ${e}`;
  }
  return t || e;
}
