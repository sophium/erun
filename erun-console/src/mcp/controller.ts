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

// useMcpTokenController mints a per-env MCP token on demand. A single request,
// no polling; the active-ref guard drops a response that lands after unmount.
// scope defaults to the mutation's own default (MCP_ADMIN_SCOPE) when omitted.
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

// useMcpToolCallController drives one tool call against a deployed
// environment's exposed MCP edge -- the console's first caller of the live
// edge, not just the token-minting half of it (see liveClient.ts). Used both
// for the admin-scope smoke test (`version`, no args) and for driving an
// erun:operate-scoped tool (`deploy`/`context_start`/`context_stop`/
// `resize`, each with its own real arguments).
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

// useAttachSessionController owns the whole mint-then-connect flow for one
// WebSocket attach session at a time: it mints its own erun:attach-scoped
// token (deliberately narrower than the erun:admin token DriveToolForm mints
// -- an attach session only ever needs to drive the byte stream, never
// raw/build/deploy/delete) and opens the socket once minting succeeds.
// Connecting again closes whatever was open first.
//
// This is deliberately not a full terminal emulator: scrollback is decoded
// raw bytes appended to a plain string, and input is sent a line at a time
// rather than keystroke by keystroke. A richer view (xterm.js, matching
// erun-ui/frontend's own terminal rendering) is a follow-up once erun-kit
// carries a shared terminal widget; this is the smallest slice that proves
// the browser can drive a live session at all.
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
