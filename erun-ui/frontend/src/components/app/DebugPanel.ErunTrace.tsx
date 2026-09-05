import { cn } from 'erun-kit';
import * as React from 'react';

import { useErunTraceBaseline } from '@/app/erunTraceBaseline';
import type { UIEnvTrace } from '@/types';

import { LoadEnvTrace } from '../../../wailsjs/go/main/App';
import { useStickToBottom } from './DebugPanel.hooks';
import { PrimaryPaneToolbar } from './DebugPanel.shared';

const ERUN_TRACE_POLL_MS = 2000;

function useEnvTracePoll(
  tenant: string,
  environment: string,
): { trace: UIEnvTrace | null; refresh: () => void } {
  const [trace, setTrace] = React.useState<UIEnvTrace | null>(null);
  const refresh = React.useCallback(() => {
    if (!tenant || !environment) {
      setTrace(null);
      return;
    }
    void LoadEnvTrace({ tenant, environment })
      .then((next) => {
        setTrace(next);
      })
      .catch(() => {
        setTrace(null);
      });
  }, [tenant, environment]);
  React.useEffect(() => {
    refresh();
    const timer = window.setInterval(refresh, ERUN_TRACE_POLL_MS);
    return () => {
      window.clearInterval(timer);
    };
  }, [refresh]);
  return { trace, refresh };
}

export function ErunTracePane({
  tenant,
  environment,
}: {
  tenant: string;
  environment: string;
}): React.ReactElement {
  const { trace, refresh } = useEnvTracePoll(tenant, environment);
  const content = trace?.content ?? '';
  // Clear is view-only: Copy and Copy report read the full content, so a
  // cleared view never truncates a bug report.
  const { cleared, rotatedOut, visibleContent, clear, showAll } = useErunTraceBaseline(
    `${tenant}/${environment}`,
    content,
  );
  const { outputRef, handleScroll } = useStickToBottom(visibleContent);

  // Toolbar and the cleared notice sit in their own grid rows, outside the
  // scroll region, so stick-to-bottom cannot scroll the actions out of view.
  return (
    <div
      className={cn(
        'grid min-h-0',
        cleared ? 'grid-rows-[auto_auto_minmax(0,1fr)]' : 'grid-rows-[auto_minmax(0,1fr)]',
      )}
    >
      <PrimaryPaneToolbar
        label={erunTraceLabel(tenant, environment, trace)}
        content={content}
        clear={{ canClear: content.trim().length > 0, onClear: clear }}
        onRefresh={refresh}
      />
      {cleared && <ClearedNotice rotatedOut={rotatedOut} onShowAll={showAll} />}
      <div
        ref={outputRef}
        onScroll={handleScroll}
        className="min-h-0 overflow-auto px-3 pb-2 font-mono text-[11px] leading-[1.35] text-[oklch(0.82_0_0)]"
        aria-label="erun trace output"
      >
        <ErunTraceBody
          tenant={tenant}
          environment={environment}
          trace={trace}
          visibleContent={visibleContent}
          cleared={cleared}
        />
      </div>
    </div>
  );
}

// ClearedNotice explains why earlier lines vanished after Clear and offers a
// one-click return to the full view: Clear only hides scrollback, the
// persistent log is untouched and always recoverable.
function ClearedNotice({
  rotatedOut,
  onShowAll,
}: {
  rotatedOut: boolean;
  onShowAll: () => void;
}): React.ReactElement {
  return (
    <div className="flex items-center justify-between gap-2 border-b border-[oklch(0.14_0_0)] px-3 py-1 text-[10px] text-[oklch(0.55_0_0)]">
      <span className="min-w-0 truncate">
        {rotatedOut
          ? 'Cleared entries have rotated out of the log — showing all.'
          : 'Showing entries since you cleared.'}
      </span>
      <button
        type="button"
        className="flex-none cursor-pointer rounded border-0 bg-transparent px-1 text-[10px] text-[oklch(0.7_0_0)] underline-offset-2 hover:underline"
        onClick={onShowAll}
      >
        Show all
      </button>
    </div>
  );
}

function erunTraceLabel(tenant: string, environment: string, trace: UIEnvTrace | null): string {
  if (!tenant || !environment) {
    return '';
  }
  return `${tenant} / ${environment} — ${trace?.path ?? ''}`;
}

function ErunTraceBody({
  tenant,
  environment,
  trace,
  visibleContent,
  cleared,
}: {
  tenant: string;
  environment: string;
  trace: UIEnvTrace | null;
  visibleContent: string;
  cleared: boolean;
}): React.ReactElement | null {
  if (!tenant || !environment) {
    return (
      <p className="m-0 text-[oklch(0.6_0_0)]">Select an environment to read its erun trace.</p>
    );
  }
  if (!trace) {
    return null;
  }
  if (trace.available) {
    // When cleared and nothing new has arrived yet, say so rather than
    // render a blank pane.
    return (
      <>
        <TraceNotice notice={trace.notice} />
        {cleared && visibleContent.length === 0 ? (
          <p className="m-0 text-[oklch(0.6_0_0)]">No new entries since you cleared.</p>
        ) : (
          <pre className="m-0 font-mono text-[11px] leading-[1.35] break-words whitespace-pre-wrap">
            {visibleContent}
          </pre>
        )}
      </>
    );
  }
  return (
    <>
      <TraceNotice notice={trace.notice} />
      <p className="m-0 text-[oklch(0.6_0_0)]">{trace.reason ?? 'No trace available.'}</p>
    </>
  );
}

// TraceNotice surfaces a non-fatal caveat (e.g. a remote env's in-pod trace
// could not be included); the content shown is still real.
function TraceNotice({ notice }: { notice?: string }): React.ReactElement | null {
  if (!notice) {
    return null;
  }
  return <p className="m-0 mb-1 text-[oklch(0.6_0_0)] italic">{notice}</p>;
}
