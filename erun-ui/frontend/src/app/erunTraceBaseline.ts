import * as React from 'react';

export interface ErunTraceBaseline {
  cleared: boolean;
  rotatedOut: boolean;
  visibleContent: string;
  clear: () => void;
  showAll: () => void;
}

// useErunTraceBaseline is a per-env view-only Clear: the persistent log is never
// truncated, and a cut point lost to log rotation falls back to showing everything.
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
