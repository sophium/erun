import * as React from 'react';

import type { McpToken } from '../app/api/mcpApi';
import { MCP_ATTACH_SCOPE, useRequestMcpTokenMutation } from '../app/api/mcpApi';
import { queryErrorMessage } from '../app/queryError';
import type { AttachHandle } from './attachClient';
import { openAttachSession } from './attachClient';
import type { McpToolResult } from './liveClient';
import { callMcpTool } from './liveClient';

export type McpTokenState =
  | { status: 'idle' }
  | { status: 'loading' }
  | { status: 'ready'; token: McpToken }
  | { status: 'error'; message: string };

export interface McpTokenController {
  state: McpTokenState;
  requestToken: (environmentId: string, scope?: string) => void;
}

// The active-ref guard drops a response that lands after unmount.
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
    (environmentId: string, scope?: string) => {
      setState({ status: 'loading' });
      requestMcpToken({ token, environmentId, scope })
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
  callTool: (
    hostname: string,
    token: string,
    toolName: string,
    args?: Record<string, unknown>,
  ) => void;
}

export function useMcpToolCallController(): McpToolCallController {
  const [state, setState] = React.useState<McpToolCallState>({ status: 'idle' });

  const activeRef = React.useRef(true);
  React.useEffect(() => {
    activeRef.current = true;
    return () => {
      activeRef.current = false;
    };
  }, []);

  const callTool = React.useCallback(
    (hostname: string, token: string, toolName: string, args?: Record<string, unknown>) => {
      setState({ status: 'loading' });
      callMcpTool(hostname, token, toolName, args)
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
    },
    [],
  );

  return { state, callTool };
}

export type AttachSessionState =
  | { status: 'idle' }
  | { status: 'minting' }
  | { status: 'connecting' }
  | { status: 'connected' }
  | { status: 'ended'; outcome: string }
  | { status: 'error'; message: string };

export interface AttachSessionController {
  state: AttachSessionState;
  scrollback: string;
  connect: (environmentId: string, hostname: string, session: string) => void;
  sendLine: (line: string) => void;
  disconnect: () => void;
}

// Mints its own erun:attach token; connecting again closes the open session. Line-based
// scrollback and input, not a full terminal emulator.
export function useAttachSessionController(token: string): AttachSessionController {
  const [state, setState] = React.useState<AttachSessionState>({ status: 'idle' });
  const [scrollback, setScrollback] = React.useState('');
  const [requestMcpToken] = useRequestMcpTokenMutation();
  const handleRef = React.useRef<AttachHandle | null>(null);
  const disconnectingRef = React.useRef(false);
  const decoderRef = React.useRef(new TextDecoder());
  const activeRef = React.useRef(true);

  React.useEffect(() => {
    activeRef.current = true;
    return () => {
      activeRef.current = false;
      handleRef.current?.close();
    };
  }, []);

  const openSession = React.useCallback((hostname: string, mcpToken: string, session: string) => {
    setState({ status: 'connecting' });
    handleRef.current = openAttachSession(hostname, mcpToken, session, {
      onOpen: () => {
        setState({ status: 'connected' });
      },
      onData: (chunk) => {
        setScrollback((prev) => prev + decoderRef.current.decode(chunk, { stream: true }));
      },
      onOutcome: (outcome) => {
        if (!disconnectingRef.current) {
          setState({ status: 'ended', outcome });
        }
      },
      onError: (message) => {
        setState({ status: 'error', message });
      },
    });
  }, []);

  const connect = React.useCallback(
    (environmentId: string, hostname: string, session: string) => {
      handleRef.current?.close();
      disconnectingRef.current = false;
      setScrollback('');
      setState({ status: 'minting' });
      requestMcpToken({ token, environmentId, scope: MCP_ATTACH_SCOPE })
        .unwrap()
        .then((minted) => {
          if (activeRef.current) {
            openSession(hostname, minted.token, session);
          }
        })
        .catch((error: unknown) => {
          if (activeRef.current) {
            setState({ status: 'error', message: queryErrorMessage(error) });
          }
        });
    },
    [token, requestMcpToken, openSession],
  );

  const sendLine = React.useCallback((line: string) => {
    handleRef.current?.send(new TextEncoder().encode(line + '\n'));
  }, []);

  const disconnect = React.useCallback(() => {
    disconnectingRef.current = true;
    handleRef.current?.close();
    setState({ status: 'idle' });
  }, []);

  return { state, scrollback, connect, sendLine, disconnect };
}
