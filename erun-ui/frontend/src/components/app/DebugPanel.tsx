import {
  CheckCircle2,
  ChevronDown,
  ChevronUp,
  ClipboardList,
  Copy,
  RefreshCw,
  Trash2,
} from 'lucide-react';
import * as React from 'react';

import { stateApi } from '@/app/api/stateApi';
import { formatDiagnosticsReport } from '@/app/diagnosticsReport';
import { useErunTraceBaseline } from '@/app/erunTraceBaseline';
import { useAppDispatch, useAppSelector } from '@/app/hooks';
import { setDebugOpen, startDebugResize } from '@/app/layoutThunks';
import {
  clearUITrace,
  formatUITrace,
  uiTraceEntries,
  type UITraceEntry,
  uiTraceGeneration,
} from '@/app/uiTraceBuffer';
import { selectionKey } from '@/app/versionSuggestions';
import { ResizeHandle } from '@/components/app/ResizeHandle';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import type { UIEnvTrace, UISelection } from '@/types';

import { LoadEnvTrace } from '../../../wailsjs/go/main/App';
import { ClipboardSetText } from '../../../wailsjs/runtime/runtime';

// DebugPanel is the Diagnostics console: two copyable surfaces meant to be
// pasted into a bug report. The erun trace is always-on and covers commands
// that ran before the console was opened (remote envs are reachability-gated);
// the UI trace exists because the packaged WebView has no Redux DevTools.

const debugSplitterClassName =
  'relative cursor-row-resize bg-[oklch(0.06_0_0)] before:absolute before:left-0 before:right-0 before:top-1 before:h-px before:bg-transparent before:transition-colors hover:before:bg-[oklch(0.36_0_0)] [.is-resizing-debug_&]:before:bg-[oklch(0.46_0_0)]';

const ERUN_TRACE_POLL_MS = 2000;

type DiagnosticsTab = 'erun' | 'ui';

export function DebugPanel({ open }: { open: boolean }): React.ReactElement {
  const dispatch = useAppDispatch();
  const [tab, setTab] = React.useState<DiagnosticsTab>('erun');
  const selection = useAppSelector((state) => state.selection.selected);

  return (
    <section
      className={cn(
        'grid min-h-0 border-t border-[oklch(0.26_0_0)] bg-[oklch(0.06_0_0)] text-[oklch(0.86_0_0)]',
        open ? 'grid-rows-[6px_34px_minmax(0,1fr)]' : 'grid-rows-[34px]',
      )}
    >
      {open && (
        <ResizeHandle
          className={debugSplitterClassName}
          orientation="horizontal"
          label="Resize diagnostics panel"
          onMouseDown={(event) => {
            dispatch(startDebugResize(event));
          }}
        />
      )}
      <div className="flex h-[34px] items-center justify-between gap-2 border-b border-[oklch(0.18_0_0)] px-3">
        <button
          type="button"
          className="flex min-w-0 items-center gap-2 border-0 bg-transparent p-0 text-xs font-medium tracking-normal text-[oklch(0.76_0_0)]"
          onClick={() => {
            dispatch(setDebugOpen(!open));
          }}
        >
          {open ? (
            <ChevronDown className="size-4" aria-hidden="true" />
          ) : (
            <ChevronUp className="size-4" aria-hidden="true" />
          )}
          <span>Diagnostics</span>
          <span className="text-[11px] font-normal text-[oklch(0.56_0_0)]">
            {open ? 'erun trace + UI trace' : 'collapsed'}
          </span>
        </button>
        {open && (
          <div className="flex items-center gap-2">
            <CopyReportButton selection={selection} />
            <div
              className="flex items-center gap-1"
              role="tablist"
              aria-label="Diagnostics sections"
            >
              <DiagnosticsTabButton
                current={tab}
                value="erun"
                label="erun trace"
                onSelect={setTab}
              />
              <DiagnosticsTabButton current={tab} value="ui" label="UI trace" onSelect={setTab} />
            </div>
          </div>
        )}
      </div>
      {open && tab === 'erun' && <ErunTracePane selection={selection} />}
      {open && tab === 'ui' && <UITracePane />}
    </section>
  );
}

function DiagnosticsTabButton({
  current,
  value,
  label,
  onSelect,
}: {
  current: DiagnosticsTab;
  value: DiagnosticsTab;
  label: string;
  onSelect: (tab: DiagnosticsTab) => void;
}): React.ReactElement {
  const active = current === value;
  return (
    <button
      type="button"
      role="tab"
      aria-selected={active}
      className={cn(
        'h-6 cursor-pointer rounded border-0 px-2 text-[11px]',
        active
          ? 'bg-[oklch(0.2_0_0)] text-[oklch(0.9_0_0)]'
          : 'bg-transparent text-[oklch(0.62_0_0)] hover:text-[oklch(0.82_0_0)]',
      )}
      onClick={() => {
        onSelect(value);
      }}
    >
      {label}
    </button>
  );
}

function useStickToBottom(content: string): {
  outputRef: React.RefObject<HTMLDivElement | null>;
  handleScroll: React.UIEventHandler<HTMLDivElement>;
} {
  const outputRef = React.useRef<HTMLDivElement>(null);
  const stuckToBottomRef = React.useRef(true);
  const handleScroll = React.useCallback(() => {
    const el = outputRef.current;
    if (!el) {
      return;
    }
    stuckToBottomRef.current = el.scrollHeight - el.scrollTop - el.clientHeight <= 4;
  }, []);
  React.useEffect(() => {
    const el = outputRef.current;
    if (el && stuckToBottomRef.current) {
      el.scrollTop = el.scrollHeight;
    }
  }, [content]);
  return { outputRef, handleScroll };
}

function useCopyAction(text: string): { copyStatus: string; copy: () => void } {
  const [copyStatus, setCopyStatus] = React.useState('');
  const copy = React.useCallback(() => {
    if (!text.trim()) {
      return;
    }
    void ClipboardSetText(text)
      .then(() => {
        setCopyStatus('Copied');
        window.setTimeout(() => {
          setCopyStatus('');
        }, 1400);
      })
      .catch(() => {
        setCopyStatus('Copy failed');
      });
  }, [text]);
  return { copyStatus, copy };
}

function CopyButton({
  copyStatus,
  disabled,
  onCopy,
}: {
  copyStatus: string;
  disabled: boolean;
  onCopy: () => void;
}): React.ReactElement {
  return (
    <Button
      className="h-6 px-2 text-[11px] [&_svg]:size-3.5"
      type="button"
      variant="ghost"
      size="sm"
      disabled={disabled}
      onClick={onCopy}
    >
      {copyStatus === 'Copied' ? <CheckCircle2 aria-hidden="true" /> : <Copy aria-hidden="true" />}
      {copyStatus || 'Copy'}
    </Button>
  );
}

// CopyReportButton produces the one-click bug report. It re-reads the erun
// trace fresh rather than reusing the polled copy, so the report stays
// current even between poll ticks.
function CopyReportButton({ selection }: { selection: UISelection | null }): React.ReactElement {
  const selectInitialState = React.useMemo(
    () => stateApi.endpoints.getInitialState.select(undefined),
    [],
  );
  const build = useAppSelector((state) => selectInitialState(state).data?.build ?? null);
  const environment = useAppSelector((state) => {
    if (!selection) {
      return null;
    }
    const tenant = state.tenants.tenants.find((entry) => entry.name === selection.tenant);
    return tenant?.environments.find((env) => env.name === selection.environment) ?? null;
  });
  const envStatus = useAppSelector((state) =>
    selection ? (state.envStatus.statusByEnv[selectionKey(selection)] ?? '') : '',
  );
  const [status, setStatus] = React.useState('');

  const copyReport = React.useCallback(() => {
    const assemble = async (): Promise<void> => {
      let trace: UIEnvTrace | null = null;
      if (selection) {
        trace = await LoadEnvTrace({
          tenant: selection.tenant,
          environment: selection.environment,
        }).catch(() => null);
      }
      const report = formatDiagnosticsReport({
        generatedAt: new Date().toISOString(),
        build,
        selection,
        environment,
        envStatus,
        trace,
        uiTrace: uiTraceEntries(),
      });
      await ClipboardSetText(report);
    };
    assemble()
      .then(() => {
        setStatus('Copied');
        window.setTimeout(() => {
          setStatus('');
        }, 1400);
      })
      .catch(() => {
        setStatus('Copy failed');
      });
  }, [selection, build, environment, envStatus]);

  return (
    <Button
      className="h-6 px-2 text-[11px] [&_svg]:size-3.5"
      type="button"
      variant="ghost"
      size="sm"
      onClick={copyReport}
    >
      {status === 'Copied' ? (
        <CheckCircle2 aria-hidden="true" />
      ) : (
        <ClipboardList aria-hidden="true" />
      )}
      {status || 'Copy report'}
    </Button>
  );
}

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

function ErunTracePane({ selection }: { selection: UISelection | null }): React.ReactElement {
  const tenant = selection?.tenant ?? '';
  const environment = selection?.environment ?? '';
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
      <ErunTraceToolbar
        label={erunTraceLabel(tenant, environment, trace)}
        content={content}
        canClear={content.trim().length > 0}
        onRefresh={refresh}
        onClear={clear}
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

function ErunTraceToolbar({
  label,
  content,
  canClear,
  onRefresh,
  onClear,
}: {
  label: string;
  content: string;
  canClear: boolean;
  onRefresh: () => void;
  onClear: () => void;
}): React.ReactElement {
  // Copy reads the full content, never the cleared view — a baselined view
  // must never produce a truncated bug report.
  const { copyStatus, copy } = useCopyAction(content);
  return (
    <div className="flex items-center justify-between gap-2 px-3 pt-1.5 pb-1">
      <span className="min-w-0 truncate text-[10px] text-[oklch(0.5_0_0)]">{label}</span>
      <span className="flex flex-none items-center gap-1">
        <Button
          className="h-6 px-2 text-[11px] [&_svg]:size-3.5"
          type="button"
          variant="ghost"
          size="sm"
          onClick={onRefresh}
        >
          <RefreshCw aria-hidden="true" />
          Refresh
        </Button>
        <CopyButton copyStatus={copyStatus} disabled={!content.trim()} onCopy={copy} />
        <Button
          className="h-6 px-2 text-[11px] [&_svg]:size-3.5"
          type="button"
          variant="ghost"
          size="sm"
          disabled={!canClear}
          onClick={onClear}
        >
          <Trash2 aria-hidden="true" />
          Clear
        </Button>
      </span>
    </div>
  );
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

// The UI trace buffer lives outside Redux (see uiTraceBuffer.ts), so this
// pane polls it instead of subscribing to a selector.
function UITracePane(): React.ReactElement {
  const [entries, setEntries] = React.useState<UITraceEntry[]>(() => uiTraceEntries());
  const generationRef = React.useRef(uiTraceGeneration());

  React.useEffect(() => {
    const timer = window.setInterval(() => {
      const generation = uiTraceGeneration();
      if (generation !== generationRef.current) {
        generationRef.current = generation;
        setEntries(uiTraceEntries());
      }
    }, 500);
    return () => {
      window.clearInterval(timer);
    };
  }, []);

  const text = formatUITrace(entries);
  const { outputRef, handleScroll } = useStickToBottom(text);
  const { copyStatus, copy } = useCopyAction(text);

  // Actions are pinned outside the scroll region — same rationale as the
  // erun trace toolbar.
  return (
    <div className="grid min-h-0 grid-rows-[auto_minmax(0,1fr)]">
      <div className="flex items-center justify-end gap-1 px-3 pt-1.5 pb-1">
        <CopyButton copyStatus={copyStatus} disabled={!text.trim()} onCopy={copy} />
        <Button
          className="h-6 px-2 text-[11px] [&_svg]:size-3.5"
          type="button"
          variant="ghost"
          size="sm"
          onClick={() => {
            clearUITrace();
            setEntries([]);
          }}
        >
          <Trash2 aria-hidden="true" />
          Clear
        </Button>
      </div>
      <div
        ref={outputRef}
        onScroll={handleScroll}
        className="min-h-0 overflow-auto px-3 pb-2 font-mono text-[11px] leading-[1.35] text-[oklch(0.82_0_0)]"
        aria-label="UI trace output"
      >
        {entries.length === 0 ? (
          <p className="m-0 text-[oklch(0.6_0_0)]">No UI activity recorded yet.</p>
        ) : (
          <pre className="m-0 font-mono text-[11px] leading-[1.35] break-words whitespace-pre-wrap">
            {text}
          </pre>
        )}
      </div>
    </div>
  );
}
