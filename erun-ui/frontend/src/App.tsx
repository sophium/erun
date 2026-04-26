import * as React from 'react';
import { Check, Copy } from 'lucide-react';

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
              'grid h-full min-h-0 min-w-0 grid-cols-[minmax(360px,1fr)] overflow-hidden bg-terminal',
              state.reviewOpen &&
                'grid-cols-[minmax(360px,1fr)_10px_minmax(420px,var(--review-width))] max-[980px]:grid-cols-[minmax(260px,1fr)_10px_minmax(360px,min(var(--review-width),58vw))]',
            )}
          >
            <div className="relative h-full min-h-0 min-w-0 overflow-hidden">
              <div ref={terminalRootRef} className="terminal h-full min-h-0 min-w-0 w-full box-border px-4 py-3.5" />
              <div
                className={cn(
                  'pointer-events-none absolute inset-0 flex items-center justify-center bg-[oklch(0_0_0/0.68)] p-10 text-center text-lg leading-[1.45] text-[oklch(0.92_0_0)]',
                  state.terminalCopyOutput && 'pointer-events-auto',
                  !state.terminalMessage && 'hidden',
                )}
              >
                <div className="flex max-w-[min(680px,100%)] flex-col items-center gap-3.5">
                  <div>{state.terminalMessage}</div>
                  {state.terminalCopyOutput && (
                    <Button
                      className="pointer-events-auto cursor-pointer border-[oklch(0.92_0_0/0.55)] bg-[oklch(0.98_0_0)] text-[oklch(0.18_0_0)] opacity-100 shadow-[0_10px_30px_oklch(0_0_0/0.32)] hover:border-[oklch(1_0_0/0.75)] hover:bg-[oklch(1_0_0)] hover:text-[oklch(0.12_0_0)] [&_svg]:size-[15px]"
                      type="button"
                      variant="outline"
                      size="sm"
                      onClick={() => {
                        void controller.copyTerminalOutput();
                      }}
                    >
                      {state.terminalCopyStatus === 'Copied' ? <Check aria-hidden="true" /> : <Copy aria-hidden="true" />}
                      {state.terminalCopyStatus || 'Copy output'}
                    </Button>
                  )}
                </div>
              </div>
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
