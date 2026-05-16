import * as React from 'react';
import { ChevronDown, ChevronRight, FileDiff, GitBranch, GitCommitHorizontal, RefreshCw, Search } from 'lucide-react';

import { useController } from '@/app/ControllerContext';
import { compactDiffError, filterDiffTree, visibleDiffTreeNodes } from '@/app/diffUtils';
import { useAppSelector } from '@/app/hooks';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { cn } from '@/lib/utils';
import type { DiffCommit, DiffTreeNode } from '@/types';
import { DiffErrorAlert, DiffList, ReviewStatus } from './DiffList';
import { FileIcon } from './FileIcon';
import { IconTooltip } from './IconTooltip';

const filesSplitterClassName =
  'relative cursor-col-resize border-l bg-background before:absolute before:top-0 before:bottom-0 before:left-1 before:w-px before:bg-transparent before:transition-colors hover:before:bg-border [.is-resizing-files_&]:before:bg-border';

export function ReviewPanel({
  reviewViewRef,
  reviewMainRef,
  diffListRef,
}: {
  reviewViewRef: React.RefObject<HTMLElement | null>;
  reviewMainRef: React.RefObject<HTMLDivElement | null>;
  diffListRef: React.RefObject<HTMLDivElement | null>;
}): React.ReactElement {
  const controller = useController();
  const filesOpen = useAppSelector((state) => state.layout.filesOpen);
  const reviewOpen = useAppSelector((state) => state.layout.reviewOpen);
  const filesVisible = filesOpen && reviewOpen;
  return (
    <section
      ref={reviewViewRef}
      className={reviewPanelClassName(reviewOpen, filesOpen)}
    >
      <div
        ref={reviewMainRef}
        className="h-full min-h-0 min-w-0 overflow-auto overscroll-contain bg-background"
        onScroll={() => controller.queueVisibleDiffSelectionUpdate()}
      >
        <div ref={diffListRef} className="flex flex-col gap-3.5 px-[18px] pt-5 pb-[34px]">
          <DiffList />
        </div>
      </div>
      <ChangedFilesSplitter visible={filesVisible} onMouseDown={(event) => controller.startFilesResize(event)} />
      <ChangedFilesAside visible={filesVisible} />
    </section>
  );
}

function reviewPanelClassName(reviewOpen: boolean, filesOpen: boolean): string {
  const gridClassName = filesOpen
    ? 'grid-cols-[minmax(260px,1fr)_10px_minmax(220px,var(--files-width))] max-[980px]:grid-cols-[minmax(0,1fr)]'
    : 'grid-cols-[minmax(0,1fr)]';
  return cn('relative grid h-full min-h-0 w-full min-w-0 overflow-hidden bg-background text-foreground', gridClassName, !reviewOpen && 'hidden');
}

function ChangedFilesSplitter({ visible, onMouseDown }: { visible: boolean; onMouseDown: React.MouseEventHandler<HTMLDivElement> }): React.ReactElement {
  return (
    <div
      className={cn(filesSplitterClassName, !visible && 'hidden', 'max-[980px]:hidden')}
      role="separator"
      aria-orientation="vertical"
      aria-label="Resize changed files list"
      onMouseDown={onMouseDown}
    />
  );
}

function ChangedFilesAside({ visible }: { visible: boolean }): React.ReactElement {
  const controller = useController();
  const changedFilesOpen = useAppSelector((state) => state.layout.changedFilesOpen);
  const diffFilter = useAppSelector((state) => state.review.diffFilter);
  return (
    <aside
      className={cn(
        'box-border flex h-full min-h-0 min-w-0 flex-col overflow-hidden border-l bg-background px-[18px] py-5',
        !visible && 'hidden',
        'max-[980px]:hidden',
      )}
    >
      <ChangedFilesHeader />
      <ReviewRangeControl />
      {changedFilesOpen ? (
        <>
          <Label className="box-border flex h-[38px] items-center gap-2 rounded-[var(--radius)] border border-input bg-background px-3 text-muted-foreground [&_svg]:size-[18px] [&_svg]:flex-none">
            <Search aria-hidden="true" />
            <Input
              className="h-auto min-w-0 flex-1 border-0 bg-transparent p-0 text-sm text-foreground shadow-none outline-none placeholder:text-muted-foreground focus-visible:border-0 focus-visible:ring-0"
              value={diffFilter}
              type="search"
              placeholder="Filter files..."
              autoComplete="off"
              onChange={(event) => controller.setDiffFilter(event.target.value)}
            />
          </Label>
          <div className="min-h-0 flex-1 overflow-auto overscroll-contain pt-3.5">
            <ChangedFileTree />
          </div>
        </>
      ) : null}
    </aside>
  );
}

function ReviewRangeControl(): React.ReactElement | null {
  const controller = useController();
  const diff = useAppSelector((state) => state.review.diff);
  const selectedReviewScope = useAppSelector((state) => state.review.selectedReviewScope);
  const diffLoading = useAppSelector((state) => state.review.diffLoading);
  const commits = [...(diff?.reviewCommits || [])].reverse();
  const base = diff?.reviewBase;
  if (!base?.commit && commits.length === 0) {
    return null;
  }

  return (
    <div className="mb-3.5 flex min-h-0 flex-col gap-2 border-b border-border pb-3.5">
      <div className="flex min-w-0 flex-col gap-0.5">
        <div className="text-xs font-semibold text-foreground">Review layers</div>
        <div className="text-[11px] leading-4 text-muted-foreground">Newest changes first. Each lower layer includes more history.</div>
      </div>
      <div className="flex min-w-0 items-center gap-2 text-xs text-muted-foreground">
        <GitBranch className="size-3.5 flex-none" aria-hidden="true" />
        <span className="flex-none">Merge target</span>
        <span className="min-w-0 truncate font-medium text-foreground">{base?.branch || 'branch base'}</span>
        {base?.shortCommit ? <span className="flex-none font-mono">{base.shortCommit}</span> : null}
      </div>
      <div className="relative flex min-h-0 flex-col gap-1 before:absolute before:top-4 before:bottom-4 before:left-[15px] before:w-px before:bg-border">
        <ReviewBoundaryButton
          label="Current local changes"
          detail="local only"
          selected={selectedReviewScope === 'current'}
          disabled={diffLoading}
          onClick={() => controller.selectReviewRange('current')}
        />
        {commits.length > 0 ? (
          <div className="flex max-h-[220px] min-h-0 flex-col gap-1 overflow-auto pr-1">
            {commits.map((commit) => (
              <ReviewCommitButton key={commit.hash} commit={commit} />
            ))}
          </div>
        ) : null}
        <ReviewBoundaryButton
          label="All branch changes"
          detail="base..current"
          selected={selectedReviewScope === 'all'}
          disabled={diffLoading}
          onClick={() => controller.selectReviewRange('all')}
        />
      </div>
    </div>
  );
}

function ReviewCommitButton({ commit }: { commit: DiffCommit }): React.ReactElement {
  const controller = useController();
  const selectedReviewScope = useAppSelector((state) => state.review.selectedReviewScope);
  const selectedReviewCommit = useAppSelector((state) => state.review.selectedReviewCommit);
  const diffLoading = useAppSelector((state) => state.review.diffLoading);
  return (
    <ReviewBoundaryButton
      label={commit.subject || commit.shortHash}
      detail={`from ${commit.shortHash}`}
      selected={selectedReviewScope === 'commit' && selectedReviewCommit === commit.hash}
      disabled={diffLoading}
      onClick={() => controller.selectReviewRange('commit', commit.hash)}
    />
  );
}

function ReviewBoundaryButton({
  label,
  detail,
  selected,
  disabled,
  onClick,
}: {
  label: string;
  detail: string;
  selected: boolean;
  disabled: boolean;
  onClick: () => void;
}): React.ReactElement {
  return (
    <button
      type="button"
      className={cn(
        'relative grid h-8 w-full cursor-pointer grid-cols-[16px_minmax(0,1fr)_auto] items-center gap-2 rounded-[var(--radius)] border-0 bg-background px-2 text-left text-xs text-foreground hover:bg-accent disabled:cursor-default disabled:opacity-60',
        selected && 'bg-primary text-primary-foreground hover:bg-primary',
      )}
      disabled={disabled}
      aria-pressed={selected}
      onClick={onClick}
    >
      <GitCommitHorizontal className="size-3.5 flex-none" aria-hidden="true" />
      <span className="min-w-0 truncate">{label}</span>
      <span className={cn('flex-none font-mono text-[11px]', selected ? 'text-primary-foreground/80' : 'text-muted-foreground')}>{detail}</span>
    </button>
  );
}

function ChangedFilesHeader(): React.ReactElement {
  const controller = useController();
  const changedFilesOpen = useAppSelector((state) => state.layout.changedFilesOpen);
  const diff = useAppSelector((state) => state.review.diff);
  const diffLoading = useAppSelector((state) => state.review.diffLoading);
  return (
    <div className="mb-3.5 flex min-w-0 items-center justify-between gap-3">
      <button
        className="inline-flex min-w-0 flex-1 cursor-pointer items-center gap-1 overflow-hidden border-0 bg-transparent p-0 text-sm font-semibold whitespace-nowrap text-foreground [&_svg]:size-4 [&_svg]:flex-none [&_svg]:text-muted-foreground"
        type="button"
        aria-expanded={changedFilesOpen}
        onClick={() => controller.toggleChangedFiles()}
      >
        <FileDiff aria-hidden="true" />
        Changed files <span className="flex-none text-muted-foreground">{diff?.summary?.fileCount || 0}</span>
        <ChevronDown className={cn('transition-transform', !changedFilesOpen && '-rotate-90')} aria-hidden="true" />
      </button>
      <div className="flex min-w-0 flex-none items-center gap-2">
        <IconTooltip label="Refresh diff">
          <Button
            className="size-7 cursor-pointer border-0 bg-transparent text-muted-foreground hover:bg-accent hover:text-accent-foreground disabled:cursor-default disabled:opacity-55 [&_svg]:size-[17px]"
            type="button"
            variant="ghost"
            size="icon"
            aria-label="Refresh diff"
            disabled={diffLoading}
            onClick={() => {
              void controller.refreshReviewDiff();
            }}
          >
            <RefreshCw />
          </Button>
        </IconTooltip>
        <div className="flex gap-1.5 text-sm font-semibold whitespace-nowrap">
          <span className="text-diff-add-foreground">+{diff?.summary?.additions || 0}</span>
          <span className="text-diff-delete-foreground">-{diff?.summary?.deletions || 0}</span>
        </div>
      </div>
    </div>
  );
}

function ChangedFileTree(): React.ReactElement {
  const controller = useController();
  const review = useAppSelector((state) => state.review);
  if (review.diffLoading) {
    return <ReviewStatus>Loading...</ReviewStatus>;
  }
  if (review.diffError) {
    return (
      <DiffErrorAlert
        message={compactDiffError(review.diffError)}
        loading={review.diffLoading}
        reconnectable={review.diffErrorReconnectable}
        onRetry={() => { void controller.loadReviewDiff(); }}
        onReconnect={() => controller.requestReconnect()}
      />
    );
  }

  const tree = visibleDiffTreeNodes(filterDiffTree(review.diff?.tree || [], review.diffFilter), new Set(review.collapsedDiffDirs));
  if (tree.length === 0) {
    return <ReviewStatus>{review.diff ? 'No matching files' : 'No changes'}</ReviewStatus>;
  }

  return (
    <>
      {tree.map((node) => (
        <ChangedFileNode key={node.path} node={node} />
      ))}
    </>
  );
}

function ChangedFileNode({
  node,
}: {
  node: DiffTreeNode;
}): React.ReactElement {
  const controller = useController();
  const collapsedDiffDirs = useAppSelector((state) => state.review.collapsedDiffDirs);
  const selectedDiffPath = useAppSelector((state) => state.review.selectedDiffPath);
  const style = { '--depth': String(node.depth) } as React.CSSProperties;

  if (node.type === 'directory') {
    const collapsed = collapsedDiffDirs.includes(node.path);
    return (
      <div className="flex flex-col">
        <button
          type="button"
          className="flex h-[34px] w-full cursor-pointer items-center gap-2 rounded-[var(--radius)] border-0 bg-transparent py-0 pr-2.5 pl-[calc(8px+(var(--depth)*18px))] text-left text-sm leading-[1.2] font-medium text-foreground hover:bg-accent"
          style={style}
          aria-expanded={!collapsed}
          aria-label={`${node.name} directory`}
          onClick={() => controller.toggleDiffDirectory(node.path)}
        >
          <ChevronRight className={cn('size-4 flex-none text-current', !collapsed && 'rotate-90')} aria-hidden="true" />
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
        onClick={() => controller.selectDiffPath(node.path)}
      >
        <FileIcon filePath={node.path} />
        <span className="min-w-0 truncate">{node.name}</span>
      </button>
    </div>
  );
}
