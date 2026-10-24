import { Button } from 'erun-kit';
import { CheckCircle2, Copy, RefreshCw, Trash2 } from 'lucide-react';
import * as React from 'react';

import { useCopyAction } from './DebugPanel.hooks';

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

// Panes with no clear behaviour omit the button; a permanently disabled one reads as broken.
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
  // Copy reads the full content, so a cleared pane never truncates a report.
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
