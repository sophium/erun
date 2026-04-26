import * as React from 'react';

import type { ERunUIController } from '@/app/ERunUIController';
import { compactDiffError, diffLineMark } from '@/app/diffUtils';
import type { AppState } from '@/app/state';
import { cn } from '@/lib/utils';
import type { DiffFile, DiffHunk } from '@/types';

export function DiffList({ controller, state }: { controller: ERunUIController; state: AppState }): React.ReactElement {
  if (state.diffLoading) {
    return <ReviewStatus>Loading diff...</ReviewStatus>;
  }
  if (state.diffError) {
    return <ReviewStatus>{compactDiffError(state.diffError)}</ReviewStatus>;
  }
  const files = state.diff?.files || [];
  if (files.length === 0) {
    return <ReviewStatus>No changes</ReviewStatus>;
  }
  return (
    <>
      {files.map((file) => (
        <DiffFileView key={file.path} file={file} selected={file.path === state.selectedDiffPath} />
      ))}
      <span className="sr-only">{controller.state.selectedDiffPath}</span>
    </>
  );
}

function DiffFileView({ file, selected }: { file: DiffFile; selected: boolean }): React.ReactElement {
  return (
    <section className={cn('diff-file', selected && 'is-selected')} data-path={file.path}>
      <header className="diff-file-header">
        <span className="diff-file-path">{file.path}</span>
        <span className="diff-file-counts">
          <span>+{file.additions}</span> <span>-{file.deletions}</span>
        </span>
      </header>
      {file.binary ? (
        <ReviewStatus>Binary file changed</ReviewStatus>
      ) : (
        (file.hunks || []).map((hunk) => <DiffHunkView key={hunk.header} hunk={hunk} />)
      )}
    </section>
  );
}

function DiffHunkView({ hunk }: { hunk: DiffHunk }): React.ReactElement {
  const contentWidth = Math.max(1, ...(hunk.lines || []).map((line) => line.content?.length || 0));
  const style = { '--diff-content-width': `${contentWidth + 2}ch` } as React.CSSProperties;

  return (
    <div className="diff-hunk">
      <div className="diff-hunk-header">{hunk.header}</div>
      <div className="diff-hunk-body" style={style}>
        {(hunk.lines || []).map((line, index) => (
          <div key={`${line.oldLine || ''}:${line.newLine || ''}:${index}`} className={`diff-line diff-line-${line.kind}`}>
            <span className="diff-line-old">{line.oldLine || ''}</span>
            <span className="diff-line-new">{line.newLine || ''}</span>
            <span className="diff-line-mark">{diffLineMark(line.kind)}</span>
            <span className="diff-line-content">{line.content || ' '}</span>
          </div>
        ))}
      </div>
    </div>
  );
}

export function ReviewStatus({ children }: { children: React.ReactNode }): React.ReactElement {
  return <div className="review-status">{children}</div>;
}
