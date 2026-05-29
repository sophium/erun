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
import { ContributeToggle } from '@/components/app/Titlebar.ContributeToggle';
import { ideTooltipLabel, isIdeDisabled } from '@/components/app/Titlebar.helpers';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import type { UISelection } from '@/types';

// Each titlebar button used to set its own absolute position with a
// hardcoded `right-[Npx]` offset; that coupled the layout to the rendered
// width of every other button in three files. After the flex refactor the
// buttons just declare their visual state and the parent flex row owns
// spacing.
const titlebarButtonClassName =
  'size-7 flex-none cursor-pointer rounded-[var(--radius)] border-0 bg-transparent text-muted-foreground [--wails-draggable:no-drag] hover:bg-accent hover:text-accent-foreground [&_svg]:size-[18px]';

const activeTitlebarButtonClassName =
  'bg-primary text-primary-foreground hover:bg-primary hover:text-primary-foreground';

// TitlebarLeftControls renders the leftmost cluster: a sidebar-only toggle.
// The cluster sits on a positioned wrapper so it stacks above the drag
// bedrock without the button needing its own z-index.
export function TitlebarLeftControls(): React.ReactElement {
  const dispatch = useAppDispatch();
  const sidebarHidden = useAppSelector((state) => state.layout.sidebarHidden);
  const SidebarIcon = sidebarHidden ? PanelLeftOpen : PanelLeftClose;
  return (
    <div className="relative z-[1] flex items-center gap-2 pl-[88px] [--wails-draggable:no-drag] max-[980px]:pl-[80px]">
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
    </div>
  );
}

// TitlebarRightControls renders the right cluster: the two IDE launchers,
// the diff-panel toggle, and the files-list toggle. Their visibility,
// disabled state, and accessible labels are unchanged from the absolute
// layout; the only difference is they now flow inside their parent flex
// row instead of stacking with right-pixel offsets.
export function TitlebarRightControls(): React.ReactElement {
  const dispatch = useAppDispatch();
  const reviewOpen = useAppSelector((state) => state.layout.reviewOpen);
  const filesOpen = useAppSelector((state) => state.layout.filesOpen);
  const selected = useAppSelector((state) => state.selection.selected);
  const tenants = useAppSelector((state) => state.tenants.tenants);
  const ReviewIcon = reviewOpen ? PanelRightClose : PanelRightOpen;
  const ideDisabled = isIdeDisabled(selected, tenants);
  const vscodeTooltip = ideTooltipLabel('VS Code', selected, ideDisabled);
  const intellijTooltip = ideTooltipLabel('IntelliJ IDEA', selected, ideDisabled);

  return (
    <>
      <IDETitlebarButton
        selected={selected}
        disabled={ideDisabled}
        tooltip={vscodeTooltip}
        icon={<Code2 />}
        variant="vscode"
        dispatch={dispatch}
      />
      <IDETitlebarButton
        selected={selected}
        disabled={ideDisabled}
        tooltip={intellijTooltip}
        icon={<Blocks />}
        variant="intellij"
        dispatch={dispatch}
      />
      <ContributeToggle />
      <IconTooltip label="Toggle diff panel">
        <Button
          className={cn(titlebarButtonClassName, reviewOpen && activeTitlebarButtonClassName)}
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
      {reviewOpen && (
        <IconTooltip label="Toggle changed files list">
          <Button
            className={cn(titlebarButtonClassName, filesOpen && activeTitlebarButtonClassName)}
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
      )}
    </>
  );
}

interface IDETitlebarButtonProps {
  selected: UISelection | null;
  disabled: boolean;
  tooltip: string;
  icon: React.ReactElement;
  variant: 'vscode' | 'intellij';
  dispatch: ReturnType<typeof useAppDispatch>;
}

function IDETitlebarButton({
  selected,
  disabled,
  tooltip,
  icon,
  variant,
  dispatch,
}: IDETitlebarButtonProps): React.ReactElement {
  return (
    <IconTooltip label={tooltip}>
      <Button
        className={titlebarButtonClassName}
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
    </IconTooltip>
  );
}
