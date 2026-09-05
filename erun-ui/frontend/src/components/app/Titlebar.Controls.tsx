import { Button, cn, IconTooltip } from 'erun-kit';
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
import { isMacPlatform } from '@/app/platform';
import { selectIsOrchestratorSession } from '@/app/selectors';
import { ContributeToggle } from '@/components/app/Titlebar.ContributeToggle';
import {
  ideTooltipLabel,
  isEnvOpenedAndRunning,
  isIdeDisabled,
} from '@/components/app/Titlebar.helpers';
import type { UISelection } from '@/types';

const titlebarButtonClassName =
  'size-7 flex-none cursor-pointer rounded-[var(--radius)] border-0 bg-transparent text-muted-foreground [--wails-draggable:no-drag] hover:bg-accent hover:text-accent-foreground [&_svg]:size-[18px]';

const activeTitlebarButtonClassName =
  'bg-primary text-primary-foreground hover:bg-primary hover:text-primary-foreground';

// macOS overlays the window traffic-light controls at the top-left, so the
// sidebar toggle must be inset to clear them. Windows/Linux put window controls
// on the right, so that inset would just push the toggle away from the edge and
// misalign it.

// TitlebarLeftControls renders the leftmost titlebar cluster: the sidebar toggle.
export function TitlebarLeftControls(): React.ReactElement {
  const dispatch = useAppDispatch();
  const sidebarHidden = useAppSelector((state) => state.layout.sidebarHidden);
  const SidebarIcon = sidebarHidden ? PanelLeftOpen : PanelLeftClose;
  return (
    <div
      className={cn(
        'relative z-[1] flex items-center gap-2 [--wails-draggable:no-drag]',
        isMacPlatform && 'pl-[88px] max-[980px]:pl-[80px]',
      )}
    >
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

// TitlebarEnvControls renders the controls that act on the SIDEBAR's selected
// environment: the two IDE buttons and the contribute toggle.
//
// Rendered only when the active session is an environment tab. They are hidden
// rather than disabled in orchestrator mode, following the tab strip's
// precedent of swapping content: disabling would leave dead icons whose
// tooltips describe an environment the operator is not working in (#1178).
function TitlebarEnvControls(): React.ReactElement {
  const dispatch = useAppDispatch();
  const selected = useAppSelector((state) => state.selection.selected);
  const tenants = useAppSelector((state) => state.tenants.tenants);
  const idleStatus = useAppSelector((state) => state.idle.idleStatus);
  const envRunning = isEnvOpenedAndRunning(selected, idleStatus, tenants);
  const ideDisabled = isIdeDisabled(selected, tenants) || !envRunning;
  const vscodeTooltip = ideTooltipLabel('VS Code', selected, ideDisabled, !envRunning);
  const intellijTooltip = ideTooltipLabel('IntelliJ IDEA', selected, ideDisabled, !envRunning);

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
      <ContributeToggle envRunning={envRunning} />
    </>
  );
}

// TitlebarRightControls renders the right titlebar cluster. In orchestrator mode
// it keeps only the diff panel toggle: that is the one control meaningful for a
// cross-env session, and the three env-scoped ones acted on whichever
// environment the sidebar happened to have selected — independent of which
// terminal tab was active, so with an orchestrator focused they targeted an
// environment it may not even be linked to (#1178).
//
// The changed-files sub-toggle stays: it renders only when the diff panel is
// open and toggles that panel's own file tree, so it is diff-panel chrome
// rather than a fourth control.
export function TitlebarRightControls(): React.ReactElement {
  const dispatch = useAppDispatch();
  const reviewOpen = useAppSelector((state) => state.layout.reviewOpen);
  const filesOpen = useAppSelector((state) => state.layout.filesOpen);
  const orchestratorMode = useAppSelector(selectIsOrchestratorSession);
  const ReviewIcon = reviewOpen ? PanelRightClose : PanelRightOpen;

  return (
    <>
      {!orchestratorMode && <TitlebarEnvControls />}
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
