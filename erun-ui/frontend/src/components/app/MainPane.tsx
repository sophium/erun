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
        'grid h-full min-h-0 min-w-0 overflow-hidden bg-terminal',
        dashboardOpen
          ? 'grid-rows-[minmax(0,1fr)] bg-background'
          : debugOpen
            ? 'grid-rows-[minmax(0,1fr)_var(--debug-height)]'
            : 'grid-rows-[minmax(0,1fr)_34px]',
      )}
    >
      {dashboardOpen && <TenantDashboardView />}
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
