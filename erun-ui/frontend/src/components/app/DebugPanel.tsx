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

// DebugPanel is the Diagnostics console (issue #466): two separate,
// copyable diagnostic surfaces designed to be pasted into an error report.
//
//   - erun trace — the selected env's persistent trace log
//     (~/.erun/<tenant>/<env>/trace.log), written by erun itself at full
//     trace verbosity on every env-scoped invocation (always on, #508),
//     readable at any time — including for commands that ran before this
//     console was opened. Host file for local envs, in-pod file
//     (reachability-gated) for remote.
//   - UI trace — the in-app Redux action history (the packaged WebView has
//     no Redux DevTools).
//
// It replaces the old raw-PTY mirror, which rendered the active session's
// byte stream and turned the panel into ANSI gibberish whenever the AI tab's
// TUI was active. The console never reads PTY bytes at all now.

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

// useStickToBottom keeps the pane pinned to its newest line unless the user
// scrolled up to read.
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

// CopyReportButton assembles the one-click bug report (issue #514): app
// build, the selected env's identity/state, a fresh erun-trace read (so the
// report is current even between poll ticks), and the UI action history —
// one paste-ready block, available from either tab.
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

// useEnvTracePoll loads the selected env's persistent trace log and keeps
// it fresh while the pane is visible, so a running command's lines arrive
// without manual refresh.
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

// ErunTracePane renders the selected env's persistent trace log.
function ErunTracePane({ selection }: { selection: UISelection | null }): React.ReactElement {
  const tenant = selection?.tenant ?? '';
  const environment = selection?.environment ?? '';
  const { trace, refresh } = useEnvTracePoll(tenant, environment);
  const content = trace?.content ?? '';
  const { outputRef, handleScroll } = useStickToBottom(content);

  // The toolbar sits in its own grid row, outside the scroll region:
  // stick-to-bottom would otherwise scroll the actions out the top the
  // moment the log outgrows the pane — an affordance that exists but cannot
  // be seen does not exist (issue #514).
  return (
    <div className="grid min-h-0 grid-rows-[auto_minmax(0,1fr)]">
      <ErunTraceToolbar
        label={erunTraceLabel(tenant, environment, trace)}
        content={content}
        onRefresh={refresh}
      />
      <div
        ref={outputRef}
        onScroll={handleScroll}
        className="min-h-0 overflow-auto px-3 pb-2 font-mono text-[11px] leading-[1.35] text-[oklch(0.82_0_0)]"
        aria-label="erun trace output"
      >
        <ErunTraceBody tenant={tenant} environment={environment} trace={trace} />
      </div>
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
  onRefresh,
}: {
  label: string;
  content: string;
  onRefresh: () => void;
}): React.ReactElement {
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
      </span>
    </div>
  );
}

function ErunTraceBody({
  tenant,
  environment,
  trace,
}: {
  tenant: string;
  environment: string;
  trace: UIEnvTrace | null;
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
    return (
      <pre className="m-0 font-mono text-[11px] leading-[1.35] break-words whitespace-pre-wrap">
        {trace.content}
      </pre>
    );
  }
  return <p className="m-0 text-[oklch(0.6_0_0)]">{trace.reason ?? 'No trace available.'}</p>;
}

// UITracePane renders the Redux action history, polling the module buffer
// while visible (the buffer is deliberately outside Redux — see
// uiTraceBuffer.ts).
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
  // erun trace toolbar (issue #514).
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
