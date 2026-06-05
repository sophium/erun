import * as React from 'react';

import { ControllerProvider } from '@/app/ControllerContext';
import { useAppDispatch, useAppSelector } from '@/app/hooks';
import { startSidebarResize } from '@/app/layoutThunks';
import { setActivityQueueOpen } from '@/app/slices/layoutSlice';
import { TerminalController } from '@/app/TerminalController';
import { ActivityQueueLauncher } from '@/components/app/ActivityQueueLauncher';
import { AutoStartPromptDialog } from '@/components/app/AutoStartPromptDialog';
import { EnvironmentDialogView } from '@/components/app/EnvironmentDialogView';
import { GlobalConfigDialogView } from '@/components/app/GlobalConfigDialogView';
import { MainPane } from '@/components/app/MainPane';
import { ManageDialogView } from '@/components/app/ManageDialogView';
import { ReconnectDialog } from '@/components/app/ReconnectDialog';
import { ReconnectStatusPanel } from '@/components/app/ReconnectStatusPanel';
import { ResizeHandle } from '@/components/app/ResizeHandle';
import { Sidebar } from '@/components/app/Sidebar';
import { TenantDialogView } from '@/components/app/TenantDialogView';
import { Titlebar } from '@/components/app/Titlebar';
import { TooltipProvider } from '@/components/ui/tooltip';
import { cn } from '@/lib/utils';

const splitterClassName =
  'relative cursor-col-resize bg-transparent before:absolute before:top-0 before:bottom-0 before:left-1 before:w-px before:bg-transparent before:transition-colors hover:before:bg-border [.is-resizing_&]:before:bg-border';

export function App(): React.ReactElement {
  const controller = React.useMemo(() => new TerminalController(), []);
  const dispatch = useAppDispatch();
  const sidebarHidden = useAppSelector((state) => state.layout.sidebarHidden);
  const activityQueueOpen = useAppSelector((state) => state.layout.activityQueueOpen);
  const terminalRootRef = React.useRef<HTMLDivElement>(null);
  const terminalPaneRef = React.useRef<HTMLElement>(null);
  const reviewViewRef = React.useRef<HTMLElement>(null);
  const reviewMainRef = React.useRef<HTMLDivElement>(null);
  const diffListRef = React.useRef<HTMLDivElement>(null);

  React.useEffect(() => {
    if (
      !terminalRootRef.current ||
      !terminalPaneRef.current ||
      !reviewViewRef.current ||
      !reviewMainRef.current ||
      !diffListRef.current
    ) {
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
    <ControllerProvider controller={controller}>
      <TooltipProvider>
        <div className="grid h-full w-full grid-rows-[52px_minmax(0,1fr)] bg-background">
          <Titlebar />
          <div
            className={cn(
              'grid h-full min-h-0 overflow-hidden',
              sidebarHidden
                ? 'grid-cols-[0_0_minmax(0,1fr)]'
                : 'grid-cols-[var(--sidebar-width)_10px_minmax(0,1fr)]',
            )}
          >
            <Sidebar />
            <ResizeHandle
              className={splitterClassName}
              orientation="vertical"
              label="Resize sidebar"
              hidden={sidebarHidden}
              onMouseDown={(event) => {
                dispatch(startSidebarResize(event));
              }}
            />
            <MainPane
              terminalPaneRef={terminalPaneRef}
              terminalRootRef={terminalRootRef}
              reviewViewRef={reviewViewRef}
              reviewMainRef={reviewMainRef}
              diffListRef={diffListRef}
              onOpenActivityQueue={() => dispatch(setActivityQueueOpen(true))}
            />
          </div>
        </div>
        <EnvironmentDialogView />
        <GlobalConfigDialogView />
        <ManageDialogView />
        <ReconnectDialog />
        <ReconnectStatusPanel />
        <TenantDialogView />
        <AutoStartPromptDialog />
        <ActivityQueueLauncher
          open={activityQueueOpen}
          onOpen={() => dispatch(setActivityQueueOpen(true))}
          onClose={() => dispatch(setActivityQueueOpen(false))}
        />
      </TooltipProvider>
    </ControllerProvider>
  );
}
