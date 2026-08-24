import { ChevronRight } from 'lucide-react';
import * as React from 'react';

import { filterDiffTree, visibleDiffTreeNodes } from '@/app/diffUtils';
import { useAppDispatch, useAppSelector } from '@/app/hooks';
import { selectDiffPath, toggleDiffDirectory } from '@/app/reviewThunks';
import { selectReviewEnvTargets } from '@/app/selectors';
import { diffPathKey } from '@/app/slices/reviewSlice';
import { useEnvDiffSlot } from '@/app/useEnvDiffSlot';
import { ReviewStatus } from '@/components/app/DiffList';
import { FileIcon } from '@/components/app/FileIcon';
import { cn } from '@/lib/utils';
import type { DiffTreeNode } from '@/types';

export function ChangedFileTree(): React.ReactElement {
  const targets = useAppSelector(selectReviewEnvTargets);
  if (targets.length === 0) {
    return <ReviewStatus>No environment selected</ReviewStatus>;
  }
  const multi = targets.length > 1;
  return (
    <>
      {targets.map((target) => (
        <ChangedFileTreeSection key={target.envKey} envKey={target.envKey} showHeader={multi} />
      ))}
    </>
  );
}

// ChangedFileTreeSection owns one environment's tree and loading state. A
// stopped environment renders its own empty state in its own section while
// every other environment keeps rendering -- the single-slot version cleared
// the one shared diff, so one unreachable env blanked them all (#1178).
//
// It deliberately does NOT duplicate DiffList's actionable DiffErrorAlert: the
// two surfaces render the same env slot, and rendering the full alert (with
// its own Retry/Reconnect/Open buttons) in both the tree aside and the main
// diff panel at once read as two unrelated reports of the same outage
// (#1230). The tree shows a short status line instead and points at the diff
// panel, which keeps the one actionable report.
function ChangedFileTreeSection({
  envKey,
  showHeader,
}: {
  envKey: string;
  showHeader: boolean;
}): React.ReactElement {
  const slot = useEnvDiffSlot(envKey);
  const diffFilter = useAppSelector((state) => state.review.diffFilter);
  const collapsedDiffDirs = useAppSelector((state) => state.review.collapsedDiffDirs);

  const header = showHeader ? (
    <div className="px-1 pt-2 pb-1 text-[11px] font-medium text-muted-foreground">{envKey}</div>
  ) : null;

  const body = ((): React.ReactElement => {
    if (slot.loading) {
      return <ReviewStatus>Loading...</ReviewStatus>;
    }
    if (slot.error) {
      const notRunning = slot.errorReconnectable && slot.errorKind === 'not-open';
      return (
        <ReviewStatus>
          {notRunning
            ? 'Environment not running — open it from the diff panel.'
            : 'Diff unavailable — see the diff panel for details.'}
        </ReviewStatus>
      );
    }
    // Collapsed directories are env-keyed, so a collapsed "app/" in one
    // environment cannot hide a same-named directory in another.
    const collapsedForEnv = new Set(
      collapsedDiffDirs
        .filter((entry) => entry.startsWith(`${envKey}:`))
        .map((entry) => entry.slice(envKey.length + 1)),
    );
    const tree = visibleDiffTreeNodes(
      filterDiffTree(slot.diff?.tree ?? [], diffFilter),
      collapsedForEnv,
    );
    if (tree.length === 0) {
      return <ReviewStatus>{slot.diff ? 'No matching files' : 'No changes'}</ReviewStatus>;
    }
    return (
      <>
        {tree.map((node) => (
          <ChangedFileNode key={node.path} envKey={envKey} node={node} />
        ))}
      </>
    );
  })();

  return (
    <>
      {header}
      {body}
    </>
  );
}

function ChangedFileNode({
  envKey,
  node,
}: {
  envKey: string;
  node: DiffTreeNode;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const collapsedDiffDirs = useAppSelector((state) => state.review.collapsedDiffDirs);
  const selectedDiffPath = useAppSelector((state) => state.review.selectedDiffPath);
  const nodeKey = diffPathKey(envKey, node.path);
  const style = { '--depth': String(node.depth) } as React.CSSProperties;

  if (node.type === 'directory') {
    const collapsed = collapsedDiffDirs.includes(nodeKey);
    return (
      <div className="flex flex-col">
        <button
          type="button"
          className="flex h-[34px] w-full cursor-pointer items-center gap-2 rounded-[var(--radius)] border-0 bg-transparent py-0 pr-2.5 pl-[calc(8px+(var(--depth)*18px))] text-left text-sm leading-[1.2] font-medium text-foreground hover:bg-accent"
          style={style}
          aria-expanded={!collapsed}
          aria-label={`${node.name} directory`}
          onClick={() => {
            dispatch(toggleDiffDirectory(nodeKey));
          }}
        >
          <ChevronRight
            className={cn('size-4 flex-none text-current', !collapsed && 'rotate-90')}
            aria-hidden="true"
          />
          <span className="min-w-0 truncate">{node.name}</span>
        </button>
      </div>
    );
  }

  const selected = node.path === selectedDiffPath;
  return (
    <div className="flex flex-col">
      <button
        type="button"
        className={cn(
          'flex h-[34px] w-full cursor-pointer items-center gap-2 rounded-[var(--radius)] border-0 bg-transparent py-0 pr-2.5 pl-[calc(8px+(var(--depth)*18px))] text-left text-sm leading-[1.2] text-foreground hover:bg-accent',
          selected && 'bg-primary text-primary-foreground hover:bg-primary',
        )}
        style={style}
        data-path={node.path}
        aria-current={selected ? 'true' : undefined}
        onClick={() => {
          dispatch(selectDiffPath(nodeKey));
        }}
      >
        <FileIcon filePath={node.path} />
        <span className="min-w-0 truncate">{node.name}</span>
      </button>
    </div>
  );
}
