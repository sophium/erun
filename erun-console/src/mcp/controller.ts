import * as React from 'react';

import type { McpToken } from '../app/api/mcpApi';
import { useRequestMcpTokenMutation } from '../app/api/mcpApi';
import { queryErrorMessage } from '../app/queryError';
import type { McpToolResult } from './liveClient';
import { callMcpTool } from './liveClient';

export type McpTokenState =
  | { status: 'idle' }
  | { status: 'loading' }
  | { status: 'ready'; token: McpToken }
  | { status: 'error'; message: string };

export interface McpTokenController {
  state: McpTokenState;
  requestToken: (environmentId: string) => void;
}

// useMcpTokenController mints a per-env MCP token on demand. A single request,
// no polling; the active-ref guard drops a response that lands after unmount.
export function useMcpTokenController(token: string): McpTokenController {
  const [state, setState] = React.useState<McpTokenState>({ status: 'idle' });
  const [requestMcpToken] = useRequestMcpTokenMutation();

  const activeRef = React.useRef(true);
  React.useEffect(() => {
    activeRef.current = true;
    return () => {
      activeRef.current = false;
    };
  }, []);

  const requestToken = React.useCallback(
    (environmentId: string) => {
      setState({ status: 'loading' });
      requestMcpToken({ token, environmentId })
        .unwrap()
        .then((minted) => {
          if (activeRef.current) {
            setState({ status: 'ready', token: minted });
          }
        })
        .catch((error: unknown) => {
          if (activeRef.current) {
            setState({ status: 'error', message: queryErrorMessage(error) });
          }
        });
    },
    [token, requestMcpToken],
  );

  return { state, requestToken };
}

export type McpToolCallState =
  | { status: 'idle' }
  | { status: 'loading' }
  | { status: 'ready'; result: McpToolResult }
  | { status: 'error'; message: string };

export interface McpToolCallController {
  state: McpToolCallState;
  callTool: (hostname: string, token: string, toolName: string) => void;
}

// useMcpToolCallController drives one read-only tool call against a
// deployed environment's exposed MCP edge -- the console's first caller of
// the live edge, not just the token-minting half of it (see liveClient.ts).
export function useMcpToolCallController(): McpToolCallController {
  const [state, setState] = React.useState<McpToolCallState>({ status: 'idle' });

  const activeRef = React.useRef(true);
  React.useEffect(() => {
    activeRef.current = true;
    return () => {
      activeRef.current = false;
    };
  }, []);

  const callTool = React.useCallback((hostname: string, token: string, toolName: string) => {
    setState({ status: 'loading' });
    callMcpTool(hostname, token, toolName)
      .then((result) => {
        if (activeRef.current) {
          setState({ status: 'ready', result });
        }
      })
      .catch((error: unknown) => {
        if (activeRef.current) {
          setState({ status: 'error', message: queryErrorMessage(error) });
        }
      });
  }, []);

  return { state, callTool };
}
