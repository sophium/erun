import * as React from 'react';

// ErunTraceBaseline is the resolved state of the erun-trace "Clear" view
// baseline (issue #529).
export interface ErunTraceBaseline {
  // cleared is true once the operator has clicked Clear and has not yet
  // returned to the full view.
  cleared: boolean;
  // rotatedOut is true when the captured cut point has scrolled out of the
  // bounded on-disk tail, so the suffix can no longer be computed and the
  // pane has fallen back to showing everything.
  rotatedOut: boolean;
  // visibleContent is what the pane should render: the slice after the cut
  // point while it is still present, otherwise the full content.
  visibleContent: string;
  clear: () => void;
  showAll: () => void;
}

// useErunTraceBaseline implements the erun-trace Clear as a per-env view
// baseline: Clear records the trace content as it stands and the pane then
// renders only what arrives after it, so new lines stand out on a busy env.
// It is a view aid only — the persistent log is never truncated and callers
// keep reading the full content for Refresh / Copy / Copy report. It degrades
// safely against log rotation (the on-disk trace is a bounded tail, #508):
// the suffix is shown only while the current content still starts with the
// captured baseline; once the cut point rotates out, it falls back to showing
// all. The baseline resets whenever envKey changes so a stale cut point never
// carries across environments.
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
