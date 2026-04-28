import * as React from 'react';
import { ChevronDown, ChevronUp, Trash2 } from 'lucide-react';

import { ERunUIController } from '@/app/ERunUIController';
import { useControllerState } from '@/app/useControllerState';
import { EnvironmentDialogView } from '@/components/app/EnvironmentDialogView';
import { GlobalConfigDialogView } from '@/components/app/GlobalConfigDialogView';
import { ManageDialogView } from '@/components/app/ManageDialogView';
import { ReviewPanel } from '@/components/app/ReviewPanel';
import { Sidebar } from '@/components/app/Sidebar';
import { TenantDialogView } from '@/components/app/TenantDialogView';
import { Titlebar } from '@/components/app/Titlebar';
import { Button } from '@/components/ui/button';
import { TooltipProvider } from '@/components/ui/tooltip';
import { cn } from '@/lib/utils';

const splitterClassName =
  'relative cursor-col-resize bg-transparent before:absolute before:top-0 before:bottom-0 before:left-1 before:w-px before:bg-transparent before:transition-colors hover:before:bg-border [.is-resizing_&]:before:bg-border';

const reviewSplitterClassName =
  'relative cursor-col-resize border-l bg-background before:absolute before:top-0 before:bottom-0 before:left-1 before:w-px before:bg-transparent before:transition-colors hover:before:bg-border [.is-resizing-review_&]:before:bg-border';

const debugSplitterClassName =
  'relative cursor-row-resize bg-[oklch(0.06_0_0)] before:absolute before:left-0 before:right-0 before:top-1 before:h-px before:bg-transparent before:transition-colors hover:before:bg-[oklch(0.36_0_0)] [.is-resizing-debug_&]:before:bg-[oklch(0.46_0_0)]';

export function App(): React.ReactElement {
  const controller = React.useMemo(() => new ERunUIController(), []);
  const state = useControllerState(controller);
  const terminalRootRef = React.useRef<HTMLDivElement>(null);
  const terminalPaneRef = React.useRef<HTMLElement>(null);
  const reviewViewRef = React.useRef<HTMLElement>(null);
  const reviewMainRef = React.useRef<HTMLDivElement>(null);
  const diffListRef = React.useRef<HTMLDivElement>(null);

  React.useEffect(() => {
    if (!terminalRootRef.current || !terminalPaneRef.current || !reviewViewRef.current || !reviewMainRef.current || !diffListRef.current) {
      return undefined;
    }
    return controller.mount({
      terminalRoot: terminalRootRef.current,
      terminalPane: terminalPaneRef.current,
      reviewView: reviewViewRef.current,
      reviewMain: reviewMainRef.current,
      diffList: diffListRef.current,
    });
  }, [controller]);

  return (
    <TooltipProvider>
      <div className="grid h-full w-full grid-rows-[52px_minmax(0,1fr)] bg-background">
        <Titlebar controller={controller} state={state} />
        <div
          className={cn(
            'grid h-full min-h-0 overflow-hidden',
            state.sidebarHidden ? 'grid-cols-[0_0_minmax(0,1fr)]' : 'grid-cols-[var(--sidebar-width)_10px_minmax(0,1fr)]',
          )}
        >
          <Sidebar controller={controller} state={state} />
          <div
            className={cn(splitterClassName, state.sidebarHidden && 'pointer-events-none')}
            role="separator"
            aria-orientation="vertical"
            aria-label="Resize sidebar"
            onMouseDown={(event) => controller.startSidebarResize(event)}
          />
          <main
            ref={terminalPaneRef}
            className={cn(
              'grid h-full min-h-0 min-w-0 overflow-hidden bg-terminal',
              state.debugOpen ? 'grid-rows-[minmax(0,1fr)_var(--debug-height)]' : 'grid-rows-[minmax(0,1fr)_34px]',
            )}
          >
            <div
              className={cn(
                'grid min-h-0 min-w-0 grid-cols-[minmax(360px,1fr)] overflow-hidden',
                state.reviewOpen &&
                  'grid-cols-[minmax(360px,1fr)_10px_minmax(420px,var(--review-width))] max-[980px]:grid-cols-[minmax(260px,1fr)_10px_minmax(360px,min(var(--review-width),58vw))]',
              )}
            >
              <div className="relative h-full min-h-0 min-w-0 overflow-hidden">
                <div ref={terminalRootRef} className="terminal h-full min-h-0 min-w-0 w-full box-border px-4 py-3.5" />
              </div>
              <div
                className={cn(reviewSplitterClassName, !state.reviewOpen && 'hidden')}
                role="separator"
                aria-orientation="vertical"
                aria-label="Resize diff panel"
                onMouseDown={(event) => controller.startReviewResize(event)}
              />
              <ReviewPanel
                controller={controller}
                state={state}
                reviewViewRef={reviewViewRef}
                reviewMainRef={reviewMainRef}
                diffListRef={diffListRef}
              />
            </div>
            <DebugPanel controller={controller} open={state.debugOpen} output={state.debugOutput} />
          </main>
        </div>
      </div>
      <EnvironmentDialogView controller={controller} state={state} />
      <GlobalConfigDialogView controller={controller} state={state} />
      <ManageDialogView controller={controller} state={state} />
      <TenantDialogView controller={controller} state={state} />
    </TooltipProvider>
  );
}

function DebugPanel({ controller, open, output }: { controller: ERunUIController; open: boolean; output: string }): React.ReactElement {
  const outputRef = React.useRef<HTMLPreElement>(null);

  React.useEffect(() => {
    if (open && outputRef.current) {
      outputRef.current.scrollTop = outputRef.current.scrollHeight;
    }
  }, [open, output]);

  return (
    <section className={cn('grid min-h-0 border-t border-[oklch(0.26_0_0)] bg-[oklch(0.06_0_0)] text-[oklch(0.86_0_0)]', open ? 'grid-rows-[6px_34px_minmax(0,1fr)]' : 'grid-rows-[34px]')}>
      {open && (
        <div
          className={debugSplitterClassName}
          role="separator"
          aria-orientation="horizontal"
          aria-label="Resize debug panel"
          onMouseDown={(event) => controller.startDebugResize(event)}
        />
      )}
      <div className="flex h-[34px] items-center justify-between gap-2 border-b border-[oklch(0.18_0_0)] px-3">
        <button
          type="button"
          className="flex min-w-0 items-center gap-2 border-0 bg-transparent p-0 text-xs font-medium tracking-normal text-[oklch(0.76_0_0)]"
          onClick={() => controller.setDebugOpen(!open)}
        >
          {open ? <ChevronDown className="size-4" aria-hidden="true" /> : <ChevronUp className="size-4" aria-hidden="true" />}
          <span>Debug</span>
          <span className="text-[11px] font-normal text-[oklch(0.56_0_0)]">{open ? 'erun -vv output' : 'collapsed'}</span>
        </button>
        {open && (
          <Button className="h-6 px-2 text-[11px] [&_svg]:size-3.5" type="button" variant="ghost" size="sm" onClick={() => controller.clearDebugOutput()}>
            <Trash2 aria-hidden="true" />
            Clear
          </Button>
        )}
      </div>
      {open && (
        <pre
          ref={outputRef}
          className="min-h-0 overflow-auto whitespace-pre-wrap break-words px-3 py-2 font-mono text-[11px] leading-[1.35] text-[oklch(0.82_0_0)]"
        >
          {output || 'Run an environment command while Debug is expanded to stream erun -vv output here.'}
        </pre>
      )}
    </section>
  );
}
