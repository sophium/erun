import { CheckCircle2, Copy, RefreshCw, Trash2 } from 'lucide-react';
import * as React from 'react';

import { Button } from '@/components/ui/button';

import { useCopyAction } from './DebugPanel.hooks';

// Shared pane chrome every Diagnostics console tab reuses: a Copy button
// and the toolbar row that sits outside the scroll region so it can never be
// pushed out of view by always-on capture.

export function CopyButton({
  copyStatus,
  disabled,
  onCopy,
}: {
  copyStatus: string;
  disabled: boolean;
  onCopy: () => void;
}): React.ReactElement {
  return (
    <Button
      className="h-6 px-2 text-[11px] [&_svg]:size-3.5"
      type="button"
      variant="ghost"
      size="sm"
      disabled={disabled}
      onClick={onCopy}
    >
      {copyStatus === 'Copied' ? <CheckCircle2 aria-hidden="true" /> : <Copy aria-hidden="true" />}
      {copyStatus || 'Copy'}
    </Button>
  );
}

// clear is omitted entirely (rather than rendered disabled) for panes with no
// clear behavior at all (the app/orchestrator log): a permanently-disabled
// button with no explanation reads as broken, not as "not applicable here".
export function PrimaryPaneToolbar({
  label,
  content,
  clear,
  onRefresh,
}: {
  label: string;
  content: string;
  clear?: { canClear: boolean; onClear: () => void };
  onRefresh: () => void;
}): React.ReactElement {
  // Copy reads the full content, never a baselined/cleared view — a cleared
  // pane must never produce a truncated bug report.
  const { copyStatus, copy } = useCopyAction(content);
  return (
    <div className="flex items-center justify-between gap-2 px-3 pt-1.5 pb-1">
      <span className="min-w-0 truncate text-[10px] text-[oklch(0.5_0_0)]">{label}</span>
      <span className="flex flex-none items-center gap-1">
        <Button
          className="h-6 px-2 text-[11px] [&_svg]:size-3.5"
          type="button"
          variant="ghost"
          size="sm"
          onClick={onRefresh}
        >
          <RefreshCw aria-hidden="true" />
          Refresh
        </Button>
        <CopyButton copyStatus={copyStatus} disabled={!content.trim()} onCopy={copy} />
        {clear && (
          <Button
            className="h-6 px-2 text-[11px] [&_svg]:size-3.5"
            type="button"
            variant="ghost"
            size="sm"
            disabled={!clear.canClear}
            onClick={clear.onClear}
          >
            <Trash2 aria-hidden="true" />
            Clear
          </Button>
        )}
      </span>
    </div>
  );
}
