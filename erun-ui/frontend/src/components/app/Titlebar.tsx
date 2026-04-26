import * as React from 'react';
import { ListTree, PanelLeftClose, PanelLeftOpen, PanelRightClose, PanelRightOpen } from 'lucide-react';

import type { ERunUIController } from '@/app/ERunUIController';
import type { AppState } from '@/app/state';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import { IconTooltip } from './IconTooltip';

export function Titlebar({ controller, state }: { controller: ERunUIController; state: AppState }): React.ReactElement {
  const SidebarIcon = state.sidebarHidden ? PanelLeftOpen : PanelLeftClose;
  const ReviewIcon = state.reviewOpen ? PanelRightClose : PanelRightOpen;

  return (
    <header
      className="titlebar"
      data-wails-drag
      onDoubleClick={(event) => controller.titlebarDoubleClick(event)}
    >
      <IconTooltip label="Toggle sidebar">
        <Button
          className="titlebar-button"
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
          className={cn('titlebar-button titlebar-button-right', state.reviewOpen && 'is-active')}
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
            'titlebar-button titlebar-button-right titlebar-button-files',
            !state.reviewOpen && 'is-hidden',
            state.filesOpen && 'is-active',
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
      <div className="titlebar-fill" data-wails-drag />
    </header>
  );
}
