import {
  Blocks,
  Code2,
  ListTree,
  PanelLeftClose,
  PanelLeftOpen,
  PanelRightClose,
  PanelRightOpen,
} from 'lucide-react';
import * as React from 'react';

import { useAppDispatch, useAppSelector } from '@/app/hooks';
import { openIDE } from '@/app/ideOpenThunks';
import { setFilesOpen, toggleReview, toggleSidebar } from '@/app/layoutThunks';
import { IconTooltip } from '@/components/app/IconTooltip';
import { ideTooltipLabel, isIdeDisabled } from '@/components/app/Titlebar.helpers';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import type { UISelection } from '@/types';

const titlebarButtonClassName =
  'absolute top-3 left-[88px] z-[1] size-7 flex-none cursor-pointer rounded-[var(--radius)] border-0 bg-transparent text-muted-foreground [--wails-draggable:no-drag] hover:bg-accent hover:text-accent-foreground [&_svg]:size-[18px] max-[980px]:left-[76px]';

const activeTitlebarButtonClassName =
  'bg-primary text-primary-foreground hover:bg-primary hover:text-primary-foreground';

interface IDETitlebarButtonProps {
  selected: UISelection | null;
  disabled: boolean;
  tooltip: string;
  className: string;
  icon: React.ReactElement;
  variant: 'vscode' | 'intellij';
  dispatch: ReturnType<typeof useAppDispatch>;
}

function IDETitlebarButton({
  selected,
  disabled,
  tooltip,
  className,
  icon,
  variant,
  dispatch,
}: IDETitlebarButtonProps): React.ReactElement {
  return (
    <IconTooltip label={tooltip}>
      <span className={className}>
        <Button
          className="size-full border-0 bg-transparent text-inherit hover:bg-accent hover:text-accent-foreground disabled:pointer-events-none disabled:opacity-50 [&_svg]:size-[18px]"
          type="button"
          variant="ghost"
          size="icon"
          aria-label={tooltip}
          disabled={disabled}
          onClick={() => {
            void dispatch(openIDE(selected ?? null, variant));
          }}
        >
          {icon}
        </Button>
      </span>
    </IconTooltip>
  );
}

export function TitlebarControls(): React.ReactElement {
  const dispatch = useAppDispatch();
  const sidebarHidden = useAppSelector((state) => state.layout.sidebarHidden);
  const reviewOpen = useAppSelector((state) => state.layout.reviewOpen);
  const filesOpen = useAppSelector((state) => state.layout.filesOpen);
  const selected = useAppSelector((state) => state.selection.selected);
  const tenants = useAppSelector((state) => state.tenants.tenants);
  const SidebarIcon = sidebarHidden ? PanelLeftOpen : PanelLeftClose;
  const ReviewIcon = reviewOpen ? PanelRightClose : PanelRightOpen;
  const ideDisabled = isIdeDisabled(selected, tenants);
  const vscodeTooltip = ideTooltipLabel('VS Code', selected, ideDisabled);
  const intellijTooltip = ideTooltipLabel('IntelliJ IDEA', selected, ideDisabled);

  return (
    <>
      <IconTooltip label="Toggle sidebar">
        <Button
          className={titlebarButtonClassName}
          type="button"
          variant="ghost"
          size="icon"
          aria-label="Toggle sidebar"
          aria-pressed={!sidebarHidden}
          onClick={() => {
            dispatch(toggleSidebar());
          }}
        >
          <SidebarIcon />
        </Button>
      </IconTooltip>
      <IconTooltip label="Toggle diff panel">
        <Button
          className={cn(
            titlebarButtonClassName,
            'left-auto right-[58px] max-[980px]:left-auto max-[980px]:right-12',
            reviewOpen && activeTitlebarButtonClassName,
          )}
          type="button"
          variant="ghost"
          size="icon"
          aria-label="Toggle diff panel"
          aria-pressed={reviewOpen}
          onClick={() => {
            dispatch(toggleReview());
          }}
        >
          <ReviewIcon />
        </Button>
      </IconTooltip>
      <IDETitlebarButton
        selected={selected}
        disabled={ideDisabled}
        tooltip={vscodeTooltip}
        className={cn(
          titlebarButtonClassName,
          'left-auto right-[122px] max-[980px]:left-auto max-[980px]:right-[108px]',
        )}
        icon={<Code2 />}
        variant="vscode"
        dispatch={dispatch}
      />
      <IDETitlebarButton
        selected={selected}
        disabled={ideDisabled}
        tooltip={intellijTooltip}
        className={cn(
          titlebarButtonClassName,
          'left-auto right-[90px] max-[980px]:left-auto max-[980px]:right-[78px]',
        )}
        icon={<Blocks />}
        variant="intellij"
        dispatch={dispatch}
      />
      <IconTooltip label="Toggle changed files list">
        <Button
          className={cn(
            titlebarButtonClassName,
            'left-auto right-6 max-[980px]:left-auto max-[980px]:right-3.5',
            !reviewOpen && 'hidden',
            filesOpen && activeTitlebarButtonClassName,
          )}
          type="button"
          variant="ghost"
          size="icon"
          aria-label="Toggle changed files list"
          aria-pressed={filesOpen}
          onClick={() => {
            dispatch(setFilesOpen(!filesOpen));
          }}
        >
          <ListTree />
        </Button>
      </IconTooltip>
    </>
  );
}
