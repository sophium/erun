import { CheckCircle2, ChevronDown, ChevronUp, Copy, Trash2 } from 'lucide-react';
import * as React from 'react';

import { readError } from '@/app/errors';
import { useAppDispatch, useAppSelector } from '@/app/hooks';
import { clearDebugOutput, setDebugOpen, startDebugResize } from '@/app/layoutThunks';
import { setDebugCopyStatus } from '@/app/slices/terminalStatusSlice';
import { ResizeHandle } from '@/components/app/ResizeHandle';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';

import { ClipboardSetText } from '../../../wailsjs/runtime/runtime';

const debugSplitterClassName =
  'relative cursor-row-resize bg-[oklch(0.06_0_0)] before:absolute before:left-0 before:right-0 before:top-1 before:h-px before:bg-transparent before:transition-colors hover:before:bg-[oklch(0.36_0_0)] [.is-resizing-debug_&]:before:bg-[oklch(0.46_0_0)]';

function useDebugPanelScrollBinding(
  open: boolean,
  sessionId: number,
  output: string,
): {
  outputRef: React.RefObject<HTMLDivElement | null>;
  handleScroll: React.UIEventHandler<HTMLDivElement>;
} {
  const outputRef = React.useRef<HTMLDivElement>(null);
  const stuckToBottomRef = React.useRef(true);

  const handleScroll = React.useCallback(() => {
    const el = outputRef.current;
    if (!el) {
      return;
    }
    const distanceFromBottom = el.scrollHeight - el.scrollTop - el.clientHeight;
    stuckToBottomRef.current = distanceFromBottom <= 4;
  }, []);

  // When the active session changes, reset stick-to-bottom and snap to the
  // bottom of the new buffer so users see the latest debug output for that tab.
  React.useEffect(() => {
    stuckToBottomRef.current = true;
    if (open && outputRef.current) {
      outputRef.current.scrollTop = outputRef.current.scrollHeight;
    }
  }, [open, sessionId]);

  React.useEffect(() => {
    if (!open) {
      return;
    }
    const el = outputRef.current;
    if (!el) {
      return;
    }
    if (stuckToBottomRef.current) {
      el.scrollTop = el.scrollHeight;
    }
  }, [open, output]);

  return { outputRef, handleScroll };
}

function useDebugCopyAction(
  output: string,
  canCopy: boolean,
): { copyStatus: string; copyDebugOutput: () => void } {
  const dispatch = useAppDispatch();
  const copyStatus = useAppSelector((state) => state.terminalStatus.debugCopyStatus);

  React.useEffect(() => {
    dispatch(setDebugCopyStatus(''));
  }, [dispatch, output]);

  const copyDebugOutput = React.useCallback(() => {
    if (!canCopy) {
      return;
    }
    void ClipboardSetText(output)
      .then(() => {
        dispatch(setDebugCopyStatus('Copied'));
        window.setTimeout(() => dispatch(setDebugCopyStatus('')), 1400);
      })
      .catch((error: unknown) => dispatch(setDebugCopyStatus(readError(error))));
  }, [canCopy, dispatch, output]);

  return { copyStatus, copyDebugOutput };
}

export function DebugPanel({
  open,
  output,
  sessionId,
  verbose,
}: {
  open: boolean;
  output: string;
  sessionId: number;
  verbose: boolean;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const canCopy = output.trim().length > 0;
  const { outputRef, handleScroll } = useDebugPanelScrollBinding(open, sessionId, output);
  const { copyStatus, copyDebugOutput } = useDebugCopyAction(output, canCopy);

  return (
    <section
      className={cn(
        'grid min-h-0 border-t border-[oklch(0.26_0_0)] bg-[oklch(0.06_0_0)] text-[oklch(0.86_0_0)]',
        open ? 'grid-rows-[6px_34px_minmax(0,1fr)]' : 'grid-rows-[34px]',
      )}
    >
      {open && (
        <ResizeHandle
          className={debugSplitterClassName}
          orientation="horizontal"
          label="Resize debug panel"
          onMouseDown={(event) => {
            dispatch(startDebugResize(event));
          }}
        />
      )}
      <DebugPanelHeader
        open={open}
        canCopy={canCopy}
        copyStatus={copyStatus}
        copyDebugOutput={copyDebugOutput}
      />
      {open && (
        <DebugPanelOutput
          outputRef={outputRef}
          handleScroll={handleScroll}
          output={output}
          sessionId={sessionId}
          verbose={verbose}
        />
      )}
    </section>
  );
}

function DebugPanelHeader({
  open,
  canCopy,
  copyStatus,
  copyDebugOutput,
}: {
  open: boolean;
  canCopy: boolean;
  copyStatus: string;
  copyDebugOutput: () => void;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  return (
    <div className="flex h-[34px] items-center justify-between gap-2 border-b border-[oklch(0.18_0_0)] px-3">
      <button
        type="button"
        className="flex min-w-0 items-center gap-2 border-0 bg-transparent p-0 text-xs font-medium tracking-normal text-[oklch(0.76_0_0)]"
        onClick={() => {
          dispatch(setDebugOpen(!open));
        }}
      >
        {open ? (
          <ChevronDown className="size-4" aria-hidden="true" />
        ) : (
          <ChevronUp className="size-4" aria-hidden="true" />
        )}
        <span>Debug</span>
        <span className="text-[11px] font-normal text-[oklch(0.56_0_0)]">
          {open ? 'erun -vv output' : 'collapsed'}
        </span>
      </button>
      {open && (
        <div className="flex items-center gap-1">
          <Button
            className="h-6 px-2 text-[11px] [&_svg]:size-3.5"
            type="button"
            variant="ghost"
            size="sm"
            disabled={!canCopy}
            onClick={copyDebugOutput}
          >
            {copyStatus === 'Copied' ? (
              <CheckCircle2 aria-hidden="true" />
            ) : (
              <Copy aria-hidden="true" />
            )}
            {copyStatus || 'Copy'}
          </Button>
          <Button
            className="h-6 px-2 text-[11px] [&_svg]:size-3.5"
            type="button"
            variant="ghost"
            size="sm"
            onClick={() => {
              dispatch(clearDebugOutput());
            }}
          >
            <Trash2 aria-hidden="true" />
            Clear
          </Button>
        </div>
      )}
    </div>
  );
}

function DebugPanelOutput({
  outputRef,
  handleScroll,
  output,
  sessionId,
  verbose,
}: {
  outputRef: React.RefObject<HTMLDivElement | null>;
  handleScroll: React.UIEventHandler<HTMLDivElement>;
  output: string;
  sessionId: number;
  verbose: boolean;
}): React.ReactElement {
  return (
    <div
      ref={outputRef}
      onScroll={handleScroll}
      className="min-h-0 overflow-auto px-3 py-2 font-mono text-[11px] leading-[1.35] text-[oklch(0.82_0_0)]"
    >
      {!verbose && sessionId > 0 && (
        <div className="mb-2 rounded border border-[oklch(0.32_0_0)] bg-[oklch(0.10_0_0)] px-3 py-2 text-[11px] leading-[1.4] text-[oklch(0.74_0_0)]">
          Active session was not started with <code>-vv</code>, so trace lines won't appear here. To
          capture verbose output: run <code>erun -vv &lt;command&gt;</code> in this shell, or open
          this Debug panel before launching commands from the UI.
        </div>
      )}
      <pre className="m-0 whitespace-pre-wrap break-words font-mono text-[11px] leading-[1.35]">
        {output ||
          'Run an environment command while Debug is expanded to stream erun -vv output here.'}
      </pre>
    </div>
  );
}
