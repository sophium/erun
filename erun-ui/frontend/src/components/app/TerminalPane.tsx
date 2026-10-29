import { cn, ResizeHandle } from 'erun-kit';
import * as React from 'react';

import { useTerminalActivityLockState } from '@/app/activityQueueState';
import { useAppDispatch, useAppSelector } from '@/app/hooks';
import { startReviewResize, stepReviewResize } from '@/app/layoutThunks';
import { openManageDialog, setManageTab } from '@/app/manageDialogThunks';
import { selectActiveTabIsAI } from '@/app/selectors';
import { clearHiddenLockOverlay, hideLockOverlay } from '@/app/slices/terminalStatusSlice';
import { computeMaxReviewWidth, MIN_REVIEW_WIDTH } from '@/app/state';
import { ActivityLockOverlay } from '@/components/app/ActivityLockOverlay';
import { AIOccupancyBanner } from '@/components/app/AIOccupancyBanner';
import { ReviewPanel } from '@/components/app/ReviewPanel';
import { TerminalBusyOverlay } from '@/components/app/TerminalBusyOverlay';
import { TerminalTabStrip } from '@/components/app/TerminalTabStrip';
import type { UIEnvironmentLease, UISelection } from '@/types';

const reviewSplitterClassName =
  'relative cursor-col-resize border-l bg-background before:absolute before:top-0 before:bottom-0 before:left-1 before:w-px before:bg-transparent before:transition-colors hover:before:bg-border [.is-resizing-review_&]:before:bg-border';

export function TerminalPane({
  hidden,
  terminalRootRef,
  reviewViewRef,
  reviewMainRef,
  diffListRef,
  onOpenActivityQueue,
}: {
  hidden: boolean;
  terminalRootRef: React.RefObject<HTMLDivElement | null>;
  reviewViewRef: React.RefObject<HTMLElement | null>;
  reviewMainRef: React.RefObject<HTMLDivElement | null>;
  diffListRef: React.RefObject<HTMLDivElement | null>;
  onOpenActivityQueue: () => void;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const sessionId = useAppSelector((state) => state.terminal.sessionId);
  const reviewOpen = useAppSelector((state) => state.layout.reviewOpen);
  const reviewWidth = useAppSelector((state) => state.layout.reviewWidth);
  const effectiveSidebarWidth = useAppSelector((state) =>
    state.layout.sidebarHidden ? 0 : state.layout.sidebarWidth,
  );
  const terminalBusy = useAppSelector((state) => state.terminalStatus.terminalBusy);
  const terminalMessage = useAppSelector((state) => state.terminalStatus.terminalMessage);
  const locks = useTerminalActivityLockState();
  const hiddenForSession = useAppSelector(
    (state) => state.terminalStatus.hiddenLockSessions[sessionId],
  );
  const liveLock = locks.get(sessionId) ?? null;
  const activeTabIsAI = useAppSelector(selectActiveTabIsAI);
  const occupancyLeases = useAppSelector((state) => state.idle.idleStatus?.leases ?? []);
  // The selection the leases were loaded for, not the active tab.
  const occupancySelection = useAppSelector((state) => state.selection.selected);
  // Dismissal hides the overlay on this desktop only; the backend lock is untouched.
  React.useEffect(() => {
    if (!liveLock && hiddenForSession) {
      dispatch(clearHiddenLockOverlay(sessionId));
    }
  }, [dispatch, liveLock, hiddenForSession, sessionId]);
  const activeLock = liveLock && !hiddenForSession ? liveLock : null;
  const hideActiveLock = React.useCallback(() => {
    dispatch(hideLockOverlay(sessionId));
  }, [dispatch, sessionId]);
  return (
    <div
      className={cn(
        // minmax(0,1fr): a hard min would clip the pane and drag the right-anchored
        // ActivityLockOverlay off-screen when space is starved.
        'grid min-h-0 min-w-0 grid-cols-[minmax(0,1fr)] overflow-hidden',
        hidden && 'hidden',
        reviewOpen &&
          'grid-cols-[minmax(0,1fr)_10px_minmax(420px,var(--review-width))] max-[980px]:grid-cols-[minmax(0,1fr)_10px_minmax(360px,min(var(--review-width),58vw))]',
      )}
    >
      <div className="grid h-full min-h-0 min-w-0 grid-rows-[32px_minmax(0,1fr)] overflow-hidden">
        <TerminalTabStrip />
        {/* Padding lives on the wrapper: FitAddon does not subtract parent padding. */}
        <div
          id="erun-terminal-pane"
          role="group"
          aria-label="Terminal"
          className="relative h-full min-h-0 min-w-0 overflow-hidden box-border px-4 pt-3.5"
        >
          <div ref={terminalRootRef} className="terminal h-full min-h-0 min-w-0 w-full" />
          <TerminalBusyOverlay message={terminalBusy ? terminalMessage : ''} />
          {activeLock ? (
            <ActivityLockOverlay
              lock={activeLock}
              onOpenQueue={onOpenActivityQueue}
              onProceedAnyway={hideActiveLock}
            />
          ) : (
            activeTabIsAI &&
            occupancyLeases.length > 0 && (
              <OccupancyBanner leases={occupancyLeases} selection={occupancySelection} />
            )
          )}
        </div>
      </div>
      <ResizeHandle
        className={reviewSplitterClassName}
        orientation="vertical"
        label="Resize diff panel"
        hidden={!reviewOpen}
        onMouseDown={(event) => {
          dispatch(startReviewResize(event));
        }}
        value={{
          now: reviewWidth,
          min: MIN_REVIEW_WIDTH,
          max: computeMaxReviewWidth(window.innerWidth, effectiveSidebarWidth),
        }}
        onStep={(delta) => {
          dispatch(stepReviewResize(delta));
        }}
      />
      <ReviewPanel
        reviewViewRef={reviewViewRef}
        reviewMainRef={reviewMainRef}
        diffListRef={diffListRef}
      />
    </div>
  );
}

function OccupancyBanner({
  leases,
  selection,
}: {
  leases: UIEnvironmentLease[];
  selection: UISelection | null;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  return (
    <AIOccupancyBanner
      leases={leases}
      selection={selection}
      onShowJobs={
        selection
          ? () => {
              dispatch(openManageDialog(selection));
              dispatch(setManageTab('jobs'));
            }
          : undefined
      }
    />
  );
}
