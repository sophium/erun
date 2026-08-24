import { cn, ResizeHandle } from 'erun-kit';
import { ChevronDown, ChevronUp } from 'lucide-react';
import * as React from 'react';

import { useAppDispatch, useAppSelector } from '@/app/hooks';
import { setDebugOpen, startDebugResize } from '@/app/layoutThunks';
import { type DiagnosticsContext, selectDiagnosticsContext } from '@/app/selectors';

import { AppLogPane, OrchestratorPane } from './DebugPanel.AppLog';
import { ErunTracePane } from './DebugPanel.ErunTrace';
import { CopyReportButton, ReportIssueButton } from './DebugPanel.Report';
import { UITracePane } from './DebugPanel.UITrace';

// DebugPanel is the Diagnostics console: it shows whatever evidence the
// current DiagnosticsContext resolves to (an orchestrator's own state, the
// selected environment's, or the desktop app's own log when neither is
// active) rather than being hardwired to an environment selection — an
// orchestrator session used to leave this panel reading "environment: none
// selected" with no trace at all (#1241). Two copyable surfaces plus a report
// button meant to be filed straight into the tracker: the primary pane is
// always-on evidence for the active context, the UI trace exists because the
// packaged WebView has no Redux DevTools and stays available in every
// context.

const debugSplitterClassName =
  'relative cursor-row-resize bg-[oklch(0.06_0_0)] before:absolute before:left-0 before:right-0 before:top-1 before:h-px before:bg-transparent before:transition-colors hover:before:bg-[oklch(0.36_0_0)] [.is-resizing-debug_&]:before:bg-[oklch(0.46_0_0)]';

type DiagnosticsTab = 'primary' | 'ui';

function primaryTabLabel(context: DiagnosticsContext): string {
  switch (context.kind) {
    case 'environment':
      return 'erun trace';
    case 'orchestrator':
      return 'orchestrator';
    case 'app':
      return 'app log';
  }
}

function PrimaryPane({ context }: { context: DiagnosticsContext }): React.ReactElement {
  switch (context.kind) {
    case 'environment':
      return <ErunTracePane tenant={context.tenant} environment={context.environment} />;
    case 'orchestrator':
      return <OrchestratorPane orchestrator={context.orchestrator} />;
    case 'app':
      return <AppLogPane label="app log" />;
  }
}

export function DebugPanel({ open }: { open: boolean }): React.ReactElement {
  const dispatch = useAppDispatch();
  const [tab, setTab] = React.useState<DiagnosticsTab>('primary');
  const context = useAppSelector(selectDiagnosticsContext);
  const primaryLabel = primaryTabLabel(context);

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
            {open ? `${primaryLabel} + UI trace` : 'collapsed'}
          </span>
        </button>
        {open && (
          <div className="flex items-center gap-2">
            <CopyReportButton context={context} />
            <ReportIssueButton context={context} />
            <div
              className="flex items-center gap-1"
              role="tablist"
              aria-label="Diagnostics sections"
            >
              <DiagnosticsTabButton
                current={tab}
                value="primary"
                label={primaryLabel}
                onSelect={setTab}
              />
              <DiagnosticsTabButton current={tab} value="ui" label="UI trace" onSelect={setTab} />
            </div>
          </div>
        )}
      </div>
      {open && tab === 'primary' && <PrimaryPane context={context} />}
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
