import * as React from 'react';

import { useAppDispatch } from '@/app/hooks';
import { titlebarDoubleClick } from '@/app/layoutThunks';
import { TitlebarControls } from '@/components/app/Titlebar.Controls';
import { IdleStatusWidget } from '@/components/app/Titlebar.IdleStatusWidget';
import { TitlebarStatus } from '@/components/app/Titlebar.Status';

export function Titlebar(): React.ReactElement {
  const dispatch = useAppDispatch();
  return (
    <header
      className="relative box-border select-none border-b bg-[color-mix(in_oklch,var(--background)_94%,transparent)] [--wails-draggable:drag]"
      data-wails-drag
      onDoubleClick={(event) => {
        dispatch(titlebarDoubleClick(event));
      }}
    >
      <TitlebarControls />
      <IdleStatusWidget />
      <TitlebarStatus />
      <div className="absolute inset-0" data-wails-drag />
    </header>
  );
}
