import { cn, ErrorBoundary, ResizeHandle, TooltipProvider } from 'erun-kit';
import * as React from 'react';

import { ControllerProvider } from '@/app/ControllerContext';
import { useAppDispatch, useAppSelector } from '@/app/hooks';
import { startSidebarResize, stepSidebarResize } from '@/app/layoutThunks';
import { setActivityQueueOpen } from '@/app/slices/layoutSlice';
import { MAX_SIDEBAR_WIDTH, MIN_SIDEBAR_WIDTH } from '@/app/state';
import { TerminalController } from '@/app/TerminalController';
import { ActivityQueueLauncher } from '@/components/app/ActivityQueueLauncher';
import { AIOccupancyPromptDialog } from '@/components/app/AIOccupancyPromptDialog';
import { AutoStartPromptDialog } from '@/components/app/AutoStartPromptDialog';
import { CloseConfirmDialog } from '@/components/app/CloseConfirmDialog';
import { EnvironmentDialogView } from '@/components/app/EnvironmentDialogView';
import { GlobalConfigDialogView } from '@/components/app/GlobalConfigDialogView';
import { MainPane } from '@/components/app/MainPane';
import { ManageDialogView } from '@/components/app/ManageDialogView';
import { OrchestratorDialog } from '@/components/app/OrchestratorDialog';
import { OutputsDialog } from '@/components/app/OutputsDialog';
import { PinVersionDialog } from '@/components/app/PinVersionDialog';
import { ReconnectDialog } from '@/components/app/ReconnectDialog';
import { ReconnectStatusPanel } from '@/components/app/ReconnectStatusPanel';
import { ReviewDetailDialog } from '@/components/app/ReviewDetailDialog';
import { Sidebar } from '@/components/app/Sidebar';
import { TenantDialogView } from '@/components/app/TenantDialogView';
import { Titlebar } from '@/components/app/Titlebar';
import { UpgradeAllDialog } from '@/components/app/UpgradeAllDialog';

const splitterClassName =
  'relative cursor-col-resize bg-transparent before:absolute before:top-0 before:bottom-0 before:left-1 before:w-px before:bg-transparent before:transition-colors hover:before:bg-border [.is-resizing_&]:before:bg-border';

export function App(): React.ReactElement {
  const controller = React.useMemo(() => new TerminalController(), []);
  const dispatch = useAppDispatch();
  const sidebarHidden = useAppSelector((state) => state.layout.sidebarHidden);
  const sidebarWidth = useAppSelector((state) => state.layout.sidebarWidth);
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
          <ErrorBoundary>
            {/*
              The grid's track count must match the number of participating
              children. A `display:none` grid item is removed from grid flow,
              so when the sidebar is hidden the resize handle is dropped — keep
              it in the template (a 3rd `0` track) and MainPane backfills into
              the empty 0-width middle track, blanking the whole content area.
              Render the handle only while the sidebar is shown
              and use a matching 2-track template when hidden, so MainPane
              always occupies the trailing `1fr` track.
            */}
            <div
              className={cn(
                'grid h-full min-h-0 overflow-hidden',
                sidebarHidden
                  ? 'grid-cols-[0_minmax(0,1fr)]'
                  : 'grid-cols-[var(--sidebar-width)_10px_minmax(0,1fr)]',
              )}
            >
              <Sidebar />
              {!sidebarHidden && (
                <ResizeHandle
                  className={splitterClassName}
                  orientation="vertical"
                  label="Resize sidebar"
                  onMouseDown={(event) => {
                    dispatch(startSidebarResize(event));
                  }}
                  value={{ now: sidebarWidth, min: MIN_SIDEBAR_WIDTH, max: MAX_SIDEBAR_WIDTH }}
                  onStep={(delta) => {
                    dispatch(stepSidebarResize(delta));
                  }}
                />
              )}
              <MainPane
                terminalPaneRef={terminalPaneRef}
                terminalRootRef={terminalRootRef}
                reviewViewRef={reviewViewRef}
                reviewMainRef={reviewMainRef}
                diffListRef={diffListRef}
                onOpenActivityQueue={() => dispatch(setActivityQueueOpen(true))}
              />
            </div>
          </ErrorBoundary>
        </div>
        <EnvironmentDialogView />
        <GlobalConfigDialogView />
        <ManageDialogView />
        <ReconnectDialog />
        <ReconnectStatusPanel />
        <TenantDialogView />
        <UpgradeAllDialog />
        <OutputsDialog />
        <PinVersionDialog />
        <OrchestratorDialog />
        <AutoStartPromptDialog />
        <CloseConfirmDialog />
        <AIOccupancyPromptDialog />
        <ReviewDetailDialog />
        <ActivityQueueLauncher
          open={activityQueueOpen}
          onOpen={() => dispatch(setActivityQueueOpen(true))}
          onClose={() => dispatch(setActivityQueueOpen(false))}
        />
      </TooltipProvider>
    </ControllerProvider>
  );
}
