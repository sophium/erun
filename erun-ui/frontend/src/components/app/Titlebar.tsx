import * as React from 'react';

import { useAppDispatch } from '@/app/hooks';
import { titlebarDoubleClick } from '@/app/layoutThunks';
import { TitlebarLeftControls, TitlebarRightControls } from '@/components/app/Titlebar.Controls';
import { IdleStatusWidget } from '@/components/app/Titlebar.IdleStatusWidget';
import { TitlebarMessageCenter } from '@/components/app/Titlebar.MessageCenter';
import { TitlebarStatus } from '@/components/app/Titlebar.Status';
import { ThemeToggle } from '@/components/app/Titlebar.ThemeToggle';
import { TitlebarWhipAction } from '@/components/app/Titlebar.WhipAction';

export function Titlebar(): React.ReactElement {
  const dispatch = useAppDispatch();
  // min-w-0 lets the status pill truncate so the dismiss button stays on-screen.
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
        {/* After IdleStatusWidget so these keep a constant offset from the right edge. */}
        <TitlebarMessageCenter />
        <TitlebarRightControls />
        <TitlebarWhipAction />
        <ThemeToggle />
      </div>
    </header>
  );
}
