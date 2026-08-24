import { Trash2 } from 'lucide-react';
import * as React from 'react';

import {
  clearUITrace,
  formatUITrace,
  uiTraceEntries,
  type UITraceEntry,
  uiTraceGeneration,
} from '@/app/uiTraceBuffer';
import { Button } from '@/components/ui/button';

import { useCopyAction, useStickToBottom } from './DebugPanel.hooks';
import { CopyButton } from './DebugPanel.shared';

// The UI trace buffer lives outside Redux (see uiTraceBuffer.ts), so this
// pane polls it instead of subscribing to a selector. It is context-agnostic
// and stays available in every DiagnosticsContext.
export function UITracePane(): React.ReactElement {
  const [entries, setEntries] = React.useState<UITraceEntry[]>(() => uiTraceEntries());
  const generationRef = React.useRef(uiTraceGeneration());

  React.useEffect(() => {
    const timer = window.setInterval(() => {
      const generation = uiTraceGeneration();
      if (generation !== generationRef.current) {
        generationRef.current = generation;
        setEntries(uiTraceEntries());
      }
    }, 500);
    return () => {
      window.clearInterval(timer);
    };
  }, []);

  const text = formatUITrace(entries);
  const { outputRef, handleScroll } = useStickToBottom(text);
  const { copyStatus, copy } = useCopyAction(text);

  // Actions are pinned outside the scroll region — same rationale as the
  // erun trace toolbar.
  return (
    <div className="grid min-h-0 grid-rows-[auto_minmax(0,1fr)]">
      <div className="flex items-center justify-end gap-1 px-3 pt-1.5 pb-1">
        <CopyButton copyStatus={copyStatus} disabled={!text.trim()} onCopy={copy} />
        <Button
          className="h-6 px-2 text-[11px] [&_svg]:size-3.5"
          type="button"
          variant="ghost"
          size="sm"
          onClick={() => {
            clearUITrace();
            setEntries([]);
          }}
        >
          <Trash2 aria-hidden="true" />
          Clear
        </Button>
      </div>
      <div
        ref={outputRef}
        onScroll={handleScroll}
        className="min-h-0 overflow-auto px-3 pb-2 font-mono text-[11px] leading-[1.35] text-[oklch(0.82_0_0)]"
        aria-label="UI trace output"
      >
        {entries.length === 0 ? (
          <p className="m-0 text-[oklch(0.6_0_0)]">No UI activity recorded yet.</p>
        ) : (
          <pre className="m-0 font-mono text-[11px] leading-[1.35] break-words whitespace-pre-wrap">
            {text}
          </pre>
        )}
      </div>
    </div>
  );
}
