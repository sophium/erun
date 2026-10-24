import { cn, IconTooltip } from 'erun-kit';
import { GitFork, Plus, X } from 'lucide-react';
import * as React from 'react';

import { useAppDispatch, useAppSelector } from '@/app/hooks';
import { openOrchestrator, stopOrchestrator } from '@/app/orchestratorThunks';
import { selectIsOrchestratorSession } from '@/app/selectors';
import { addTerminalTab, closeTerminalTab, selectTerminalTab } from '@/app/sessionThunks';
import { selectionKey } from '@/app/versionSuggestions';

// StripTab is shared by env tabs and cross-env orchestrator sessions.
interface StripTab {
  key: string;
  label: string;
  sessionId: number;
  icon?: 'contribute';
  closeable: boolean;
  onSelect: () => void;
  onClose: () => void;
}

function activateTab(index: number, tabs: StripTab[], focusTab: (index: number) => void): void {
  tabs[index]?.onSelect();
  focusTab(index);
}

function tabStripKeyDownHandler(tabs: StripTab[], focusTab: (index: number) => void) {
  return (event: React.KeyboardEvent<HTMLButtonElement>, index: number): void => {
    switch (event.key) {
      case 'ArrowRight':
        event.preventDefault();
        activateTab((index + 1) % tabs.length, tabs, focusTab);
        return;
      case 'ArrowLeft':
        event.preventDefault();
        activateTab((index - 1 + tabs.length) % tabs.length, tabs, focusTab);
        return;
      case 'Home':
        event.preventDefault();
        activateTab(0, tabs, focusTab);
        return;
      case 'End':
        event.preventDefault();
        activateTab(tabs.length - 1, tabs, focusTab);
        return;
      case 'Delete':
      case 'Backspace':
        if (event.metaKey || event.ctrlKey) {
          event.preventDefault();
          tabs[index]?.onClose();
        }
        return;
      default:
        return;
    }
  };
}

export function TerminalTabStrip(): React.ReactElement {
  const dispatch = useAppDispatch();
  const selection = useAppSelector((state) => state.selection.selected);
  const tabsByEnv = useAppSelector((state) => state.terminal.tabsByEnv);
  const activeId = useAppSelector((state) => state.terminal.sessionId);
  const orchestrators = useAppSelector((state) => state.orchestrators.items);
  const tabRefs = React.useRef<(HTMLButtonElement | null)[]>([]);
  const stripBaseClass =
    'flex h-8 items-end border-b border-[oklch(0.18_0_0)] bg-[oklch(0.05_0_0)] pl-2 pr-1';

  // Orchestrators are cross-env, so the strip lists them instead of the selected env's tabs.
  const orchestratorMode = useAppSelector(selectIsOrchestratorSession);

  let tabs: StripTab[];
  let showNewTerminal = false;
  if (orchestratorMode) {
    tabs = orchestrators.map((orchestrator) => ({
      key: orchestrator.id,
      label: orchestrator.name,
      sessionId: orchestrator.sessionId,
      closeable: true,
      onSelect: () => {
        dispatch(openOrchestrator(orchestrator.sessionId));
      },
      onClose: () => {
        void dispatch(stopOrchestrator(orchestrator.id));
      },
    }));
  } else if (selection) {
    tabs = (tabsByEnv[selectionKey(selection)] ?? []).map((tab, index) => ({
      key: String(tab.sessionId),
      label: tab.label || `Terminal ${String(index + 1)}`,
      sessionId: tab.sessionId,
      icon:
        tab.kind === 'contribute-erun' || tab.kind === 'contribute-ai' ? 'contribute' : undefined,
      closeable: tab.kind === 'extra',
      onSelect: () => {
        dispatch(selectTerminalTab(tab.sessionId));
      },
      onClose: () => {
        void dispatch(closeTerminalTab(tab.sessionId));
      },
    }));
    showNewTerminal = true;
  } else {
    return <div className={stripBaseClass} aria-hidden="true" />;
  }

  tabRefs.current.length = tabs.length;
  const focusTab = (index: number): void => {
    tabRefs.current[index]?.focus();
  };
  const handleKeyDown = tabStripKeyDownHandler(tabs, focusTab);

  return (
    <div
      className={stripBaseClass}
      role="tablist"
      aria-label={orchestratorMode ? 'Orchestrators' : 'Open terminals'}
    >
      {tabs.map((tab, index) => (
        <Tab
          key={tab.key}
          label={tab.label}
          icon={tab.icon}
          closeable={tab.closeable}
          active={tab.sessionId === activeId}
          ref={(node) => {
            tabRefs.current[index] = node;
          }}
          onSelect={tab.onSelect}
          onClose={tab.onClose}
          onKeyDown={(event) => {
            handleKeyDown(event, index);
          }}
        />
      ))}
      {showNewTerminal && (
        <IconTooltip label="New terminal">
          <button
            type="button"
            className="ml-1 mb-[-1px] flex h-7 items-center justify-center rounded-md px-2 text-[oklch(0.66_0_0)] transition-colors hover:bg-[oklch(0.13_0_0)] hover:text-[oklch(0.96_0_0)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[oklch(0.62_0_0)]"
            aria-label="Open a new terminal"
            onClick={() => {
              void dispatch(addTerminalTab());
            }}
          >
            <Plus className="size-4" aria-hidden="true" />
          </button>
        </IconTooltip>
      )}
    </div>
  );
}

interface TabProps {
  label: string;
  icon?: 'contribute';
  closeable: boolean;
  active: boolean;
  onSelect: () => void;
  onClose: () => void;
  onKeyDown: (event: React.KeyboardEvent<HTMLButtonElement>) => void;
}

const Tab = React.forwardRef<HTMLButtonElement, TabProps>(function Tab(
  { label, icon, closeable, active, onSelect, onClose, onKeyDown },
  ref,
) {
  return (
    <div
      className={cn(
        'group relative -mb-px flex h-7 items-stretch overflow-hidden rounded-t-md border border-b-0 transition-colors',
        active
          ? 'border-[oklch(0.22_0_0)] bg-terminal text-[oklch(0.96_0_0)]'
          : 'border-transparent bg-transparent text-[oklch(0.66_0_0)] hover:bg-[oklch(0.10_0_0)] hover:text-[oklch(0.92_0_0)]',
      )}
    >
      <button
        ref={ref}
        type="button"
        role="tab"
        aria-selected={active}
        aria-controls="erun-terminal-pane"
        tabIndex={active ? 0 : -1}
        className={cn(
          'flex items-center pl-3 pr-1.5 text-[12px] leading-none font-medium tracking-tight bg-transparent border-0 outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-[oklch(0.62_0_0)]',
          active ? 'cursor-default' : 'cursor-pointer',
        )}
        onClick={onSelect}
        onKeyDown={onKeyDown}
      >
        {icon === 'contribute' && (
          <GitFork className="mr-1 size-3.5 text-[oklch(0.7_0.18_180)]" aria-hidden="true" />
        )}
        {label}
      </button>
      {closeable && (
        <IconTooltip label={`Close ${label}`}>
          <button
            type="button"
            tabIndex={-1}
            className={cn(
              'mr-1 flex size-5 items-center justify-center rounded text-[oklch(0.56_0_0)] transition-opacity transition-colors hover:bg-[oklch(0.18_0_0)] hover:text-[oklch(0.96_0_0)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[oklch(0.62_0_0)]',
              active
                ? 'opacity-100'
                : 'opacity-0 group-hover:opacity-100 group-focus-within:opacity-100',
            )}
            aria-label={`Close ${label}`}
            onClick={(event) => {
              event.stopPropagation();
              onClose();
            }}
          >
            <X className="size-3" aria-hidden="true" />
          </button>
        </IconTooltip>
      )}
    </div>
  );
});
