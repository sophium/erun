import * as React from 'react';

// ErunTraceBaseline is the resolved state of the erun-trace "Clear" view
// baseline.
export interface ErunTraceBaseline {
  cleared: boolean;
  rotatedOut: boolean;
  visibleContent: string;
  clear: () => void;
  showAll: () => void;
}

// useErunTraceBaseline implements erun-trace "Clear" as a per-env view aid: after
// Clear the pane shows only lines that arrive afterwards, so new activity stands
// out on a busy env. It never truncates the persistent log — Refresh / Copy / Copy
// report still read the full content — and it degrades safely when the bounded
// on-disk trace rotates past the cut point by falling back to showing everything.
// The baseline resets per env so a stale cut point never carries across environments.
export function useErunTraceBaseline(envKey: string, content: string): ErunTraceBaseline {
  const [baseline, setBaseline] = React.useState<string | null>(null);
  React.useEffect(() => {
    setBaseline(null);
  }, [envKey]);
  const clear = React.useCallback(() => {
    setBaseline(content);
  }, [content]);
  const showAll = React.useCallback(() => {
    setBaseline(null);
  }, []);
  const cleared = baseline !== null;
  const stillPrefix = baseline !== null && content.startsWith(baseline);
  return {
    cleared,
    rotatedOut: cleared && !stillPrefix,
    visibleContent: stillPrefix ? content.slice(baseline.length) : content,
    clear,
    showAll,
  };
}
