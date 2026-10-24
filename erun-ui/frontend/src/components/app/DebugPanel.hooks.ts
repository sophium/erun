import * as React from 'react';

import { ClipboardSetText } from '../../../wailsjs/runtime/runtime';

export function useStickToBottom(content: string): {
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
    stuckToBottomRef.current = el.scrollHeight - el.scrollTop - el.clientHeight <= 4;
  }, []);
  React.useEffect(() => {
    const el = outputRef.current;
    if (el && stuckToBottomRef.current) {
      el.scrollTop = el.scrollHeight;
    }
  }, [content]);
  return { outputRef, handleScroll };
}

export function useCopyAction(text: string): { copyStatus: string; copy: () => void } {
  const [copyStatus, setCopyStatus] = React.useState('');
  const copy = React.useCallback(() => {
    if (!text.trim()) {
      return;
    }
    void ClipboardSetText(text)
      .then(() => {
        setCopyStatus('Copied');
        window.setTimeout(() => {
          setCopyStatus('');
        }, 1400);
      })
      .catch(() => {
        setCopyStatus('Copy failed');
      });
  }, [text]);
  return { copyStatus, copy };
}
