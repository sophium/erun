import { cn } from 'erun-kit';
import * as React from 'react';

import { useAppSelector } from '@/app/hooks';
import { DebugPanel } from '@/components/app/DebugPanel';
import { TenantDashboardView } from '@/components/app/TenantDashboardView';
import { TerminalPane } from '@/components/app/TerminalPane';

export function MainPane({
  terminalPaneRef,
  terminalRootRef,
  reviewViewRef,
  reviewMainRef,
  diffListRef,
  onOpenActivityQueue,
}: {
  terminalPaneRef: React.RefObject<HTMLElement | null>;
  terminalRootRef: React.RefObject<HTMLDivElement | null>;
  reviewViewRef: React.RefObject<HTMLElement | null>;
  reviewMainRef: React.RefObject<HTMLDivElement | null>;
  diffListRef: React.RefObject<HTMLDivElement | null>;
  onOpenActivityQueue: () => void;
}): React.ReactElement {
  const dashboardTenant = useAppSelector((state) => state.tenantDashboard.tenant);
  const debugOpen = useAppSelector((state) => state.layout.debugOpen);
  const dashboardOpen = Boolean(dashboardTenant);
  return (
    <main
      ref={terminalPaneRef}
      className={cn(
        // grid-cols-[minmax(0,1fr)] pins the single implicit column to the
        // element's own width. Without it, a grid's column defaults to
        // "auto" sizing, which grows to fit a wide descendant (e.g. the
        // tenant dashboard's data tables) instead of letting that descendant
        // scroll within the width the shell actually gave <main>.
        'grid h-full min-h-0 min-w-0 grid-cols-[minmax(0,1fr)] overflow-hidden bg-terminal',
        dashboardOpen
          ? 'grid-rows-[minmax(0,1fr)] bg-background'
          : debugOpen
            ? 'grid-rows-[minmax(0,1fr)_var(--debug-height)]'
            : 'grid-rows-[minmax(0,1fr)_34px]',
      )}
    >
      {dashboardOpen && (
        // TenantDashboardView's own root does not shrink below its content's
        // natural width (a wide data table can widen it past what a narrow
        // <main> has to give). Contain that here with a scrollbar rather than
        // <main>'s own overflow-hidden silently clipping it: a control the
        // shell can't yet lay out within the viewport must still be reachable.
        <div className="min-h-0 min-w-0 overflow-x-auto">
          <TenantDashboardView />
        </div>
      )}
      <TerminalPane
        hidden={dashboardOpen}
        terminalRootRef={terminalRootRef}
        reviewViewRef={reviewViewRef}
        reviewMainRef={reviewMainRef}
        diffListRef={diffListRef}
        onOpenActivityQueue={onOpenActivityQueue}
      />
      {!dashboardOpen && <DebugPanel open={debugOpen} />}
    </main>
  );
}
