import { cn, ResizeHandle } from 'erun-kit';
import * as React from 'react';

import { useTerminalActivityLockState } from '@/app/activityQueueState';
import { useAppDispatch, useAppSelector } from '@/app/hooks';
import { startReviewResize } from '@/app/layoutThunks';
import { selectActiveTabIsAI } from '@/app/selectors';
import { clearHiddenLockOverlay, hideLockOverlay } from '@/app/slices/terminalStatusSlice';
import { ActivityLockOverlay } from '@/components/app/ActivityLockOverlay';
import { AIOccupancyBanner } from '@/components/app/AIOccupancyBanner';
import { ReviewPanel } from '@/components/app/ReviewPanel';
import { TerminalBusyOverlay } from '@/components/app/TerminalBusyOverlay';
import { TerminalTabStrip } from '@/components/app/TerminalTabStrip';

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
  const terminalBusy = useAppSelector((state) => state.terminalStatus.terminalBusy);
  const terminalMessage = useAppSelector((state) => state.terminalStatus.terminalMessage);
  const locks = useTerminalActivityLockState();
  const hiddenForSession = useAppSelector(
    (state) => state.terminalStatus.hiddenLockSessions[sessionId],
  );
  const liveLock = locks.get(sessionId) ?? null;
  const activeTabIsAI = useAppSelector(selectActiveTabIsAI);
  const occupancyLeases = useAppSelector((state) => state.idle.idleStatus?.leases ?? []);
  // The user can dismiss the overlay locally for a session if it's
  // covering output they need to read or input they need to provide
  // (e.g. the in-pod CLI's helm-recovery prompt). Backend keeps the
  // lock state intact; only this desktop's view of it is hidden.
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
        // The terminal column tracks the visible width (min 0). A hard min
        // (e.g. minmax(360px,1fr)) forces the pane wider than the available
        // area when the sidebar is wide or the window is narrow, so the
        // `overflow-hidden` parent clips its right edge — and drags the
        // right-anchored ActivityLockOverlay off-screen with it.
        // At any width where the terminal already fits, `minmax(0,1fr)` is
        // pixel-identical to a hard min; it only differs when starved, where
        // letting xterm reflow narrower beats clipping content off the right.
        'grid min-h-0 min-w-0 grid-cols-[minmax(0,1fr)] overflow-hidden',
        hidden && 'hidden',
        reviewOpen &&
          'grid-cols-[minmax(0,1fr)_10px_minmax(420px,var(--review-width))] max-[980px]:grid-cols-[minmax(0,1fr)_10px_minmax(360px,min(var(--review-width),58vw))]',
      )}
    >
      <div className="grid h-full min-h-0 min-w-0 grid-rows-[32px_minmax(0,1fr)] overflow-hidden">
        <TerminalTabStrip />
        {/* Padding lives on the wrapper, not on the FitAddon parent: xterm's FitAddon reads the parent's computed height but does not subtract its padding, so any padding on terminalRoot would over-count rows and clip the bottom line. */}
        <div
          id="erun-terminal-pane"
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
            occupancyLeases.length > 0 && <AIOccupancyBanner leases={occupancyLeases} />
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
      />
      <ReviewPanel
        reviewViewRef={reviewViewRef}
        reviewMainRef={reviewMainRef}
        diffListRef={diffListRef}
      />
    </div>
  );
}
