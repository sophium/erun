import * as React from 'react';
import { Check, Copy } from 'lucide-react';

import { ERunUIController } from '@/app/ERunUIController';
import { useControllerState } from '@/app/useControllerState';
import { EnvironmentDialogView } from '@/components/app/EnvironmentDialogView';
import { ManageDialogView } from '@/components/app/ManageDialogView';
import { ReviewPanel } from '@/components/app/ReviewPanel';
import { Sidebar } from '@/components/app/Sidebar';
import { Titlebar } from '@/components/app/Titlebar';
import { Button } from '@/components/ui/button';
import { TooltipProvider } from '@/components/ui/tooltip';
import { cn } from '@/lib/utils';

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
      <div className={cn('app-shell', state.sidebarHidden && 'sidebar-hidden')}>
        <Titlebar controller={controller} state={state} />
        <div className="workbench">
          <Sidebar controller={controller} state={state} />
          <div
            className="splitter"
            role="separator"
            aria-orientation="vertical"
            aria-label="Resize sidebar"
            onMouseDown={(event) => controller.startSidebarResize(event)}
          />
          <main
            ref={terminalPaneRef}
            className={cn('terminal-pane', state.reviewOpen && 'has-review-panel')}
          >
            <div className="terminal-view">
              <div ref={terminalRootRef} className="terminal" />
              <div className={cn('terminal-message', state.terminalCopyOutput && 'has-copy-action', !state.terminalMessage && 'is-hidden')}>
                <div className="terminal-message-content">
                  <div>{state.terminalMessage}</div>
                  {state.terminalCopyOutput && (
                    <Button
                      className="terminal-copy-button"
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
              className={cn('review-splitter', !state.reviewOpen && 'is-hidden')}
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
      <ManageDialogView controller={controller} state={state} />
    </TooltipProvider>
  );
}
