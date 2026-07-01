import * as React from 'react';

import { useAppDispatch } from '@/app/hooks';
import { titlebarDoubleClick } from '@/app/layoutThunks';
import { TitlebarLeftControls, TitlebarRightControls } from '@/components/app/Titlebar.Controls';
import { IdleStatusWidget } from '@/components/app/Titlebar.IdleStatusWidget';
import { TitlebarStatus } from '@/components/app/Titlebar.Status';

// Titlebar lays out three slots in a flex row:
//   - Left: window-control adjacency + sidebar toggle
//   - Center: TitlebarStatus pill (flex-1, centered)
//   - Right: idle widget + IDE buttons + review/files toggles
//
// The previous layout pinned every child with absolute positioning and
// hardcoded `right-[Npx]` offsets that had to be updated in lockstep across
// three files whenever a button was added or removed. Switching to flex
// removes that hidden coupling — slots now consume only the space they need
// and the right cluster reflows automatically.
//
// The absolute drag bedrock is rendered first so subsequent flex siblings
// stack above it without needing explicit z-index on every button.
export function Titlebar(): React.ReactElement {
  const dispatch = useAppDispatch();
  // min-w-0 on the header: it is a grid item (row 1 of the app grid) whose
  // default min-width:auto lets it grow to its content width. A long status
  // message then stretched the header past the viewport, so the pill's
  // `max-w-full` resolved to that oversized width — the message never truncated
  // and the dismiss button was pushed off-screen, leaving the banner
  // un-dismissable (#713). min-w-0 caps the header at the grid track (viewport),
  // restoring the truncate chain so the message ellipsizes and the dismiss X
  // stays reachable.
  return (
    <header
      className="relative box-border flex h-full min-w-0 items-center gap-3 border-b bg-[color-mix(in_oklch,var(--background)_94%,transparent)] px-3 select-none [--wails-draggable:drag]"
      data-wails-drag
      onDoubleClick={(event) => {
        dispatch(titlebarDoubleClick(event));
      }}
    >
      <div className="pointer-events-none absolute inset-0" data-wails-drag aria-hidden="true" />
      <TitlebarLeftControls />
      <div className="relative z-[1] flex min-w-0 flex-1 justify-center">
        <TitlebarStatus />
      </div>
      <div className="relative z-[1] flex items-center gap-2 [--wails-draggable:no-drag]">
        <IdleStatusWidget />
        <TitlebarRightControls />
      </div>
    </header>
  );
}
