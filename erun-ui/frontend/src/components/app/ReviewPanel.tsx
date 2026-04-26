import * as React from 'react';
import { ChevronDown, ChevronRight, FileDiff, RefreshCw, Search } from 'lucide-react';

import type { ERunUIController } from '@/app/ERunUIController';
import { compactDiffError, filterDiffTree, visibleDiffTreeNodes } from '@/app/diffUtils';
import type { AppState } from '@/app/state';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { cn } from '@/lib/utils';
import type { DiffTreeNode } from '@/types';
import { DiffList, ReviewStatus } from './DiffList';
import { FileIcon } from './FileIcon';
import { IconTooltip } from './IconTooltip';

export function ReviewPanel({
  controller,
  state,
  reviewViewRef,
  reviewMainRef,
  diffListRef,
}: {
  controller: ERunUIController;
  state: AppState;
  reviewViewRef: React.RefObject<HTMLElement | null>;
  reviewMainRef: React.RefObject<HTMLDivElement | null>;
  diffListRef: React.RefObject<HTMLDivElement | null>;
}): React.ReactElement {
  return (
    <section
      ref={reviewViewRef}
      className={cn('review-view', !state.reviewOpen && 'is-hidden', !state.filesOpen && 'files-hidden')}
    >
      <div
        ref={reviewMainRef}
        className="review-main"
        onScroll={() => controller.queueVisibleDiffSelectionUpdate()}
      >
        <div ref={diffListRef} className="diff-list">
          <DiffList controller={controller} state={state} />
        </div>
      </div>
      <div
        className="files-splitter"
        role="separator"
        aria-orientation="vertical"
        aria-label="Resize changed files list"
        onMouseDown={(event) => controller.startFilesResize(event)}
      />
      <aside className="changed-files">
        <div className="changed-files-header">
          <button className="changed-files-title" type="button">
            <FileDiff aria-hidden="true" />
            Changed files <span className="changed-files-count">{state.diff?.summary?.fileCount || 0}</span>
            <ChevronDown aria-hidden="true" />
          </button>
          <div className="changed-files-actions">
            <IconTooltip label="Refresh diff">
              <Button
                className="changed-files-icon-button"
                type="button"
                variant="ghost"
                size="icon"
                aria-label="Refresh diff"
                disabled={state.diffLoading}
                onClick={() => {
                  void controller.loadReviewDiff();
                }}
              >
                <RefreshCw />
              </Button>
            </IconTooltip>
            <div className="changed-files-stats">
              <span>+{state.diff?.summary?.additions || 0}</span>
              <span>-{state.diff?.summary?.deletions || 0}</span>
            </div>
          </div>
        </div>
        <Label className="file-filter">
          <Search aria-hidden="true" />
          <Input
            className="file-filter-input"
            value={state.diffFilter}
            type="search"
            placeholder="Filter files..."
            autoComplete="off"
            onChange={(event) => controller.setDiffFilter(event.target.value)}
          />
        </Label>
        <div className="changed-file-tree">
          <ChangedFileTree controller={controller} state={state} />
        </div>
      </aside>
    </section>
  );
}

function ChangedFileTree({ controller, state }: { controller: ERunUIController; state: AppState }): React.ReactElement {
  if (state.diffLoading) {
    return <ReviewStatus>Loading...</ReviewStatus>;
  }
  if (state.diffError) {
    return <ReviewStatus>{compactDiffError(state.diffError)}</ReviewStatus>;
  }

  const tree = visibleDiffTreeNodes(filterDiffTree(state.diff?.tree || [], state.diffFilter), state.collapsedDiffDirs);
  if (tree.length === 0) {
    return <ReviewStatus>{state.diff ? 'No matching files' : 'No changes'}</ReviewStatus>;
  }

  return (
    <>
      {tree.map((node) => (
        <ChangedFileNode key={node.path} controller={controller} state={state} node={node} />
      ))}
    </>
  );
}

function ChangedFileNode({
  controller,
  state,
  node,
}: {
  controller: ERunUIController;
  state: AppState;
  node: DiffTreeNode;
}): React.ReactElement {
  const style = { '--depth': String(node.depth) } as React.CSSProperties;

  if (node.type === 'directory') {
    const collapsed = state.collapsedDiffDirs.has(node.path);
    return (
      <div className="changed-file-node">
        <button
          type="button"
          className="changed-file-row changed-file-row-directory"
          style={style}
          onClick={() => controller.toggleDiffDirectory(node.path)}
        >
          <ChevronRight className={cn('tree-chevron', !collapsed && 'is-open')} aria-hidden="true" />
          <span>{node.name}</span>
        </button>
      </div>
    );
  }

  return (
    <div className="changed-file-node">
      <button
        type="button"
        className={cn('changed-file-row changed-file-row-file', node.path === state.selectedDiffPath && 'is-selected')}
        style={style}
        data-path={node.path}
        onClick={() => controller.selectDiffPath(node.path)}
      >
        <FileIcon filePath={node.path} />
        <span>{node.name}</span>
      </button>
    </div>
  );
}
