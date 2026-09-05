import * as React from 'react';

import type { McpToolCallState } from './controller';

// Shared between DriveToolForm (MCPAccessPanel.tsx) and OperateToolForm --
// split out to avoid those two forms importing from each other.
export function DriveToolResult({ state }: { state: McpToolCallState }): React.ReactElement | null {
  if (state.status === 'ready') {
    return (
      <pre
        role="status"
        aria-live="polite"
        className={
          state.result.isError
            ? 'whitespace-pre-wrap text-sm text-destructive'
            : 'whitespace-pre-wrap text-sm text-muted-foreground'
        }
      >
        {state.result.text}
      </pre>
    );
  }
  if (state.status === 'error') {
    return (
      <p className="text-sm text-destructive" role="alert">
        Could not call the tool: {state.message}
      </p>
    );
  }
  return null;
}
