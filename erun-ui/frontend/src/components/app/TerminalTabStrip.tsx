import { Plus, X } from 'lucide-react';
import * as React from 'react';

import { useAppDispatch, useAppSelector } from '@/app/hooks';
import { addTerminalTab, closeTerminalTab, selectTerminalTab } from '@/app/sessionThunks';
import { selectionKey } from '@/app/versionSuggestions';
import { IconTooltip } from '@/components/app/IconTooltip';
import { cn } from '@/lib/utils';

interface TerminalTab {
  sessionId: number;
  label: string;
  kind: string;
}

function activateTab(
  index: number,
  tabs: TerminalTab[],
  dispatch: ReturnType<typeof useAppDispatch>,
  focusTab: (index: number) => void,
): void {
  const id = tabs[index]?.sessionId;
  if (id !== undefined) {
    dispatch(selectTerminalTab(id));
  }
  focusTab(index);
}

function tabStripKeyDownHandler(
  tabs: TerminalTab[],
  dispatch: ReturnType<typeof useAppDispatch>,
  focusTab: (index: number) => void,
) {
  return (
    event: React.KeyboardEvent<HTMLButtonElement>,
    index: number,
    sessionId: number,
  ): void => {
    switch (event.key) {
      case 'ArrowRight':
        event.preventDefault();
        activateTab((index + 1) % tabs.length, tabs, dispatch, focusTab);
        return;
      case 'ArrowLeft':
        event.preventDefault();
        activateTab((index - 1 + tabs.length) % tabs.length, tabs, dispatch, focusTab);
        return;
      case 'Home':
        event.preventDefault();
        activateTab(0, tabs, dispatch, focusTab);
        return;
      case 'End':
        event.preventDefault();
        activateTab(tabs.length - 1, tabs, dispatch, focusTab);
        return;
      case 'Delete':
      case 'Backspace':
        if (event.metaKey || event.ctrlKey) {
          event.preventDefault();
          void dispatch(closeTerminalTab(sessionId));
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
  const tabRefs = React.useRef<(HTMLButtonElement | null)[]>([]);
  const stripBaseClass =
    'flex h-8 items-end border-b border-[oklch(0.18_0_0)] bg-[oklch(0.05_0_0)] pl-2 pr-1';

  if (!selection) {
    return <div className={stripBaseClass} aria-hidden="true" />;
  }

  const tabs = tabsByEnv[selectionKey(selection)] ?? [];
  tabRefs.current.length = tabs.length;

  const focusTab = (index: number): void => {
    const node = tabRefs.current[index];
    node?.focus();
  };

  const handleKeyDown = tabStripKeyDownHandler(tabs, dispatch, focusTab);

  return (
    <div className={stripBaseClass} role="tablist" aria-label="Open terminals">
      {tabs.map((tab, index) => (
        <Tab
          key={tab.sessionId}
          label={tab.label || `Terminal ${String(index + 1)}`}
          closeable={tab.kind === 'extra'}
          active={tab.sessionId === activeId}
          ref={(node) => {
            tabRefs.current[index] = node;
          }}
          onSelect={() => {
            dispatch(selectTerminalTab(tab.sessionId));
          }}
          onClose={() => {
            void dispatch(closeTerminalTab(tab.sessionId));
          }}
          onKeyDown={(event) => {
            handleKeyDown(event, index, tab.sessionId);
          }}
        />
      ))}
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
    </div>
  );
}

interface TabProps {
  label: string;
  closeable: boolean;
  active: boolean;
  onSelect: () => void;
  onClose: () => void;
  onKeyDown: (event: React.KeyboardEvent<HTMLButtonElement>) => void;
}

const Tab = React.forwardRef<HTMLButtonElement, TabProps>(function Tab(
  { label, closeable, active, onSelect, onClose, onKeyDown },
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
