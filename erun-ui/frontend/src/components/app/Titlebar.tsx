import * as React from 'react';
import { ListTree, PanelLeftClose, PanelLeftOpen, PanelRightClose, PanelRightOpen } from 'lucide-react';

import type { ERunUIController } from '@/app/ERunUIController';
import type { AppState } from '@/app/state';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import { IconTooltip } from './IconTooltip';

const titlebarButtonClassName =
  'absolute top-3 left-[88px] z-[1] size-7 flex-none cursor-pointer rounded-[var(--radius)] border-0 bg-transparent text-muted-foreground [--wails-draggable:no-drag] hover:bg-accent hover:text-accent-foreground [&_svg]:size-[18px] max-[980px]:left-[76px]';

const activeTitlebarButtonClassName = 'bg-primary text-primary-foreground hover:bg-primary hover:text-primary-foreground';

export function Titlebar({ controller, state }: { controller: ERunUIController; state: AppState }): React.ReactElement {
  const SidebarIcon = state.sidebarHidden ? PanelLeftOpen : PanelLeftClose;
  const ReviewIcon = state.reviewOpen ? PanelRightClose : PanelRightOpen;

  return (
    <header
      className="relative box-border select-none border-b bg-[color-mix(in_oklch,var(--background)_94%,transparent)] [--wails-draggable:drag]"
      data-wails-drag
      onDoubleClick={(event) => controller.titlebarDoubleClick(event)}
    >
      <IconTooltip label="Toggle sidebar">
        <Button
          className={titlebarButtonClassName}
          type="button"
          variant="ghost"
          size="icon"
          aria-label="Toggle sidebar"
          aria-pressed={!state.sidebarHidden}
          onClick={() => controller.toggleSidebar()}
        >
          <SidebarIcon />
        </Button>
      </IconTooltip>
      <IconTooltip label="Toggle diff panel">
        <Button
          className={cn(
            titlebarButtonClassName,
            'left-auto right-[58px] max-[980px]:left-auto max-[980px]:right-12',
            state.reviewOpen && activeTitlebarButtonClassName,
          )}
          type="button"
          variant="ghost"
          size="icon"
          aria-label="Toggle diff panel"
          aria-pressed={state.reviewOpen}
          onClick={() => controller.toggleReview()}
        >
          <ReviewIcon />
        </Button>
      </IconTooltip>
      <IconTooltip label="Toggle changed files list">
        <Button
          className={cn(
            titlebarButtonClassName,
            'left-auto right-6 max-[980px]:left-auto max-[980px]:right-3.5',
            !state.reviewOpen && 'hidden',
            state.filesOpen && activeTitlebarButtonClassName,
          )}
          type="button"
          variant="ghost"
          size="icon"
          aria-label="Toggle changed files list"
          aria-pressed={state.filesOpen}
          onClick={() => controller.setFilesOpen(!state.filesOpen)}
        >
          <ListTree />
        </Button>
      </IconTooltip>
      <div className="absolute inset-0" data-wails-drag />
    </header>
  );
}
